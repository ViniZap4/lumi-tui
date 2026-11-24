package account

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestFileRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "accounts.yaml")

	now := time.Date(2026, 5, 22, 12, 0, 0, 0, time.UTC)
	original := &File{
		Accounts: []Account{
			{
				Server:      "https://lumi.work.example",
				Username:    "alice",
				UserID:      "u-1",
				DisplayName: "Alice",
				Token:       "tok-1",
				ExpiresAt:   now.Add(24 * time.Hour),
				AddedAt:     now,
			},
			{
				Server:      "https://lumi.home.example",
				Username:    "bob",
				UserID:      "u-2",
				DisplayName: "Bob",
				Token:       "tok-2",
				AddedAt:     now.Add(-time.Hour),
			},
		},
	}
	if err := original.SaveTo(path); err != nil {
		t.Fatalf("SaveTo: %v", err)
	}

	loaded, err := LoadFrom(path)
	if err != nil {
		t.Fatalf("LoadFrom: %v", err)
	}
	if len(loaded.Accounts) != 2 {
		t.Fatalf("expected 2 accounts, got %d", len(loaded.Accounts))
	}

	gotAlice := loaded.Find("https://lumi.work.example", "alice")
	if gotAlice == nil {
		t.Fatalf("alice account missing after roundtrip")
	}
	if gotAlice.Token != "tok-1" || gotAlice.Username != "alice" || gotAlice.DisplayName != "Alice" {
		t.Errorf("alice fields wrong: %+v", gotAlice)
	}
	if !gotAlice.ExpiresAt.Equal(now.Add(24 * time.Hour)) {
		t.Errorf("alice expires_at wrong: got %v, want %v", gotAlice.ExpiresAt, now.Add(24*time.Hour))
	}

	gotBob := loaded.Find("https://lumi.home.example", "bob")
	if gotBob == nil {
		t.Fatalf("bob account missing")
	}
	if !gotBob.ExpiresAt.IsZero() {
		t.Errorf("bob expires_at should be zero (omitted), got %v", gotBob.ExpiresAt)
	}
}

func TestSaveSetsRestrictivePermissions(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "accounts.yaml")

	f := &File{Accounts: []Account{{Server: "https://x", Username: "y", Token: "z", AddedAt: time.Now()}}}
	if err := f.SaveTo(path); err != nil {
		t.Fatalf("SaveTo: %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if info.Mode().Perm() != 0600 {
		t.Errorf("expected 0600 perms, got %v", info.Mode().Perm())
	}
}

func TestSaveForcesPermissionsOnOverwrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "accounts.yaml")

	// Pre-create the file with broader perms to simulate a legacy file.
	if err := os.WriteFile(path, []byte("accounts: []\n"), 0644); err != nil {
		t.Fatalf("pre-write: %v", err)
	}

	f := &File{Accounts: []Account{{Server: "https://x", Username: "y", Token: "z", AddedAt: time.Now()}}}
	if err := f.SaveTo(path); err != nil {
		t.Fatalf("SaveTo: %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if info.Mode().Perm() != 0600 {
		t.Errorf("expected 0600 perms after overwrite, got %v", info.Mode().Perm())
	}
}

func TestLoadFromMissingFileReturnsEmpty(t *testing.T) {
	f, err := LoadFrom(filepath.Join(t.TempDir(), "nope.yaml"))
	if err != nil {
		t.Fatalf("LoadFrom missing: %v", err)
	}
	if len(f.Accounts) != 0 {
		t.Errorf("expected empty, got %d entries", len(f.Accounts))
	}
}

func TestUpsertReplacesBySameServerAndUser(t *testing.T) {
	f := &File{}
	old := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	new := time.Date(2026, 5, 22, 0, 0, 0, 0, time.UTC)

	if f.Upsert(Account{Server: "https://s", Username: "a", Token: "t1", AddedAt: old}) {
		t.Errorf("first upsert should not be a replacement")
	}
	if !f.Upsert(Account{Server: "https://S/", Username: "A", Token: "t2", AddedAt: new}) {
		t.Errorf("re-login as the same user (normalised) should replace")
	}
	if len(f.Accounts) != 1 || f.Accounts[0].Token != "t2" {
		t.Fatalf("upsert didn't replace: %+v", f.Accounts)
	}
}

func TestUpsertKeepsTwoAccountsOnSameServer(t *testing.T) {
	f := &File{}
	f.Upsert(Account{Server: "https://s", Username: "alice", Token: "t1"})
	if f.Upsert(Account{Server: "https://s", Username: "bob", Token: "t2"}) {
		t.Fatalf("different user on same server must not replace")
	}
	if len(f.Accounts) != 2 {
		t.Fatalf("want 2 rows, got %d", len(f.Accounts))
	}
	if a := f.Find("https://s", "alice"); a == nil || a.Token != "t1" {
		t.Errorf("alice lost: %+v", a)
	}
	if a := f.Find("https://s", "bob"); a == nil || a.Token != "t2" {
		t.Errorf("bob lost: %+v", a)
	}
	if _, err := f.Resolve("https://s", ""); err != ErrAmbiguousAccount {
		t.Errorf("Resolve without username = %v, want ErrAmbiguousAccount", err)
	}
	if a, err := f.Resolve("https://s", "bob"); err != nil || a.Token != "t2" {
		t.Errorf("Resolve bob = %+v, %v", a, err)
	}
	if _, err := f.Resolve("https://other", ""); err != ErrNoAccount {
		t.Errorf("Resolve unknown server = %v, want ErrNoAccount", err)
	}
}

func TestLoadV1MigratesToV3WithoutLoss(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "lumi", "accounts.yaml")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	v1 := `# old header
accounts:
    - server: HTTPS://Lumi.Work.Example:443/
      username: alice
      user_id: u-1
      display_name: Alice
      token: tok-1
      expires_at: 2026-12-01T00:00:00Z
      added_at: 2026-05-22T12:00:00Z
    - server: http://localhost:8080
      username: bob
      token: tok-2
      added_at: 2026-05-21T12:00:00Z
`
	if err := os.WriteFile(path, []byte(v1), 0o644); err != nil {
		t.Fatal(err)
	}
	f, err := LoadFrom(path)
	if err != nil {
		t.Fatalf("LoadFrom: %v", err)
	}
	if f.Version != RegistryVersion {
		t.Errorf("v1 file should be migrated to v3 on load, got %d", f.Version)
	}
	a := f.Find("https://lumi.work.example", "alice")
	if a == nil || a.Token != "tok-1" || a.UserID != "u-1" || a.DisplayName != "Alice" || a.ExpiresAt.IsZero() {
		t.Fatalf("alice not migrated intact: %+v", a)
	}
	if a.Server != "https://lumi.work.example" {
		t.Errorf("server not normalised: %q", a.Server)
	}
	if b := f.Find("http://localhost:8080", "bob"); b == nil || b.Token != "tok-2" {
		t.Fatalf("bob lost: %+v", b)
	}
	if err := f.SaveTo(path); err != nil {
		t.Fatalf("SaveTo: %v", err)
	}
	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), "version: 3") || strings.Contains(string(data), "tok-") {
		t.Errorf("saved registry must be v3 without tokens:\n%s", data)
	}
	tok, _ := os.ReadFile(filepath.Join(filepath.Dir(path), "tokens.yaml"))
	if !strings.Contains(string(tok), "tok-1") || !strings.Contains(string(tok), "tok-2") {
		t.Errorf("tokens.yaml lost a token:\n%s", tok)
	}
	info, _ := os.Stat(path)
	if info.Mode().Perm() != 0o600 {
		t.Errorf("perm = %o, want 0600", info.Mode().Perm())
	}
	again, err := LoadFrom(path)
	if err != nil || len(again.Accounts) != 2 || again.Version != 3 || again.Find("http://localhost:8080", "bob").Token != "tok-2" {
		t.Fatalf("reload: %+v, %v", again, err)
	}
}

func TestSaveCreatesDirWith0700(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fresh", "accounts.yaml")
	f := &File{}
	f.Upsert(Account{Server: "https://x", Username: "y", Token: "z"})
	if err := f.SaveTo(path); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o700 {
		t.Errorf("dir perm = %o, want 0700", info.Mode().Perm())
	}
}

func TestNormalizeServerURL(t *testing.T) {
	cases := map[string]string{
		"https://Lumi.Work.com/":       "https://lumi.work.com",
		"HTTPS://lumi.work.com:443":    "https://lumi.work.com",
		"http://localhost:80/":         "http://localhost",
		"http://localhost:8080":        "http://localhost:8080",
		"https://x.com/lumi/?q=1#frag": "https://x.com/lumi",
		"  https://x.com/Lumi  ":       "https://x.com/Lumi",
		"https://[::1]:443/":           "https://[::1]",
	}
	for in, want := range cases {
		got, err := NormalizeServerURL(in)
		if err != nil || got != want {
			t.Errorf("NormalizeServerURL(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, bad := range []string{"", "ftp://x", "https://"} {
		if _, err := NormalizeServerURL(bad); err == nil {
			t.Errorf("NormalizeServerURL(%q) should fail", bad)
		}
	}
}

// Registry order (SPEC-LIVROS §8.3): oldest first, like Lumi.app.
func TestUpsertSortsByAddedAtAscending(t *testing.T) {
	f := &File{}
	older := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	newer := time.Date(2026, 5, 22, 0, 0, 0, 0, time.UTC)
	f.Upsert(Account{Server: "https://new", Username: "u2", Token: "t2", AddedAt: newer})
	f.Upsert(Account{Server: "https://old", Username: "u1", Token: "t1", AddedAt: older})
	if f.Accounts[0].Server != "https://old" {
		t.Errorf("expected oldest first, got %s", f.Accounts[0].Server)
	}
}

func TestRemove(t *testing.T) {
	f := &File{Accounts: []Account{
		{Server: "https://a"},
		{Server: "https://b"},
	}}
	if !f.Remove("https://a", "") {
		t.Errorf("expected remove to return true")
	}
	if len(f.Accounts) != 1 || f.Accounts[0].Server != "https://b" {
		t.Errorf("expected only b to remain, got %+v", f.Accounts)
	}
	if f.Remove("https://missing", "") {
		t.Errorf("expected remove of missing to return false")
	}
}

func TestIsExpired(t *testing.T) {
	past := Account{ExpiresAt: time.Now().Add(-time.Hour)}
	future := Account{ExpiresAt: time.Now().Add(time.Hour)}
	zero := Account{}
	if !past.IsExpired() {
		t.Errorf("past should be expired")
	}
	if future.IsExpired() {
		t.Errorf("future should not be expired")
	}
	if zero.IsExpired() {
		t.Errorf("zero expires_at should NOT be considered expired")
	}
}

// The shared registry never carries a secret; the token goes to the
// TUI-only tokens.yaml (0600) with the warning header.
func TestSaveKeepsTokensOutOfRegistry(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "accounts.yaml")

	f := &File{Accounts: []Account{{Server: "https://x", Username: "y", Token: "secret-z", AddedAt: time.Now()}}}
	if err := f.SaveTo(path); err != nil {
		t.Fatalf("SaveTo: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if strings.Contains(string(data), "secret-z") || strings.Contains(string(data), "token") {
		t.Errorf("registry leaked the token:\n%s", data)
	}
	tok, err := os.ReadFile(filepath.Join(dir, "tokens.yaml"))
	if err != nil || !strings.Contains(string(tok), "DO NOT share") || !strings.Contains(string(tok), "secret-z") {
		t.Errorf("tokens.yaml: %v\n%s", err, tok)
	}
	for _, name := range []string{"accounts.yaml", "tokens.yaml", "accounts.yaml.lock", "tokens.yaml.lock"} {
		if info, err := os.Stat(filepath.Join(dir, name)); err != nil || info.Mode().Perm() != 0600 {
			t.Errorf("%s: %v %v", name, err, info)
		}
	}
}
