package books

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vinizap/lumi/tui-client/account"
)

func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	return data
}

// TestGoldenRoundTrip is the cross-client contract: parsing the shared
// fixture and writing it back must reproduce it byte for byte.
func TestGoldenRoundTrip(t *testing.T) {
	want := readFixture(t, "books.v1.yaml")
	r, err := Parse(want)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if r.ReadOnly {
		t.Fatalf("fixture parsed read-only: %v", r.Problems)
	}
	got := r.Marshal()
	if !bytes.Equal(got, want) {
		t.Fatalf("round trip differs.\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

func TestGoldenFieldValues(t *testing.T) {
	r, err := Parse(readFixture(t, "books.v1.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Books) != 3 {
		t.Fatalf("want 3 books, got %d", len(r.Books))
	}
	work := r.Get("0192f7a1-5b1e-7c3d-9e2f-4a6b8c0d1e2f")
	if work == nil || work.Kind != KindBound || work.ServerURL != "https://lumi.work.com" ||
		work.Account != "alice" || work.VaultID != "8c5a1d9f-2b3c-4d5e-8f60-718293a4b5c6" ||
		work.Path != "~/notes/work" || work.Sort != 10 ||
		!work.AddedAt.Equal(time.Date(2026, 11, 22, 10, 0, 0, 0, time.UTC)) {
		t.Fatalf("work book wrong: %+v", work)
	}
	if got := r.ActiveBook(); got == nil || got.ID != work.ID {
		t.Errorf("active = %+v, want work", got)
	}
	recipes := r.Get("3f2e1d0c-9b8a-4766-8554-433221100fed")
	if recipes == nil || recipes.Name != `Receitas "da vó"` || recipes.Path != "" || !recipes.LastOpenedAt.IsZero() {
		t.Fatalf("recipes book wrong: %+v", recipes)
	}
	// sort tie (10, 10) breaks on id.
	if r.Books[0].Name != "Diário" || r.Books[1].Name != "Trabalho" {
		t.Errorf("order wrong: %s, %s", r.Books[0].Name, r.Books[1].Name)
	}
}

func TestMarshalEmpty(t *testing.T) {
	got := string((&Registry{}).Marshal())
	want := Header + "\nversion: 1\nactive: null\nbooks: []\n"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
	r, err := Parse([]byte(got))
	if err != nil || len(r.Books) != 0 || r.ReadOnly {
		t.Fatalf("reparse empty: %+v %v", r, err)
	}
}

func TestMarshalEscapes(t *testing.T) {
	r := &Registry{}
	_, err := r.Add(Book{ID: "A", Name: "back\\slash \"q\" tab\tnl\n", Kind: KindLocal, Path: "/x: #y",
		AddedAt: time.Date(2026, 1, 2, 3, 4, 5, 999, time.FixedZone("BRT", -3*3600))})
	if err != nil {
		t.Fatal(err)
	}
	out := r.Marshal()
	if !strings.Contains(string(out), `name: "back\\slash \"q\" tab\tnl\n"`) {
		t.Errorf("escaping wrong:\n%s", out)
	}
	if !strings.Contains(string(out), "added_at: 2026-01-02T06:04:05Z") {
		t.Errorf("timestamp not UTC seconds Z:\n%s", out)
	}
	back, err := Parse(out)
	if err != nil {
		t.Fatal(err)
	}
	if back.Books[0].Name != r.Books[0].Name || back.Books[0].Path != "/x: #y" {
		t.Errorf("escape round trip lost data: %+v", back.Books[0])
	}
	if !bytes.Equal(back.Marshal(), out) {
		t.Errorf("not idempotent")
	}
}

func TestNewerVersionIsReadOnly(t *testing.T) {
	data := []byte("version: 2\nactive: null\nbooks:\n  - id: x\n    name: \"n\"\n    kind: local\n    sealed: true\n")
	r, err := Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	if !r.ReadOnly {
		t.Fatal("v2 file must be read-only for a v1 writer")
	}
	path := filepath.Join(t.TempDir(), "books.yaml")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := r.Save(path); err != ErrReadOnly {
		t.Fatalf("Save = %v, want ErrReadOnly", err)
	}
	after, _ := os.ReadFile(path)
	if !bytes.Equal(after, data) {
		t.Fatal("newer file was clobbered")
	}
}

func TestUnknownKindIsReadOnly(t *testing.T) {
	r, err := Parse([]byte("version: 1\nbooks:\n  - id: x\n    kind: sealed\n"))
	if err != nil {
		t.Fatal(err)
	}
	if !r.ReadOnly {
		t.Fatal("unknown kind must not be silently dropped on save")
	}
}

func TestSavePermsAndAtomic(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "lumi")
	path := filepath.Join(dir, "books.yaml")
	r := &Registry{}
	if _, err := r.Add(Book{ID: "b1", Name: "One", Kind: KindLocal}); err != nil {
		t.Fatal(err)
	}
	if err := r.Save(path); err != nil {
		t.Fatal(err)
	}
	fi, _ := os.Stat(path)
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("file perm %o", fi.Mode().Perm())
	}
	di, _ := os.Stat(dir)
	if di.Mode().Perm() != 0o700 {
		t.Errorf("dir perm %o", di.Mode().Perm())
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Errorf("temp file left behind: %v", entries)
	}
}

func TestAddEnforcesOneBoundBookPerVault(t *testing.T) {
	r := &Registry{}
	if _, err := r.Add(Book{ID: "a", Kind: KindBound, VaultID: "V", ServerURL: "https://a", Account: "u"}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Add(Book{ID: "b", Kind: KindBound, VaultID: "v", ServerURL: "https://b", Account: "u"}); err == nil {
		t.Fatal("second bound book for the same vault must be rejected")
	}
	if r.Books[0].Sort != 10 {
		t.Errorf("first sort = %d", r.Books[0].Sort)
	}
}

func TestActiveFallsBackToLowestSort(t *testing.T) {
	r := &Registry{Active: "gone"}
	r.Add(Book{ID: "b", Kind: KindLocal, Sort: 20})
	r.Add(Book{ID: "a", Kind: KindLocal, Sort: 30})
	r.Add(Book{ID: "c", Kind: KindLocal, Sort: 10})
	if got := r.ActiveBook(); got.ID != "c" {
		t.Errorf("active fallback = %s, want c", got.ID)
	}
	r.Remove("c")
	if got := r.ActiveBook(); got.ID != "b" {
		t.Errorf("after remove = %s, want b", got.ID)
	}
}

func TestMoveRenumbers(t *testing.T) {
	r := &Registry{}
	r.Add(Book{ID: "a", Kind: KindLocal})
	r.Add(Book{ID: "b", Kind: KindLocal})
	r.Add(Book{ID: "c", Kind: KindLocal})
	if !r.Move("c", -1) {
		t.Fatal("move failed")
	}
	ids := []string{r.Books[0].ID, r.Books[1].ID, r.Books[2].ID}
	if strings.Join(ids, "") != "acb" || r.Books[2].Sort != 30 {
		t.Errorf("order %v sorts %d", ids, r.Books[2].Sort)
	}
	if r.Move("a", -1) {
		t.Error("moving first up should fail")
	}
}

func TestFind(t *testing.T) {
	r := &Registry{}
	r.Add(Book{ID: "aaaa", Name: "Work", Kind: KindLocal})
	r.Add(Book{ID: "bbbb", Name: "Home", Kind: KindLocal})
	if b := r.Find("2"); b == nil || b.ID != "bbbb" {
		t.Errorf("by position: %+v", b)
	}
	if b := r.Find("work"); b == nil || b.ID != "aaaa" {
		t.Errorf("by name: %+v", b)
	}
	if b := r.Find("AAAA"); b == nil || b.ID != "aaaa" {
		t.Errorf("by id: %+v", b)
	}
	if r.Find("nope") != nil {
		t.Error("unexpected match")
	}
}

func TestImportVaultsFromFixture(t *testing.T) {
	entries, err := account.LoadVaultsFrom(filepath.Join("testdata", "vaults.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	ids := map[string]string{
		filepath.Join(ExpandHome("~/notes/work")): "8C5A1D9F-2B3C-4D5E-8F60-718293A4B5C6",
		"/srv/notes/old": "8c5a1d9f-2b3c-4d5e-8f60-718293a4b5c6", // same vault → demoted to local
	}
	r := ImportVaults(entries, func(p string) string { return ids[p] })
	if len(r.Books) != 3 {
		t.Fatalf("want 3 books (no loss), got %d", len(r.Books))
	}
	work := r.Get("11111111-1111-4111-8111-111111111111")
	if work.Kind != KindBound || work.ServerURL != "https://lumi.work.com" || work.Account != "alice" ||
		work.VaultID != "8c5a1d9f-2b3c-4d5e-8f60-718293a4b5c6" || work.Path != "~/notes/work" || work.Sort != 10 {
		t.Errorf("work: %+v", work)
	}
	diary := r.Get("22222222-2222-4222-8222-222222222222")
	if diary.Kind != KindLocal || diary.ServerURL != "" || diary.Path != "/srv/notes/diario" || diary.Sort != 20 {
		t.Errorf("diary: %+v", diary)
	}
	old := r.Get("33333333-3333-4333-8333-333333333333")
	if old.Kind != KindLocal || old.Path != "/srv/notes/old" {
		t.Errorf("duplicate vault_id should import as local with folder kept: %+v", old)
	}
}

func TestImportIncompleteBinding(t *testing.T) {
	entries := []account.VaultEntry{{ID: "x", Name: "X", Path: "/p", RawPath: "/p", Server: "https://s", Account: "u"}}
	r := ImportVaults(entries, func(string) string { return "" })
	if !r.Books[0].IncompleteBinding() {
		t.Fatalf("missing vault.yaml must give an incomplete binding: %+v", r.Books[0])
	}
	if !strings.Contains(string(r.Marshal()), "vault_id: null") {
		t.Error("vault_id should be null")
	}
}

func TestLoadOrMigrateLeavesVaultsYAMLAndIsOneShot(t *testing.T) {
	dir := t.TempDir()
	vp := filepath.Join(dir, "vaults.yaml")
	bp := filepath.Join(dir, "books.yaml")
	orig := readFixture(t, "vaults.yaml")
	os.WriteFile(vp, orig, 0o600)

	st, err := LoadOrMigrate(bp, vp)
	if err != nil {
		t.Fatal(err)
	}
	if !st.Migrated || len(st.Registry.Books) != 3 {
		t.Fatalf("migration: %+v", st)
	}
	after, _ := os.ReadFile(vp)
	if !bytes.Equal(after, orig) {
		t.Fatal("vaults.yaml must not be modified")
	}
	// Edit vaults.yaml afterwards (an old client adds a row): books.yaml
	// already exists, so no re-import.
	os.WriteFile(vp, append(orig, []byte("  - id: 44444444-4444-4444-8444-444444444444\n    name: New\n    path: /n\n")...), 0o600)
	st2, err := LoadOrMigrate(bp, vp)
	if err != nil {
		t.Fatal(err)
	}
	if st2.Migrated || len(st2.Registry.Books) != 3 {
		t.Fatalf("second load must not re-import: %+v", st2)
	}
}

func TestReadFolderVaultID(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, ".lumi"), 0o700)
	os.WriteFile(filepath.Join(dir, ".lumi", "vault.yaml"), []byte(
		"id: 0b0b\nname: W\nslug: w\nserver:\n  url: https://s\n  vault_id: 8c5a1d9f-2b3c-4d5e-8f60-718293a4b5c6\n  last_synced_at: 2026-01-01T00:00:00Z\n"), 0o600)
	if got := ReadFolderVaultID(dir); got != "8c5a1d9f-2b3c-4d5e-8f60-718293a4b5c6" {
		t.Errorf("got %q", got)
	}
	if ReadFolderVaultID(t.TempDir()) != "" {
		t.Error("missing file should give empty")
	}
}
