package account

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestSaveVaultsRoundtrip writes a registry, reads it back, and asserts
// that the row content survives the trip. Covers the cross-client
// contract with lumi-apple: anything the writer emits must be loadable
// by LoadVaultsFrom unchanged (modulo the `last_opened_at` sort).
func TestSaveVaultsRoundtrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "vaults.yaml")

	now := time.Date(2026, 5, 25, 12, 0, 0, 0, time.UTC)
	in := []VaultEntry{
		{
			ID:           "8c5a1d9f-1111-2222-3333-444444444444",
			Name:         "Work team",
			Path:         "/home/u/notes/work",
			RawPath:      "/home/u/notes/work",
			Server:       "https://lumi.work.com",
			Account:      "alice",
			AddedAt:      now,
			LastOpenedAt: now.Add(5 * time.Minute),
		},
		{
			ID:      "11111111-2222-3333-4444-555555555555",
			Name:    "Personal",
			Path:    "/home/u/notes/personal",
			RawPath: "/home/u/notes/personal",
			AddedAt: now.Add(-time.Hour),
		},
	}
	if err := SaveVaultsAt(path, in); err != nil {
		t.Fatalf("SaveVaultsAt: %v", err)
	}
	// File must be 0600 — vault paths + accounts are mildly sensitive.
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %o, want 0600", info.Mode().Perm())
	}

	out, err := LoadVaultsFrom(path)
	if err != nil {
		t.Fatalf("LoadVaultsFrom: %v", err)
	}
	if len(out) != 2 {
		t.Fatalf("got %d entries, want 2", len(out))
	}
	// Sort is newest-first by last_opened_at then added_at.
	if out[0].Name != "Work team" {
		t.Fatalf("first entry name = %q, want Work team", out[0].Name)
	}
	if out[0].Server != "https://lumi.work.com" || out[0].Account != "alice" {
		t.Errorf("server-bound fields lost: server=%q account=%q", out[0].Server, out[0].Account)
	}
	if !out[0].LastOpenedAt.Equal(now.Add(5 * time.Minute)) {
		t.Errorf("last_opened_at lost: got %v want %v", out[0].LastOpenedAt, now.Add(5*time.Minute))
	}
	if !out[1].LastOpenedAt.IsZero() {
		t.Errorf("local-only vault last_opened should be zero, got %v", out[1].LastOpenedAt)
	}
}

// TestSaveVaultsMatchesAppleByteShape asserts that the emitted file is
// byte-for-byte aligned with lumi-apple's VaultRegistry.writeToDisk —
// the comment lines, key order, null literals. A drift here is a
// cross-client contract break.
func TestSaveVaultsMatchesAppleByteShape(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "vaults.yaml")

	added := time.Date(2026, 5, 25, 10, 0, 0, 0, time.UTC)
	last := time.Date(2026, 5, 25, 10, 5, 0, 0, time.UTC)
	entries := []VaultEntry{{
		ID:           "8c5a1d9f-1111-2222-3333-444444444444",
		Name:         "Work team",
		RawPath:      "~/notes/work",
		Server:       "https://lumi.work.com",
		Account:      "alice",
		AddedAt:      added,
		LastOpenedAt: last,
	}}
	if err := SaveVaultsAt(path, entries); err != nil {
		t.Fatalf("SaveVaultsAt: %v", err)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	got := string(body)
	want := `# lumi shared vault registry. Format defined in SPEC.md.
# Written by lumi-apple and lumi-tui (Phase 5+).
vaults:
  - id: 8c5a1d9f-1111-2222-3333-444444444444
    name: Work team
    path: ~/notes/work
    server: "https://lumi.work.com"
    account: alice
    added_at: 2026-05-25T10:00:00Z
    last_opened_at: 2026-05-25T10:05:00Z
`
	// Quoting rules mirror VaultRegistry.swift's `yamlString`:
	//   - `Work team` contains no `:`/`#`/`"`/leading-or-trailing space → bare
	//   - `~/notes/work` likewise → bare
	//   - `https://lumi.work.com` contains `:` → quoted
	//   - `alice` → bare
	// If this diff fails because of quoting changes, also update
	// VaultRegistry.swift's yamlString so the two stay aligned.
	if got != want {
		t.Errorf("byte shape mismatch.\nGOT:\n%s\nWANT:\n%s", got, want)
	}
}

// TestUpsertVaultReplaceByID — upserting twice with the same id keeps
// the file at one row, with the second row's data winning.
func TestUpsertVaultReplaceByID(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "vaults.yaml")
	id := "abcdef00-0000-0000-0000-000000000000"
	first := VaultEntry{
		ID:      id,
		Name:    "Old name",
		Path:    "/tmp/work",
		RawPath: "/tmp/work",
		AddedAt: time.Now().UTC().Truncate(time.Second),
	}
	if _, err := UpsertVaultAt(path, first); err != nil {
		t.Fatalf("first upsert: %v", err)
	}
	second := first
	second.Name = "New name"
	second.Server = "https://x.example"
	if _, err := UpsertVaultAt(path, second); err != nil {
		t.Fatalf("second upsert: %v", err)
	}
	entries, err := LoadVaultsFrom(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("got %d entries after upsert-replace, want 1", len(entries))
	}
	if entries[0].Name != "New name" || entries[0].Server != "https://x.example" {
		t.Errorf("second upsert didn't take: %+v", entries[0])
	}
}

// TestUpsertVaultDistinctIDsCoexist — two different ids both land in
// the registry.
func TestUpsertVaultDistinctIDsCoexist(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "vaults.yaml")
	a := VaultEntry{
		ID:      "aaaa0000-0000-0000-0000-000000000000",
		Name:    "A", Path: "/tmp/a", RawPath: "/tmp/a",
		AddedAt: time.Now().UTC().Truncate(time.Second),
	}
	b := VaultEntry{
		ID:      "bbbb0000-0000-0000-0000-000000000000",
		Name:    "B", Path: "/tmp/b", RawPath: "/tmp/b",
		AddedAt: time.Now().UTC().Add(time.Second).Truncate(time.Second),
	}
	if _, err := UpsertVaultAt(path, a); err != nil {
		t.Fatalf("upsert a: %v", err)
	}
	if _, err := UpsertVaultAt(path, b); err != nil {
		t.Fatalf("upsert b: %v", err)
	}
	entries, err := LoadVaultsFrom(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("got %d entries, want 2", len(entries))
	}
}

// TestEncodeHomeRelative covers the three branches: equal-to-home,
// inside-home, outside-home.
func TestEncodeHomeRelative(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home dir")
	}
	cases := []struct {
		in   string
		want string
	}{
		{home, "~"},
		{filepath.Join(home, "notes"), "~/notes"},
		{filepath.Join(home, "a", "b", "c"), "~/a/b/c"},
		{"/etc/lumi", "/etc/lumi"},
	}
	for _, tc := range cases {
		got := EncodeHomeRelative(tc.in)
		if got != tc.want {
			t.Errorf("EncodeHomeRelative(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestSaveVaultsQuotingTriggers ensures the writer quotes values that
// would confuse the line reader (colon, hash, leading space, embedded
// quote). Aligns with VaultRegistry.swift's yamlString helper.
func TestSaveVaultsQuotingTriggers(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "vaults.yaml")
	entries := []VaultEntry{{
		ID:      "11111111-1111-1111-1111-111111111111",
		Name:    "Has: colon",      // must be quoted
		RawPath: "/tmp/has hash#thing", // must be quoted
		Server:  `quote"d`,          // must be quoted + escaped
		AddedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	}}
	if err := SaveVaultsAt(path, entries); err != nil {
		t.Fatalf("save: %v", err)
	}
	body, _ := os.ReadFile(path)
	s := string(body)
	for _, want := range []string{
		`name: "Has: colon"`,
		`path: "/tmp/has hash#thing"`,
		`server: "quote\"d"`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("emitted file missing %q:\n%s", want, s)
		}
	}
}
