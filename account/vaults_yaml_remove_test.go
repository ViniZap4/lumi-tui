package account

import (
	"path/filepath"
	"testing"
	"time"
)

func seedVaults(t *testing.T, path string) {
	t.Helper()
	now := time.Date(2026, 5, 25, 12, 0, 0, 0, time.UTC)
	entries := []VaultEntry{
		{
			ID: "aaaaaaaa-0000-4000-8000-000000000000",
			Name: "Work", Path: "/home/u/work", RawPath: "/home/u/work",
			AddedAt: now,
		},
		{
			ID: "bbbbbbbb-0000-4000-8000-000000000000",
			Name: "Personal", Path: "/home/u/personal", RawPath: "/home/u/personal",
			AddedAt: now.Add(-time.Hour),
		},
	}
	if err := SaveVaultsAt(path, entries); err != nil {
		t.Fatalf("seed: %v", err)
	}
}

func TestRemoveVaultByIDAtSuccess(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "vaults.yaml")
	seedVaults(t, path)

	removed, remaining, err := RemoveVaultByIDAt(path, "AAAAAAAA-0000-4000-8000-000000000000")
	if err != nil {
		t.Fatalf("remove: %v", err)
	}
	if !removed {
		t.Fatalf("expected removed=true")
	}
	if len(remaining) != 1 {
		t.Fatalf("got %d remaining, want 1", len(remaining))
	}
	if remaining[0].Name != "Personal" {
		t.Errorf("kept the wrong row: %+v", remaining[0])
	}
}

func TestRemoveVaultByIDAtNotFound(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "vaults.yaml")
	seedVaults(t, path)

	removed, remaining, err := RemoveVaultByIDAt(path, "deadbeef-0000-4000-8000-000000000000")
	if err != nil {
		t.Fatalf("remove: %v", err)
	}
	if removed {
		t.Fatalf("expected removed=false")
	}
	if len(remaining) != 2 {
		t.Fatalf("expected 2 entries unchanged, got %d", len(remaining))
	}
}

func TestRemoveVaultByPathAtSuccess(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "vaults.yaml")
	seedVaults(t, path)

	removed, remaining, err := RemoveVaultByPathAt(path, "/home/u/work/") // trailing slash normalized
	if err != nil {
		t.Fatalf("remove: %v", err)
	}
	if !removed {
		t.Fatalf("expected removed=true")
	}
	if len(remaining) != 1 || remaining[0].Name != "Personal" {
		t.Fatalf("got %+v, want only Personal", remaining)
	}
}

func TestRemoveVaultByPathAtNotFound(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "vaults.yaml")
	seedVaults(t, path)

	removed, remaining, err := RemoveVaultByPathAt(path, "/nowhere")
	if err != nil {
		t.Fatalf("remove: %v", err)
	}
	if removed {
		t.Fatalf("expected removed=false")
	}
	if len(remaining) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(remaining))
	}
}

func TestBumpLastOpenedAtByID(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "vaults.yaml")
	seedVaults(t, path)

	bumpedAt := time.Date(2026, 5, 25, 20, 0, 0, 0, time.UTC)
	bumped, err := BumpLastOpenedAtAt(path, "aaaaaaaa-0000-4000-8000-000000000000", bumpedAt)
	if err != nil {
		t.Fatalf("bump: %v", err)
	}
	if !bumped {
		t.Fatalf("expected bumped=true")
	}
	entries, err := LoadVaultsFrom(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("entry count changed: %d", len(entries))
	}
	// After bump the Work entry's last_opened_at should be in the future
	// relative to Personal's added_at, so it sorts first.
	if entries[0].Name != "Work" {
		t.Fatalf("Work should sort first after bump; got %q first", entries[0].Name)
	}
	if !entries[0].LastOpenedAt.Equal(bumpedAt) {
		t.Errorf("last_opened_at = %v, want %v", entries[0].LastOpenedAt, bumpedAt)
	}
}

func TestBumpLastOpenedAtByPath(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "vaults.yaml")
	seedVaults(t, path)

	bumpedAt := time.Date(2026, 5, 25, 20, 0, 0, 0, time.UTC)
	bumped, err := BumpLastOpenedAtAt(path, "/home/u/personal/", bumpedAt)
	if err != nil {
		t.Fatalf("bump: %v", err)
	}
	if !bumped {
		t.Fatalf("expected bumped=true")
	}
	entries, _ := LoadVaultsFrom(path)
	for _, e := range entries {
		if e.Name == "Personal" && !e.LastOpenedAt.Equal(bumpedAt) {
			t.Errorf("Personal last_opened_at = %v, want %v", e.LastOpenedAt, bumpedAt)
		}
	}
}

func TestBumpLastOpenedAtNoMatchIsNoOp(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "vaults.yaml")
	seedVaults(t, path)

	bumped, err := BumpLastOpenedAtAt(path, "/no/match", time.Now())
	if err != nil {
		t.Fatalf("bump: %v", err)
	}
	if bumped {
		t.Fatalf("expected bumped=false for non-matching path")
	}
}

func TestBumpLastOpenedAtEmptyRegistry(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "vaults.yaml")
	// no seed
	bumped, err := BumpLastOpenedAtAt(path, "/anything", time.Now())
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if bumped {
		t.Fatalf("expected bumped=false for empty registry")
	}
}

func TestIsLikelyUUID(t *testing.T) {
	cases := map[string]bool{
		"":                                          false,
		"foo":                                       false,
		"aaaaaaaa-0000-4000-8000-000000000000":     true,
		"AAAAAAAA-0000-4000-8000-000000000000":     false, // lowercase only
		"aaaaaaaa-0000-4000-8000-00000000000":      false, // too short
		"aaaaaaaa-0000-4000-8000-0000000000000":    false, // too long
		"aaaaaaaa00000400080000000000000000000":    false, // missing dashes
		"gggggggg-0000-4000-8000-000000000000":     false, // non-hex
	}
	for in, want := range cases {
		if got := isLikelyUUID(in); got != want {
			t.Errorf("isLikelyUUID(%q) = %v, want %v", in, got, want)
		}
	}
}
