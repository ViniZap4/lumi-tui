package bookclone

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vinizap/lumi/tui-client/account"
	"github.com/vinizap/lumi/tui-client/books"
)

const vaultID = "0b9d2a52-7c1e-4b38-9a43-1f2e3d4c5b6a"

func fakeServer(t *testing.T, status map[string]int) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if code := status[r.URL.Path]; code != 0 {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(code)
			_, _ = w.Write([]byte(`{"error":"conflict"}`))
			return
		}
		if r.Header.Get("X-Lumi-Token") != "tok" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":"unauthorized"}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/vaults":
			_, _ = w.Write([]byte(`{"vaults":[{"id":"` + vaultID + `","slug":"test","name":"Test","created_by":"u1","created_at":""}]}`))
		case "/api/vaults/" + vaultID + "/notes":
			_, _ = w.Write([]byte(`{"notes":[
				{"id":"n1","vault_id":"v","path":"hello.md","title":"Hello"},
				{"id":"n2","vault_id":"v","path":"sub/world.md","title":"World"}],"limit":200,"offset":0}`))
		case "/api/vaults/" + vaultID + "/notes/n1/content":
			_, _ = w.Write([]byte(`{"id":"n1","path":"hello.md","body":"# Hello\n"}`))
		case "/api/vaults/" + vaultID + "/notes/n2/content":
			_, _ = w.Write([]byte(`{"id":"n2","path":"sub/world.md","body":"# World\n"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func homeEnv(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(home, ".cache"))
	return home
}

func client(t *testing.T, url, token string) *account.Client {
	c, err := account.NewClient(url)
	if err != nil {
		t.Fatal(err)
	}
	c.SetToken(token)
	return c
}

func TestRunByVaultIDRegistersBookAndReportsProgress(t *testing.T) {
	home := homeEnv(t)
	srv := fakeServer(t, nil)
	reg := &books.Registry{Version: books.FormatVersion}
	reg.Add(books.Book{ID: vaultID, Name: "Test", Kind: books.KindBound, ServerURL: srv.URL, Account: "alice", VaultID: vaultID})
	dest := filepath.Join(home, "lumi", "test")
	var ticks []int
	total := 0
	res, err := Run(context.Background(), Options{
		Client: client(t, srv.URL, "tok"), Server: srv.URL, Account: "alice", VaultID: vaultID, Path: dest,
		OnStart: func(_ account.RemoteVault, n int) { total = n },
		OnNote:  func(done, _ int, _ string, _ bool) { ticks = append(ticks, done) },
		UpdateBooks: func(fn func(*books.Registry) bool) error {
			fn(reg)
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if total != 2 || len(ticks) != 2 || ticks[1] != 2 || res.Written != 2 || !res.CreatedRoot {
		t.Fatalf("total %d ticks %v res %+v", total, ticks, res)
	}
	if b, _ := os.ReadFile(filepath.Join(dest, "sub", "world.md")); string(b) != "# World\n" {
		t.Fatalf("world.md = %q", b)
	}
	if got := reg.Get(vaultID).Path; got != "~/lumi/test" {
		t.Fatalf("book path %q", got)
	}
	vs, _ := account.LoadVaults()
	if len(vs) != 1 || vs[0].ID != vaultID || vs[0].Account != "alice" {
		t.Fatalf("vaults.yaml %+v", vs)
	}
}

func TestRunErrorsCarryStage(t *testing.T) {
	homeEnv(t)
	srv := fakeServer(t, map[string]int{"/api/vaults/" + vaultID + "/notes/n2/content": http.StatusConflict})
	dest := filepath.Join(t.TempDir(), "x")

	_, err := Run(context.Background(), Options{Client: client(t, srv.URL, "bad"), Server: srv.URL, VaultID: vaultID, Path: dest})
	if StageOf(err) != StageListVaults || !errors.Is(err, account.ErrUnauthorized) {
		t.Fatalf("auth: %v (%s)", err, StageOf(err))
	}
	res, err := Run(context.Background(), Options{Client: client(t, srv.URL, "tok"), Server: srv.URL, VaultID: "nope", Path: dest})
	if StageOf(err) != StageNotFound || res.Visible != 1 {
		t.Fatalf("not found: %v", err)
	}
	res, err = Run(context.Background(), Options{Client: client(t, srv.URL, "tok"), Server: srv.URL, VaultID: vaultID, Path: dest})
	var api *account.APIError
	if StageOf(err) != StageDownload || !errors.As(err, &api) || api.Status != http.StatusConflict {
		t.Fatalf("epoch conflict: %v", err)
	}
	// a failed clone cleans exactly what it wrote, and the folder it made
	Cleanup(res)
	if _, err := os.Stat(dest); !os.IsNotExist(err) {
		t.Fatalf("cleanup left %s", dest)
	}
}

func TestRunCancelled(t *testing.T) {
	homeEnv(t)
	srv := fakeServer(t, nil)
	ctx, cancel := context.WithCancel(context.Background())
	dest := filepath.Join(t.TempDir(), "x")
	_, err := Run(ctx, Options{Client: client(t, srv.URL, "tok"), Server: srv.URL, VaultID: vaultID, Path: dest,
		OnNote: func(int, int, string, bool) { cancel() }})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("want cancel, got %v", err)
	}
	if vs, _ := account.LoadVaults(); len(vs) != 0 {
		t.Fatal("a cancelled clone must not register the vault")
	}
}

func TestCleanupKeepsForeignFiles(t *testing.T) {
	root := t.TempDir()
	mine := filepath.Join(root, "a", "mine.md")
	theirs := filepath.Join(root, "a", "theirs.md")
	os.MkdirAll(filepath.Dir(mine), 0o755)
	os.WriteFile(mine, []byte("x"), 0o644)
	os.WriteFile(theirs, []byte("y"), 0o644)
	Cleanup(Result{Path: root, WrittenPaths: []string{mine, "/etc/passwd"}, CreatedRoot: true})
	if _, err := os.Stat(theirs); err != nil {
		t.Fatal("cleanup removed a file it did not write")
	}
	if _, err := os.Stat(mine); !os.IsNotExist(err) {
		t.Fatal("cleanup kept a file it wrote")
	}
}

func TestValidateCloneDest(t *testing.T) {
	home := homeEnv(t)
	book := filepath.Join(home, "notes")
	os.MkdirAll(book, 0o755)
	full := filepath.Join(home, "full")
	os.MkdirAll(full, 0o755)
	os.WriteFile(filepath.Join(full, "x.md"), nil, 0o644)
	os.WriteFile(filepath.Join(home, "file"), nil, 0o644)
	os.MkdirAll(filepath.Join(home, "empty"), 0o755)
	list := []books.Book{{ID: "b", Name: "Notas", Kind: books.KindLocal, Path: "~/notes"}}

	cases := map[string]error{
		"":                     ErrDestEmpty,
		"   ":                  ErrDestEmpty,
		"relative/dir":         ErrDestRelative,
		"~/lumi/../etc":        ErrDestTraversal,
		"~/full":               ErrDestNotEmpty,
		"~/file":               ErrDestNotDir,
		"~/lumi/new":           nil,
		"~/empty":              nil,
		home + "/lumi/abs-new": nil,
	}
	for in, want := range cases {
		_, err := ValidateCloneDest(in, list)
		if !errors.Is(err, want) && !(want == nil && err == nil) {
			t.Errorf("%q: got %v want %v", in, err, want)
		}
	}
	for _, in := range []string{"~/notes", "~/notes/sub/x", book + "/deep"} {
		var ib *InsideBookError
		if _, err := ValidateCloneDest(in, list); !errors.As(err, &ib) || ib.Book != "Notas" {
			t.Errorf("%q inside book: %v", in, err)
		}
	}
	abs, err := ValidateCloneDest("~/lumi/new", list)
	if err != nil || abs != filepath.Join(home, "lumi", "new") {
		t.Fatalf("expand: %q %v", abs, err)
	}
}

func TestValidateLinkDest(t *testing.T) {
	home := homeEnv(t)
	os.MkdirAll(filepath.Join(home, "notes", "inner"), 0o755)
	os.MkdirAll(filepath.Join(home, "mine"), 0o755)
	list := []books.Book{{ID: "b", Name: "Notas", Kind: books.KindLocal, Path: "~/notes/inner"}}
	if _, err := ValidateLinkDest("~/missing", list, "x"); !errors.Is(err, ErrDestMissing) {
		t.Errorf("missing: %v", err)
	}
	if _, err := ValidateLinkDest("~/notes", list, "x"); err == nil || !strings.Contains(err.Error(), "contém") {
		t.Errorf("contains book: %v", err)
	}
	if _, err := ValidateLinkDest("~/mine", list, "x"); err != nil {
		t.Errorf("ok dir: %v", err)
	}
}

func TestSlugifyAndDefaultDest(t *testing.T) {
	home := homeEnv(t)
	for in, want := range map[string]string{"Meu Livro!": "meu-livro", "Ação & Reação": "acao-reacao", "///": "livro", "test": "test"} {
		if got := Slugify(in); got != want {
			t.Errorf("Slugify(%q) = %q want %q", in, got, want)
		}
	}
	if got := DefaultDest("", "Test", nil); got != "~/lumi/test" {
		t.Fatalf("default %q", got)
	}
	os.MkdirAll(filepath.Join(home, "lumi", "test"), 0o755)
	os.WriteFile(filepath.Join(home, "lumi", "test", "x"), nil, 0o644)
	if got := DefaultDest("~/books/", "Test", nil); got != "~/books/test" {
		t.Fatalf("custom dir %q", got)
	}
	if got := DefaultDest("", "Test", nil); got != "~/lumi/test-2" {
		t.Fatalf("taken → %q", got)
	}
}

func TestLinkFolder(t *testing.T) {
	home := homeEnv(t)
	dir := filepath.Join(home, "here")
	os.MkdirAll(dir, 0o755)
	reg := &books.Registry{Version: books.FormatVersion}
	reg.Add(books.Book{ID: vaultID, Name: "Test", Kind: books.KindBound, ServerURL: "https://s.example", Account: "alice", VaultID: vaultID})
	up := func(fn func(*books.Registry) bool) error { fn(reg); return nil }
	if err := LinkFolder(up, *reg.Get(vaultID), dir); err != nil {
		t.Fatal(err)
	}
	if reg.Get(vaultID).Path != "~/here" {
		t.Fatalf("path %q", reg.Get(vaultID).Path)
	}
	if err := LinkFolder(up, books.Book{Name: "L", Kind: books.KindLocal}, dir); err == nil {
		t.Fatal("a local book cannot be linked to a server")
	}
}
