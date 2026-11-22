package feed

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vinizap/lumi/tui-client/account"
	"github.com/vinizap/lumi/tui-client/domain"
	"github.com/vinizap/lumi/tui-client/filesystem"
	"github.com/vinizap/lumi/tui-client/internal/fakeserver"
)

type pushEnv struct {
	t     *testing.T
	srv   *fakeserver.Server
	cl    *account.Client
	root  string
	cache string
	s     *Syncer
}

func newPushEnv(t *testing.T) *pushEnv {
	t.Helper()
	srv := fakeserver.New("v1", "notes")
	t.Cleanup(srv.Close)
	cl, err := account.NewClient(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	cl.SetToken("tok")
	e := &pushEnv{t: t, srv: srv, cl: cl, root: t.TempDir(), cache: t.TempDir()}
	e.s = e.syncer()
	return e
}

// syncer builds a fresh Syncer over the persisted cache, as a new process
// would.
func (e *pushEnv) syncer() *Syncer {
	c := LoadCache(e.cache, "book1", "v1", e.srv.URL)
	return &Syncer{API: e.cl, VaultID: "v1", Root: e.root, Cache: c, Bases: BasesFor(e.cache, "book1"),
		Now: func() time.Time { return time.Date(2026, 11, 22, 15, 4, 5, 0, time.UTC) }}
}

func (e *pushEnv) path(rel string) string { return filepath.Join(e.root, rel) }

// pull writes the server note into the folder and records its base, as
// the mirror/clone/sync pull do.
func (e *pushEnv) pull(id, rel string) {
	e.t.Helper()
	snap, err := e.cl.GetNoteSnapshot(context.Background(), "v1", id)
	if err != nil {
		e.t.Fatal(err)
	}
	n := &domain.Note{ID: id, Title: "T " + id, Path: e.path(rel), Content: snap.Text, HadFrontmatter: true, Tags: []string{"x"}}
	_ = os.MkdirAll(filepath.Dir(n.Path), 0o755)
	if err := filesystem.WriteNote(n); err != nil {
		e.t.Fatal(err)
	}
	if err := e.s.Record(id, n.Path, snap); err != nil {
		e.t.Fatal(err)
	}
	if err := e.s.Cache.Save(e.cache, "book1"); err != nil {
		e.t.Fatal(err)
	}
}

func (e *pushEnv) editLocal(rel, body string) {
	e.t.Helper()
	n, err := filesystem.ReadNote(e.path(rel))
	if err != nil {
		e.t.Fatal(err)
	}
	n.Content = body
	if err := filesystem.WriteNote(n); err != nil {
		e.t.Fatal(err)
	}
}

func (e *pushEnv) read(rel string) *domain.Note {
	e.t.Helper()
	n, err := filesystem.ReadNote(e.path(rel))
	if err != nil {
		e.t.Fatal(err)
	}
	return n
}

func (e *pushEnv) push(rel string) PushResult {
	e.t.Helper()
	res, err := e.s.Push(context.Background(), e.path(rel))
	if err != nil {
		e.t.Fatalf("push %s: %v", rel, err)
	}
	return res
}

func (e *pushEnv) assertBase(id, text string) {
	e.t.Helper()
	b, ok := e.s.Bases.Load(id)
	if !ok || b.Text != text || b.State == "" {
		e.t.Fatalf("base for %s = %+v, want text %q", id, b, text)
	}
	if e.s.Cache.Notes[id].Hash != BodyHash(text) {
		e.t.Fatalf("cache hash for %s does not match base", id)
	}
}

// V9a regression: the remote edit the TUI never saw must survive (V9b).
func TestPushMergesConcurrentRemoteEdit(t *testing.T) {
	e := newPushEnv(t)
	e.srv.AddNote("a", "a.md", "hello world")
	e.pull("a", "a.md")
	e.srv.RemoteEdit("a", "# hello world") // web, concurrently
	e.editLocal("a.md", "hello brave world")

	res := e.push("a.md")
	if res.Outcome != PushMerged {
		t.Fatalf("outcome = %v", res.Outcome)
	}
	if got := e.srv.Text("a"); got != "# hello brave world" {
		t.Fatalf("server text = %q (remote edit lost?)", got)
	}
	n := e.read("a.md")
	if n.Content != "# hello brave world" || n.ID != "a" || n.Title != "T a" || len(n.Tags) != 1 {
		t.Fatalf("local note = %+v", n)
	}
	d := e.srv.Diffs()
	if len(d) != 1 || d[0].Origin != "tui" || d[0].BaseState == "" || d[0].BaseClock != "" || d[0].Text != "hello brave world" {
		t.Fatalf("diff requests = %+v", d)
	}
	e.assertBase("a", "# hello brave world")
	if p := e.s.Pending(); len(p) != 0 {
		t.Fatalf("pending after push = %v", p)
	}
}

func TestPushNoopWhenFileMatchesBase(t *testing.T) {
	e := newPushEnv(t)
	e.srv.AddNote("a", "a.md", "hello")
	e.pull("a", "a.md")
	if res := e.push("a.md"); res.Outcome != PushNoop {
		t.Fatalf("outcome = %v", res.Outcome)
	}
	if len(e.srv.Diffs()) != 0 {
		t.Fatal("no-op push hit the server")
	}
}

func TestPushKeepsBaseWhitespace(t *testing.T) {
	e := newPushEnv(t)
	e.srv.AddNote("a", "a.md", "hello world\n")
	e.pull("a", "a.md")
	e.editLocal("a.md", "hello brave world")
	e.push("a.md")
	if got := e.srv.Text("a"); got != "hello brave world\n" {
		t.Fatalf("server text = %q (trailing newline deleted)", got)
	}
}

// base_mismatch with an unchanged remote text: re-pull and retry once on
// the fresh snapshot — the edit lands, no conflict copy.
func TestPushBaseMismatchRetriesOnFreshSnapshot(t *testing.T) {
	e := newPushEnv(t)
	e.srv.AddNote("a", "a.md", "hello world")
	e.pull("a", "a.md")
	e.srv.ForgetHistory()
	e.editLocal("a.md", "hello brave world")

	res := e.push("a.md")
	if res.Outcome != PushMerged || res.ConflictPath != "" {
		t.Fatalf("res = %+v", res)
	}
	if e.srv.Text("a") != "hello brave world" || len(e.srv.Diffs()) != 2 {
		t.Fatalf("server %q, diffs %d", e.srv.Text("a"), len(e.srv.Diffs()))
	}
	e.assertBase("a", "hello brave world")
}

// The /diff reply's `state` is the next base: no extra GET /snapshot,
// and a follow-up push merges on it.
func TestPushUsesDiffStateAsNextBase(t *testing.T) {
	e := newPushEnv(t)
	e.srv.AddNote("a", "a.md", "hello world")
	e.pull("a", "a.md")
	snaps := e.srv.Snapshots()
	e.srv.RemoteEdit("a", "# hello world")
	e.editLocal("a.md", "hello brave world")

	if res := e.push("a.md"); res.Outcome != PushMerged {
		t.Fatalf("outcome = %v", res.Outcome)
	}
	if got := e.srv.Snapshots(); got != snaps {
		t.Fatalf("snapshots = %d, want %d (state from /diff not used)", got, snaps)
	}
	e.assertBase("a", "# hello brave world")

	e.srv.RemoteEdit("a", "# hello brave world!")
	e.editLocal("a.md", "# hi brave world")
	if res := e.push("a.md"); res.Outcome != PushMerged || res.ConflictPath != "" {
		t.Fatalf("second push = %+v", res)
	}
	if got := e.srv.Text("a"); got != "# hi brave world!" {
		t.Fatalf("server text = %q", got)
	}
	if d := e.srv.Diffs(); len(d) != 2 {
		t.Fatalf("diffs = %d (second push needed a retry?)", len(d))
	}
	if got := e.srv.Snapshots(); got != snaps {
		t.Fatalf("snapshots = %d, want %d", got, snaps)
	}
	e.assertBase("a", "# hi brave world!")
}

// Older servers: /diff has no `state`, the next base comes from GET
// /snapshot.
func TestPushWithoutDiffStateFetchesSnapshot(t *testing.T) {
	e := newPushEnv(t)
	e.srv.DiffOmitState = true
	e.srv.AddNote("a", "a.md", "hello world")
	e.pull("a", "a.md")
	snaps := e.srv.Snapshots()
	e.srv.RemoteEdit("a", "# hello world")
	e.editLocal("a.md", "hello brave world")

	if res := e.push("a.md"); res.Outcome != PushMerged {
		t.Fatalf("outcome = %v", res.Outcome)
	}
	if got := e.srv.Snapshots(); got != snaps+1 {
		t.Fatalf("snapshots = %d, want %d", got, snaps+1)
	}
	if e.read("a.md").Content != "# hello brave world" {
		t.Fatalf("local = %q", e.read("a.md").Content)
	}
	e.assertBase("a", "# hello brave world")
}

// base_too_old with an unchanged remote text: re-pull and retry once on
// the fresh snapshot, like base_mismatch.
func TestPushBaseTooOldRetriesOnFreshSnapshot(t *testing.T) {
	e := newPushEnv(t)
	e.srv.AddNote("a", "a.md", "hello world")
	e.pull("a", "a.md")
	e.srv.ExpireHistory()
	e.editLocal("a.md", "hello brave world")

	res := e.push("a.md")
	if res.Outcome != PushMerged || res.ConflictPath != "" {
		t.Fatalf("res = %+v", res)
	}
	d := e.srv.Diffs()
	if e.srv.Text("a") != "hello brave world" || len(d) != 2 || d[0].BaseState == d[1].BaseState {
		t.Fatalf("server %q, diffs %+v", e.srv.Text("a"), d)
	}
	e.assertBase("a", "hello brave world")
}

// base_too_old with both sides changed: keep both, nothing lost.
func TestPushBaseTooOldBothChangedKeepsBoth(t *testing.T) {
	e := newPushEnv(t)
	e.srv.AddNote("a", "a.md", "hello world")
	e.pull("a", "a.md")
	e.srv.ExpireHistory()
	e.srv.RemoteEdit("a", "# hello world")
	e.editLocal("a.md", "hello brave world")

	res := e.push("a.md")
	if res.Outcome != PushConflict || res.ConflictPath == "" {
		t.Fatalf("res = %+v", res)
	}
	if got := e.read("a.md").Content; got != "# hello world" {
		t.Fatalf("note = %q", got)
	}
	if got := e.read(res.ConflictPath).Content; got != "hello brave world" {
		t.Fatalf("conflict copy = %q", got)
	}
	if e.srv.Text("a") != "# hello world" || len(e.srv.Diffs()) != 1 {
		t.Fatalf("server %q, diffs %d", e.srv.Text("a"), len(e.srv.Diffs()))
	}
	e.assertBase("a", "# hello world")
}

// base_required / base_mismatch / base_too_old with both sides changed:
// keep both.
func TestPushBaseConflictBothChangedKeepsBoth(t *testing.T) {
	for _, code := range []string{"base_required", "base_mismatch", "base_too_old"} {
		t.Run(code, func(t *testing.T) {
			e := newPushEnv(t)
			e.srv.AddNote("a", "dir/a.md", "hello world")
			e.pull("a", "dir/a.md")
			e.srv.RemoteEdit("a", "# hello world")
			e.srv.Fail409 = []string{code}
			e.editLocal("dir/a.md", "hello brave world")

			res := e.push("dir/a.md")
			if res.Outcome != PushConflict {
				t.Fatalf("outcome = %v", res.Outcome)
			}
			if res.ConflictPath != "dir/a.conflict-20261122-150405.md" {
				t.Fatalf("conflict path = %q", res.ConflictPath)
			}
			if got := e.read("dir/a.md"); got.Content != "# hello world" || got.ID != "a" {
				t.Fatalf("note = %+v", got)
			}
			cp := e.read(res.ConflictPath)
			if cp.Content != "hello brave world" || cp.ID != "" || !strings.Contains(cp.Title, "conflito") {
				t.Fatalf("conflict copy = %+v", cp)
			}
			if e.srv.Text("a") != "# hello world" {
				t.Fatalf("server overwritten: %q", e.srv.Text("a"))
			}
			e.assertBase("a", "# hello world")
			if p := e.s.Pending(); len(p) != 0 {
				t.Fatalf("pending = %v", p)
			}
		})
	}
}

// No stored base (e.g. a folder synced before bases existed) but the
// remote text is still what this client last wrote: the snapshot is a
// valid base.
func TestPushWithoutBaseRemoteUnchangedUsesSnapshot(t *testing.T) {
	e := newPushEnv(t)
	e.srv.AddNote("a", "a.md", "hello world")
	e.pull("a", "a.md")
	_ = e.s.Bases.Delete("a")
	e.editLocal("a.md", "hello brave world")
	if res := e.push("a.md"); res.Outcome != PushMerged {
		t.Fatalf("outcome = %v", res.Outcome)
	}
	if e.srv.Text("a") != "hello brave world" {
		t.Fatalf("server = %q", e.srv.Text("a"))
	}
	e.assertBase("a", "hello brave world")
}

// No base and no record, texts differ: nothing to merge against.
func TestPushWithoutAnyBaseBothDifferentKeepsBoth(t *testing.T) {
	e := newPushEnv(t)
	e.srv.AddNote("a", "a.md", "server text")
	n := &domain.Note{ID: "a", Title: "A", Path: e.path("a.md"), Content: "local text", HadFrontmatter: true}
	if err := filesystem.WriteNote(n); err != nil {
		t.Fatal(err)
	}
	res := e.push("a.md")
	if res.Outcome != PushConflict || len(e.srv.Diffs()) != 0 {
		t.Fatalf("res = %+v diffs = %d", res, len(e.srv.Diffs()))
	}
	if e.read("a.md").Content != "server text" || e.read(res.ConflictPath).Content != "local text" {
		t.Fatal("both versions not kept")
	}
}

// The file changes while the push is in flight: the newer local text is
// kept as a conflict copy, never overwritten.
func TestPushFileEditedDuringPushIsKept(t *testing.T) {
	e := newPushEnv(t)
	e.srv.AddNote("a", "a.md", "hello world")
	e.pull("a", "a.md")
	e.editLocal("a.md", "hello brave world")
	e.srv.OnDiff = func(fakeserver.DiffRequest) { e.editLocal("a.md", "hello brave new world") }
	res := e.push("a.md")
	if res.Outcome != PushConflict {
		t.Fatalf("outcome = %v", res.Outcome)
	}
	if e.read(res.ConflictPath).Content != "hello brave new world" {
		t.Fatal("in-flight edit lost")
	}
	if e.read("a.md").Content != "hello brave world" {
		t.Fatalf("note = %q", e.read("a.md").Content)
	}
}

func TestBasePersistsInCacheDirNotNotesFolder(t *testing.T) {
	e := newPushEnv(t)
	e.srv.AddNote("a", "a.md", "hello")
	e.pull("a", "a.md")

	entries, _ := os.ReadDir(filepath.Join(e.cache, "book1.bases"))
	if len(entries) != 1 {
		t.Fatalf("base files = %v", entries)
	}
	fi, _ := entries[0].Info()
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("base perm = %v", fi.Mode().Perm())
	}
	_ = filepath.WalkDir(e.root, func(p string, d os.DirEntry, err error) error {
		if !d.IsDir() && p != e.path("a.md") {
			t.Errorf("unexpected file in notes folder: %s", p)
		}
		return nil
	})
	// A new process sees the same base.
	s2 := e.syncer()
	b, ok := s2.Bases.Load("a")
	if !ok || b.Text != "hello" || b.State == "" || s2.Cache.Notes["a"].Hash != BodyHash("hello") {
		t.Fatalf("reloaded base = %+v ok=%v cache=%+v", b, ok, s2.Cache.Notes)
	}
}

func TestPendingEditsAndOfflinePush(t *testing.T) {
	e := newPushEnv(t)
	e.srv.AddNote("a", "a.md", "alpha")
	e.srv.AddNote("b", "sub/b.md", "beta")
	e.pull("a", "a.md")
	e.pull("b", "sub/b.md")
	if p := e.s.Pending(); len(p) != 0 {
		t.Fatalf("pending = %v", p)
	}
	e.editLocal("sub/b.md", "beta 2")
	if p := e.s.Pending(); len(p) != 1 || p[0] != "sub/b.md" {
		t.Fatalf("pending = %v", p)
	}

	// Offline: the push fails, nothing is queued, the file and its base
	// stay as they were — still pending.
	e.srv.Close()
	if _, err := e.s.Push(context.Background(), e.path("sub/b.md")); err == nil || !isNetworkErr(err) {
		t.Fatalf("offline push err = %v", err)
	}
	if e.read("sub/b.md").Content != "beta 2" {
		t.Fatal("offline push touched the file")
	}
	e.assertBase("b", "beta")
	if p := e.syncer().Pending(); len(p) != 1 {
		t.Fatalf("pending after offline push = %v", p)
	}
}

func TestPushRequiresServerState(t *testing.T) {
	e := newPushEnv(t)
	e.srv.OmitState = true // pre-S9 server
	e.srv.AddNote("a", "a.md", "hello")
	e.pull("a", "a.md")
	if _, ok := e.s.Bases.Load("a"); ok {
		t.Fatal("stored a base without state")
	}
	e.editLocal("a.md", "hello there")
	e.srv.RemoteEdit("a", "# hello")
	res := e.push("a.md")
	if res.Outcome != PushConflict || len(e.srv.Diffs()) != 0 {
		t.Fatalf("res = %+v, diffs = %+v (bare text must never be sent)", res, e.srv.Diffs())
	}
}

// The feed-driven path: a catch-up that finds a dirty note the server
// also changed merges it instead of skipping it.
func TestSyncerCatchUpMergesDirtyNote(t *testing.T) {
	e := newPushEnv(t)
	e.srv.AddNote("a", "a.md", "hello world")
	e.srv.AddNote("b", "b.md", "bee")
	res, err := e.s.CatchUp(context.Background(), e.cl)
	if err != nil || res.Stats.Written != 2 {
		t.Fatalf("first catch-up: %+v %v", res, err)
	}
	e.assertBase("a", "hello world")

	e.editLocal("a.md", "hello brave world")
	e.srv.RemoteEdit("a", "# hello world")
	e.srv.RemoteEdit("b", "bee 2")
	res, err = e.s.CatchUp(context.Background(), e.cl)
	if err != nil {
		t.Fatal(err)
	}
	if res.Stats.Merged != 1 || res.Stats.Written != 2 || len(res.Stats.Dirty) != 0 || len(res.Stats.Conflicts) != 0 {
		t.Fatalf("stats = %+v", res.Stats)
	}
	if e.read("a.md").Content != "# hello brave world" || e.srv.Text("a") != "# hello brave world" {
		t.Fatalf("a local %q server %q", e.read("a.md").Content, e.srv.Text("a"))
	}
	if e.read("b.md").Content != "bee 2" {
		t.Fatal("clean note not pulled")
	}
	e.assertBase("b", "bee 2")

	// A local-only edit is not pushed by the mirror (that is the flush's
	// job) and stays pending.
	e.editLocal("b.md", "bee 3")
	e.srv.RemoteEdit("a", "# hello brave world!")
	if _, err := e.s.CatchUp(context.Background(), e.cl); err != nil {
		t.Fatal(err)
	}
	if p := e.s.Pending(); len(p) != 1 || p[0] != "b.md" {
		t.Fatalf("pending = %v", p)
	}
	if e.srv.Text("b") != "bee 2" {
		t.Fatal("mirror pushed a local-only edit")
	}
}

func TestSyncerCatchUpNoPushLeavesDirtyAlone(t *testing.T) {
	e := newPushEnv(t)
	e.srv.AddNote("a", "a.md", "hello world")
	if _, err := e.s.CatchUp(context.Background(), e.cl); err != nil {
		t.Fatal(err)
	}
	e.s.NoPush = true
	e.editLocal("a.md", "mine")
	e.srv.RemoteEdit("a", "theirs")
	res, err := e.s.CatchUp(context.Background(), e.cl)
	if err != nil || len(res.Stats.Dirty) != 1 || len(e.srv.Diffs()) != 0 {
		t.Fatalf("res %+v err %v", res, err)
	}
	if e.read("a.md").Content != "mine" {
		t.Fatal("dirty note overwritten")
	}
}

func TestSendText(t *testing.T) {
	for _, c := range []struct{ base, body, want string }{
		{"hello\n", "hi", "hi\n"},
		{"\n\nhello  \n", "hi", "\n\nhi  \n"},
		{"", "hi", "hi"},
		{"x", "", ""},
	} {
		if got := sendText(c.base, c.body); got != c.want {
			t.Errorf("sendText(%q,%q) = %q want %q", c.base, c.body, got, c.want)
		}
	}
}

func TestIsBaseConflictIsNotNetwork(t *testing.T) {
	if isNetworkErr(account.ErrBaseMismatch) || errors.Is(account.ErrBaseMismatch, account.ErrBaseRequired) {
		t.Fatal("sentinels confused")
	}
}
