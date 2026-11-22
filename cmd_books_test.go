package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vinizap/lumi/tui-client/account"
	"github.com/vinizap/lumi/tui-client/books"
)

func loadTestBooks(t *testing.T) *books.Registry {
	t.Helper()
	st, err := books.Open()
	if err != nil {
		t.Fatal(err)
	}
	return st.Registry
}

func TestBooksCLILocalLifecycle(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := filepath.Join(home, "notes", "diario")
	os.MkdirAll(dir, 0o755)

	var out, errb bytes.Buffer
	if code := runBooksCmd([]string{"link", dir, "--name", "Diário"}, &out, &errb); code != 0 {
		t.Fatalf("link: %d %s", code, errb.String())
	}
	reg := loadTestBooks(t)
	if len(reg.Books) != 1 || reg.Books[0].Path != "~/notes/diario" || reg.Books[0].Kind != books.KindLocal {
		t.Fatalf("registry %+v", reg.Books)
	}
	out.Reset()
	if code := runBooksCmd(nil, &out, &errb); code != 0 || !strings.Contains(out.String(), "Diário") {
		t.Fatalf("list: %d %q", code, out.String())
	}
	out.Reset()
	if code := runBooksCmd([]string{"rename", "1", "Journal"}, &out, &errb); code != 0 {
		t.Fatalf("rename: %s", errb.String())
	}
	if code := runBooksCmd([]string{"use", "journal"}, &out, &errb); code != 0 {
		t.Fatalf("use: %s", errb.String())
	}
	reg = loadTestBooks(t)
	if reg.Books[0].Name != "Journal" || reg.Active != reg.Books[0].ID || reg.Books[0].LastOpenedAt.IsZero() {
		t.Fatalf("after rename/use: %+v active=%s", reg.Books[0], reg.Active)
	}
	out.Reset()
	if code := runBooksCmd([]string{"unlink", dir}, &out, &errb); code != 0 {
		t.Fatalf("unlink: %s", errb.String())
	}
	if _, err := os.Stat(dir); err != nil {
		t.Fatal("unlink must never delete files")
	}
	if len(loadTestBooks(t).Books) != 0 {
		t.Fatal("book not removed")
	}
	info, _ := os.Stat(filepath.Join(home, ".config", "lumi", "books.yaml"))
	if info.Mode().Perm() != 0o600 {
		t.Errorf("books.yaml perm %o", info.Mode().Perm())
	}
}

func TestVaultsIsAliasAndMigrates(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	cfg := filepath.Join(home, ".config", "lumi")
	os.MkdirAll(cfg, 0o700)
	os.WriteFile(filepath.Join(cfg, "vaults.yaml"), []byte(
		"vaults:\n  - id: 11111111-1111-4111-8111-111111111111\n    name: Old\n    path: /srv/old\n    added_at: 2026-01-01T00:00:00Z\n"), 0o600)
	var out, errb bytes.Buffer
	if code := runBooksCmd([]string{"list"}, &out, &errb); code != 0 {
		t.Fatalf("%d %s", code, errb.String())
	}
	if !strings.Contains(out.String(), "Imported 1 book") || !strings.Contains(out.String(), "Old") {
		t.Fatalf("out %q", out.String())
	}
}

func TestBooksLinkBindsAndRepairsIncompleteBinding(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Lumi-Token") != "tok" {
			w.WriteHeader(401)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"vaults": []map[string]any{
			{"id": "8C5A1D9F-2B3C-4D5E-8F60-718293A4B5C6", "slug": "work", "name": "Trabalho"},
		}})
	}))
	defer srv.Close()
	f := &account.File{}
	f.Upsert(account.Account{Server: srv.URL, Username: "alice", Token: "tok"})
	if err := f.Save(); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(home, "work")
	os.MkdirAll(dir, 0o755)

	// A legacy `vault link --server` leaves an incomplete binding.
	var out, errb bytes.Buffer
	if code := runVaultLinkCmd([]string{dir, "--server", srv.URL, "--account", "alice"}, &out, &errb); code != 0 {
		t.Fatalf("vault link: %s", errb.String())
	}
	reg := loadTestBooks(t)
	if len(reg.Books) != 1 || !reg.Books[0].IncompleteBinding() {
		t.Fatalf("want incomplete binding: %+v", reg.Books)
	}
	if code := runBooksCmd([]string{"link", dir, "--server", srv.URL}, &out, &errb); code != 0 {
		t.Fatalf("books link: %s", errb.String())
	}
	reg = loadTestBooks(t)
	b := reg.Books[0]
	if b.VaultID != "8c5a1d9f-2b3c-4d5e-8f60-718293a4b5c6" || b.Account != "alice" || b.Name != "Trabalho" || len(reg.Books) != 1 {
		t.Fatalf("not repaired: %+v", reg.Books)
	}
	// unlink with a folder -> local, binding dropped, row kept.
	if code := runBooksCmd([]string{"unlink", "Trabalho"}, &out, &errb); code != 0 {
		t.Fatal(errb.String())
	}
	reg = loadTestBooks(t)
	if len(reg.Books) != 1 || reg.Books[0].IsBound() {
		t.Fatalf("unlink of bound+folder should make it local: %+v", reg.Books)
	}
}
