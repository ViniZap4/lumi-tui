package feed

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vinizap/lumi/tui-client/account"
	"github.com/vinizap/lumi/tui-client/filesystem"
)

type fakeAPI struct {
	pages   map[int64]*account.ChangesPage // keyed by after
	gone    map[int64]int64                // after -> head for 410
	notes   []account.RemoteNote
	texts   map[string]string
	calls   []int64
	failFor string
}

func (f *fakeAPI) Changes(_ context.Context, _ string, after int64, _ int) (*account.ChangesPage, error) {
	f.calls = append(f.calls, after)
	if h, ok := f.gone[after]; ok {
		return nil, &account.ResyncRequiredError{Head: h}
	}
	if p, ok := f.pages[after]; ok {
		return p, nil
	}
	return &account.ChangesPage{Head: after}, nil
}
func (f *fakeAPI) ListAllNotes(context.Context, string) ([]account.RemoteNote, error) {
	return f.notes, nil
}
func (f *fakeAPI) GetNoteSnapshot(_ context.Context, _, id string) (*account.NoteSnapshot, error) {
	if id == f.failFor {
		return nil, errors.New("HTTP 500")
	}
	return &account.NoteSnapshot{ID: id, Text: f.texts[id]}, nil
}

func rn(id, path string, seq int64) account.ChangedNote {
	return account.ChangedNote{RemoteNote: account.RemoteNote{ID: id, Path: path, Title: strings.ToUpper(id)}, ChangeSeq: seq}
}

func newCache() *BookCache { return &BookCache{Notes: map[string]NoteEntry{}} }

func TestCatchUpPagesWritesAndAdvancesCursor(t *testing.T) {
	root := t.TempDir()
	api := &fakeAPI{
		pages: map[int64]*account.ChangesPage{
			0: {Notes: []account.ChangedNote{rn("a", "a.md", 1)}, Head: 5, More: true},
			1: {Notes: []account.ChangedNote{rn("b", "sub/b.md", 4)}, Deleted: []account.DeletedNote{{ID: "zz", Seq: 5}}, Head: 5},
		},
		texts: map[string]string{"a": "alpha\n", "b": "beta\n"},
	}
	c := newCache()
	res, err := CatchUp(context.Background(), api, "v", root, c)
	if err != nil {
		t.Fatal(err)
	}
	if c.Cursor != 5 || res.Head != 5 || res.Stats.Written != 2 {
		t.Fatalf("cursor %d res %+v", c.Cursor, res)
	}
	n, err := filesystem.ReadNote(filepath.Join(root, "sub", "b.md"))
	if err != nil || n.ID != "b" || n.Content != "beta" {
		t.Fatalf("b.md: %+v %v", n, err)
	}
}

func TestCatchUpNeverOverwritesDirtyNote(t *testing.T) {
	root := t.TempDir()
	api := &fakeAPI{
		pages: map[int64]*account.ChangesPage{0: {Notes: []account.ChangedNote{rn("a", "a.md", 1)}, Head: 1}},
		texts: map[string]string{"a": "v1\n"},
	}
	c := newCache()
	if _, err := CatchUp(context.Background(), api, "v", root, c); err != nil {
		t.Fatal(err)
	}
	// User edits the file locally.
	p := filepath.Join(root, "a.md")
	n, _ := filesystem.ReadNote(p)
	n.Content = "my local edit\n"
	filesystem.WriteNote(n)

	api.pages[1] = &account.ChangesPage{Notes: []account.ChangedNote{rn("a", "a.md", 2)}, Head: 2}
	api.texts["a"] = "v2 from web\n"
	res, err := CatchUp(context.Background(), api, "v", root, c)
	if err != nil {
		t.Fatal(err)
	}
	n, _ = filesystem.ReadNote(p)
	if n.Content != "my local edit" || len(res.Stats.Dirty) != 1 {
		t.Fatalf("dirty note clobbered: %q %+v", n.Content, res.Stats)
	}
	if c.Cursor != 2 {
		t.Errorf("cursor should still advance: %d", c.Cursor)
	}
}

func TestCatchUpUnrecordedFileIsDirtyUnlessEqual(t *testing.T) {
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "a.md"), []byte("---\nid: a\ntitle: A\n---\nsame\n"), 0o644)
	os.WriteFile(filepath.Join(root, "b.md"), []byte("---\nid: b\ntitle: B\n---\nold clone\n"), 0o644)
	api := &fakeAPI{
		pages: map[int64]*account.ChangesPage{0: {Notes: []account.ChangedNote{rn("a", "a.md", 1), rn("b", "b.md", 2)}, Head: 2}},
		texts: map[string]string{"a": "same\n", "b": "newer\n"},
	}
	c := newCache()
	res, err := CatchUp(context.Background(), api, "v", root, c)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := c.Notes["a"]; !ok {
		t.Error("equal unrecorded note should be adopted as clean")
	}
	if len(res.Stats.Dirty) != 1 || res.Stats.Dirty[0] != "b.md" {
		t.Errorf("dirty %+v", res.Stats.Dirty)
	}
}

func TestCatchUpDeletesOnlyCleanNotes(t *testing.T) {
	root := t.TempDir()
	api := &fakeAPI{
		pages: map[int64]*account.ChangesPage{0: {Notes: []account.ChangedNote{rn("a", "a.md", 1), rn("b", "b.md", 2)}, Head: 2}},
		texts: map[string]string{"a": "A\n", "b": "B\n"},
	}
	c := newCache()
	CatchUp(context.Background(), api, "v", root, c)
	nb, _ := filesystem.ReadNote(filepath.Join(root, "b.md"))
	nb.Content = "edited\n"
	filesystem.WriteNote(nb)
	api.pages[2] = &account.ChangesPage{Deleted: []account.DeletedNote{{ID: "a", Seq: 3}, {ID: "b", Seq: 4}}, Head: 4}
	res, err := CatchUp(context.Background(), api, "v", root, c)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "a.md")); !os.IsNotExist(err) {
		t.Error("clean a.md should be deleted")
	}
	if _, err := os.Stat(filepath.Join(root, "b.md")); err != nil {
		t.Error("dirty b.md must survive a remote delete")
	}
	if res.Stats.Deleted != 1 {
		t.Errorf("%+v", res.Stats)
	}
}

func TestCatchUp410ResyncRecordsHeadFirst(t *testing.T) {
	root := t.TempDir()
	api := &fakeAPI{
		gone:  map[int64]int64{7: 20},
		notes: []account.RemoteNote{{ID: "a", Path: "a.md", Title: "A"}},
		texts: map[string]string{"a": "fresh\n"},
		pages: map[int64]*account.ChangesPage{20: {Notes: []account.ChangedNote{rn("a", "a.md", 21)}, Head: 21}},
	}
	c := newCache()
	c.Cursor = 7
	// "old" was mirrored before and is gone from the server.
	os.WriteFile(filepath.Join(root, "old.md"), []byte("---\nid: old\ntitle: O\n---\nx\n"), 0o644)
	c.Notes["old"] = NoteEntry{Path: "old.md", Hash: BodyHash("x\n")}
	res, err := CatchUp(context.Background(), api, "v", root, c)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Resync || c.Cursor != 21 {
		t.Fatalf("res %+v cursor %d calls %v", res, c.Cursor, api.calls)
	}
	if got := api.calls; len(got) != 2 || got[0] != 7 || got[1] != 20 {
		t.Errorf("calls %v: after resync the next changes must start at the recorded head", got)
	}
	if _, err := os.Stat(filepath.Join(root, "old.md")); !os.IsNotExist(err) {
		t.Error("clean note missing from relist should be deleted")
	}
}

func TestCatchUpFailureKeepsCursor(t *testing.T) {
	api := &fakeAPI{
		pages:   map[int64]*account.ChangesPage{3: {Notes: []account.ChangedNote{rn("a", "a.md", 4)}, Head: 4}},
		failFor: "a",
	}
	c := newCache()
	c.Cursor = 3
	if _, err := CatchUp(context.Background(), api, "v", t.TempDir(), c); err == nil {
		t.Fatal("want error")
	}
	if c.Cursor != 3 {
		t.Errorf("cursor moved past an unapplied page: %d", c.Cursor)
	}
}

func TestCatchUpRejectsTraversal(t *testing.T) {
	api := &fakeAPI{pages: map[int64]*account.ChangesPage{0: {Notes: []account.ChangedNote{rn("a", "../evil.md", 1)}, Head: 1}}}
	if _, err := CatchUp(context.Background(), api, "v", t.TempDir(), newCache()); err == nil {
		t.Fatal("traversal path must be rejected")
	}
}
