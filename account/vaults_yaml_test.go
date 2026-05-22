package account

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadVaultsFromMissingFileReturnsEmpty(t *testing.T) {
	v, err := LoadVaultsFrom(filepath.Join(t.TempDir(), "nope.yaml"))
	if err != nil {
		t.Fatalf("LoadVaultsFrom missing: %v", err)
	}
	if len(v) != 0 {
		t.Errorf("expected no entries, got %d", len(v))
	}
}

// The schema must exactly match what lumi-apple writes — this is the
// canonical cross-client contract. The fixture below is copy-pasted from
// what VaultRegistry.writeToDisk emits.
func TestParseAppleWriterFixture(t *testing.T) {
	fixture := `# lumi shared vault registry. Format defined in SPEC.md.
# Written by lumi-apple; read by both the Apple client and the TUI (Phase 5+).
vaults:
  - id: 8c5a1d9f-1234-1234-1234-aabbccddeeff
    name: Work team
    path: ~/notes/work
    server: https://lumi.work.com
    account: alice
    added_at: 2026-05-14T12:00:00Z
    last_opened_at: 2026-05-22T09:00:00Z
  - id: cafe0000-0000-0000-0000-000000000000
    name: Personal scratch
    path: ~/notes/scratch
    server: null
    account: null
    added_at: 2026-05-10T08:00:00Z
    last_opened_at: null
`
	dir := t.TempDir()
	path := filepath.Join(dir, "vaults.yaml")
	if err := os.WriteFile(path, []byte(fixture), 0644); err != nil {
		t.Fatalf("pre-write: %v", err)
	}

	entries, err := LoadVaultsFrom(path)
	if err != nil {
		t.Fatalf("LoadVaultsFrom: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}
	// Most-recently-opened first.
	if entries[0].Name != "Work team" {
		t.Errorf("expected Work team first, got %q", entries[0].Name)
	}
	if !entries[0].IsServerBound() {
		t.Errorf("Work team should be server-bound")
	}
	if entries[0].Account != "alice" {
		t.Errorf("Work team account: got %q", entries[0].Account)
	}
	if entries[0].LastOpenedAt.IsZero() {
		t.Errorf("Work team should have last_opened_at parsed")
	}
	// The path is expanded; raw is preserved for fidelity.
	if entries[0].RawPath != "~/notes/work" {
		t.Errorf("raw path should be preserved, got %q", entries[0].RawPath)
	}
	home, _ := os.UserHomeDir()
	if !strings.HasPrefix(entries[0].Path, home) {
		t.Errorf("expected expanded path to start with home dir; got %q", entries[0].Path)
	}

	// Second entry: local-only (server == null, account == null), no last_opened.
	if entries[1].IsServerBound() {
		t.Errorf("scratch vault should NOT be server-bound")
	}
	if entries[1].Server != "" || entries[1].Account != "" {
		t.Errorf("null server/account should parse as empty; got server=%q account=%q",
			entries[1].Server, entries[1].Account)
	}
	if !entries[1].LastOpenedAt.IsZero() {
		t.Errorf("scratch last_opened_at should be zero")
	}
}

func TestParseSkipsRowsMissingRequiredFields(t *testing.T) {
	fixture := `vaults:
  - id: 8c5a1d9f-1234-1234-1234-aabbccddeeff
    name: Complete
    path: ~/notes/complete
    added_at: 2026-05-14T12:00:00Z
  - id: deadbeef-0000-0000-0000-000000000000
    name: NoPathRow
    added_at: 2026-05-14T12:00:00Z
  - name: NoIDRow
    path: ~/no-id
    added_at: 2026-05-14T12:00:00Z
`
	dir := t.TempDir()
	path := filepath.Join(dir, "vaults.yaml")
	_ = os.WriteFile(path, []byte(fixture), 0644)

	entries, err := LoadVaultsFrom(path)
	if err != nil {
		t.Fatalf("LoadVaultsFrom: %v", err)
	}
	if len(entries) != 1 {
		t.Errorf("expected 1 valid entry, got %d (%+v)", len(entries), entries)
	}
	if entries[0].Name != "Complete" {
		t.Errorf("unexpected entry: %+v", entries[0])
	}
}

func TestParseHandlesQuotedValues(t *testing.T) {
	// Apple's writer quotes values containing ':', '#', '"', or
	// leading/trailing whitespace. Reader must unquote symmetrically.
	fixture := `vaults:
  - id: 8c5a1d9f-1234-1234-1234-aabbccddeeff
    name: "Has: colon"
    path: "~/notes/with #hash"
    server: "https://lumi.work.com"
    account: null
    added_at: 2026-05-14T12:00:00Z
`
	dir := t.TempDir()
	path := filepath.Join(dir, "vaults.yaml")
	_ = os.WriteFile(path, []byte(fixture), 0644)

	entries, err := LoadVaultsFrom(path)
	if err != nil {
		t.Fatalf("LoadVaultsFrom: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	if entries[0].Name != "Has: colon" {
		t.Errorf("unquoted name wrong: %q", entries[0].Name)
	}
	if entries[0].RawPath != "~/notes/with #hash" {
		t.Errorf("unquoted path wrong: %q", entries[0].RawPath)
	}
}

func TestParseIgnoresUnknownTopLevelKeys(t *testing.T) {
	// Tolerance: unknown top-level keys (`themes:`, `version:`) must not
	// confuse the parser into eating subsequent `vaults:` rows.
	fixture := `version: 2
themes:
  - tokyo-night
vaults:
  - id: 8c5a1d9f-1234-1234-1234-aabbccddeeff
    name: Work
    path: ~/notes/work
    added_at: 2026-05-14T12:00:00Z
`
	dir := t.TempDir()
	path := filepath.Join(dir, "vaults.yaml")
	_ = os.WriteFile(path, []byte(fixture), 0644)

	entries, err := LoadVaultsFrom(path)
	if err != nil {
		t.Fatalf("LoadVaultsFrom: %v", err)
	}
	if len(entries) != 1 || entries[0].Name != "Work" {
		t.Errorf("expected single Work entry, got %+v", entries)
	}
}
