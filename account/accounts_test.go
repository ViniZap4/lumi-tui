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

	gotAlice := loaded.Find("https://lumi.work.example")
	if gotAlice == nil {
		t.Fatalf("alice account missing after roundtrip")
	}
	if gotAlice.Token != "tok-1" || gotAlice.Username != "alice" || gotAlice.DisplayName != "Alice" {
		t.Errorf("alice fields wrong: %+v", gotAlice)
	}
	if !gotAlice.ExpiresAt.Equal(now.Add(24 * time.Hour)) {
		t.Errorf("alice expires_at wrong: got %v, want %v", gotAlice.ExpiresAt, now.Add(24*time.Hour))
	}

	gotBob := loaded.Find("https://lumi.home.example")
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

func TestUpsertReplacesByServer(t *testing.T) {
	f := &File{}
	old := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	new := time.Date(2026, 5, 22, 0, 0, 0, 0, time.UTC)

	replaced := f.Upsert(Account{Server: "https://s", Username: "a", Token: "t1", AddedAt: old})
	if replaced {
		t.Errorf("first upsert should not be a replacement")
	}
	replaced = f.Upsert(Account{Server: "https://s", Username: "b", Token: "t2", AddedAt: new})
	if !replaced {
		t.Errorf("second upsert on same server should replace")
	}
	if len(f.Accounts) != 1 {
		t.Errorf("expected 1 row after upsert, got %d", len(f.Accounts))
	}
	if f.Accounts[0].Username != "b" || f.Accounts[0].Token != "t2" {
		t.Errorf("upsert didn't replace fields: %+v", f.Accounts[0])
	}
}

func TestUpsertSortsByAddedAtDescending(t *testing.T) {
	f := &File{}
	older := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	newer := time.Date(2026, 5, 22, 0, 0, 0, 0, time.UTC)
	f.Upsert(Account{Server: "https://old", Username: "u1", Token: "t1", AddedAt: older})
	f.Upsert(Account{Server: "https://new", Username: "u2", Token: "t2", AddedAt: newer})
	if f.Accounts[0].Server != "https://new" {
		t.Errorf("expected most-recent first, got %s", f.Accounts[0].Server)
	}
}

func TestRemove(t *testing.T) {
	f := &File{Accounts: []Account{
		{Server: "https://a"},
		{Server: "https://b"},
	}}
	if !f.Remove("https://a") {
		t.Errorf("expected remove to return true")
	}
	if len(f.Accounts) != 1 || f.Accounts[0].Server != "https://b" {
		t.Errorf("expected only b to remain, got %+v", f.Accounts)
	}
	if f.Remove("https://missing") {
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

func TestSaveHeaderIncludesWarning(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "accounts.yaml")

	f := &File{Accounts: []Account{{Server: "https://x", Username: "y", Token: "z", AddedAt: time.Now()}}}
	if err := f.SaveTo(path); err != nil {
		t.Fatalf("SaveTo: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !strings.Contains(string(data), "DO NOT share") {
		t.Errorf("header missing 'DO NOT share' warning; got:\n%s", string(data))
	}
}
