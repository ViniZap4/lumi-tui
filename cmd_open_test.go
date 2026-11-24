package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vinizap/lumi/tui-client/books"
	"github.com/vinizap/lumi/tui-client/feed"
)

func withOpenHooks(t *testing.T, goos string) *[][]string {
	t.Helper()
	var calls [][]string
	oldR, oldG := openRunner, openGOOS
	openRunner = func(name string, args ...string) error {
		calls = append(calls, append([]string{name}, args...))
		return nil
	}
	openGOOS = goos
	t.Cleanup(func() { openRunner, openGOOS = oldR, oldG })
	return &calls
}

func seedOpenBooks(t *testing.T) (home, local, bound string) {
	home = t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CACHE_HOME", filepath.Join(home, ".cache"))
	local = filepath.Join(home, "notas")
	bound = filepath.Join(home, "work")
	os.MkdirAll(filepath.Join(local, "dir"), 0o755)
	os.MkdirAll(bound, 0o755)
	os.WriteFile(filepath.Join(local, "dir", "ideia.md"), []byte("---\nid: ideia\ntitle: Ideia\n---\n# x\n"), 0o644)
	os.WriteFile(filepath.Join(bound, "plano.md"), []byte("---\nid: plano-id\ntitle: Plano\n---\n# y\n"), 0o644)
	st, err := books.Open()
	if err != nil {
		t.Fatal(err)
	}
	st.Registry.Add(books.Book{ID: "11111111-1111-4111-8111-111111111111", Name: "Notas", Kind: books.KindLocal, Path: local})
	st.Registry.Add(books.Book{ID: "22222222-2222-4222-8222-222222222222", Name: "Work", Kind: books.KindBound,
		ServerURL: "https://lumi-api.example.com", Account: "ana", VaultID: "33333333-3333-4333-8333-333333333333", Path: bound})
	if err := st.Save(); err != nil {
		t.Fatal(err)
	}
	return
}

func TestOpenMacOSBoundNote(t *testing.T) {
	seedOpenBooks(t)
	calls := withOpenHooks(t, "darwin")
	var out, errb bytes.Buffer
	if code := runOpenCmd([]string{"Work/plano"}, &out, &errb); code != 0 {
		t.Fatalf("%d %s", code, errb.String())
	}
	want := "lumi://open?server=https%3A%2F%2Flumi-api.example.com&account=ana&vault=33333333-3333-4333-8333-333333333333&note=plano-id"
	if len(*calls) != 1 || (*calls)[0][0] != "open" || (*calls)[0][1] != want {
		t.Fatalf("calls %v", *calls)
	}
}

func TestOpenUsesSyncCacheID(t *testing.T) {
	seedOpenBooks(t)
	dir, _ := feed.CacheDir()
	c := feed.LoadCache(dir, "22222222-2222-4222-8222-222222222222", "33333333-3333-4333-8333-333333333333", "https://lumi-api.example.com")
	c.Notes["server-note-7"] = feed.NoteEntry{Path: "plano.md"}
	if err := c.Save(dir, "22222222-2222-4222-8222-222222222222"); err != nil {
		t.Fatal(err)
	}
	withOpenHooks(t, "linux")
	var out, errb bytes.Buffer
	if code := runOpenCmd([]string{"work/plano.md", "--print"}, &out, &errb); code != 0 {
		t.Fatalf("%d %s", code, errb.String())
	}
	if got := strings.TrimSpace(out.String()); got != "https://lumi.example.com/#/b/33333333-3333-4333-8333-333333333333/n/server-note-7" {
		t.Fatalf("url %q", got)
	}
}

func TestOpenLinuxWebAndLocalBooks(t *testing.T) {
	seedOpenBooks(t)
	calls := withOpenHooks(t, "linux")
	var out, errb bytes.Buffer
	if code := runOpenCmd([]string{"2"}, &out, &errb); code != 0 {
		t.Fatalf("%s", errb.String())
	}
	if (*calls)[0][0] != "xdg-open" || (*calls)[0][1] != "https://lumi.example.com/#/b/33333333-3333-4333-8333-333333333333" {
		t.Fatalf("calls %v", *calls)
	}
	*calls = nil
	errb.Reset()
	if code := runOpenCmd([]string{"Notas/dir/ideia"}, &out, &errb); code == 0 || len(*calls) != 0 || !strings.Contains(errb.String(), "local book") {
		t.Fatalf("local on linux: %d %v %s", code, *calls, errb.String())
	}
	withOpenHooks(t, "darwin")
	out.Reset()
	if code := runOpenCmd([]string{"Notas/dir/ideia", "--print"}, &out, &errb); code != 0 {
		t.Fatal(errb.String())
	}
	if !strings.HasPrefix(out.String(), "lumi://open?path=") || !strings.Contains(out.String(), "note=dir%2Fideia.md") {
		t.Fatalf("local app url %q", out.String())
	}
}

func TestOpenRejectsTraversalAndUnknown(t *testing.T) {
	seedOpenBooks(t)
	calls := withOpenHooks(t, "darwin")
	var out, errb bytes.Buffer
	for _, ref := range []string{"Notas/../../etc/passwd", "Nope/x", "Work/missing"} {
		errb.Reset()
		if code := runOpenCmd([]string{ref}, &out, &errb); code == 0 {
			t.Errorf("%s accepted", ref)
		}
	}
	if len(*calls) != 0 {
		t.Fatalf("opened something: %v", *calls)
	}
	if code := runOpenCmd([]string{"--bogus"}, &out, &errb); code != 64 {
		t.Fatal("bad flag")
	}
}

func TestOpenUnknownWebOriginHint(t *testing.T) {
	home, _, _ := seedOpenBooks(t)
	_ = home
	st, _ := books.Open()
	st.Registry.Books[1].ServerURL = "https://notes.example.com"
	st.Save()
	calls := withOpenHooks(t, "linux")
	var out, errb bytes.Buffer
	if code := runOpenCmd([]string{"Work"}, &out, &errb); code == 0 || !strings.Contains(errb.String(), "web_origins") || len(*calls) != 0 {
		t.Fatalf("%d %s %v", code, errb.String(), *calls)
	}
}

// The TUI's gx on a mermaid diagram runs `lumi open <absolute note
// path>`: the book is found from the path's folder, the note from the
// rest (bound books by note id, local books by relative path).
func TestOpenAbsoluteNotePath(t *testing.T) {
	_, local, bound := seedOpenBooks(t)
	calls := withOpenHooks(t, "darwin")
	var out, errb bytes.Buffer
	if code := runOpenCmd([]string{filepath.Join(bound, "plano.md")}, &out, &errb); code != 0 {
		t.Fatalf("%d %s", code, errb.String())
	}
	if len(*calls) != 1 || !strings.HasSuffix((*calls)[0][1], "&note=plano-id") {
		t.Fatalf("bound: calls %v", *calls)
	}
	out.Reset()
	if code := runOpenCmd([]string{filepath.Join(local, "dir", "ideia.md"), "--print"}, &out, &errb); code != 0 {
		t.Fatalf("%d %s", code, errb.String())
	}
	if !strings.HasSuffix(strings.TrimSpace(out.String()), "&note=dir%2Fideia.md") {
		t.Fatalf("local: %q", out.String())
	}
}
