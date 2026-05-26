package main

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/vinizap/lumi/tui-client/account"
)

// fixtureVaults returns a small but mixed registry for table-driven
// tests: one local, one server-bound, sorted in registry order.
func fixtureVaults() []account.VaultEntry {
	return []account.VaultEntry{
		{
			ID:           "11111111-1111-4111-8111-111111111111",
			Name:         "Personal",
			Path:         "/Users/test/notes",
			RawPath:      "~/notes",
			LastOpenedAt: time.Date(2026, 5, 26, 10, 0, 0, 0, time.UTC),
			AddedAt:      time.Date(2026, 5, 20, 10, 0, 0, 0, time.UTC),
		},
		{
			ID:      "22222222-2222-4222-8222-222222222222",
			Name:    "Work",
			Path:    "/Users/test/work",
			RawPath: "~/work",
			Server:  "https://lumi.work.com",
			Account: "alice",
			AddedAt: time.Date(2026, 5, 25, 10, 0, 0, 0, time.UTC),
		},
	}
}

func sendKey(m vaultManageModel, key string) vaultManageModel {
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(key)})
	return updated.(vaultManageModel)
}

func sendKeyEnter(m vaultManageModel) (vaultManageModel, tea.Cmd) {
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	return updated.(vaultManageModel), cmd
}

func sendKeyEsc(m vaultManageModel) (vaultManageModel, tea.Cmd) {
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	return updated.(vaultManageModel), cmd
}

func TestVaultManageModelCursorNavigation(t *testing.T) {
	m := newVaultManageModel(fixtureVaults())
	if m.cursor != 0 {
		t.Fatalf("initial cursor = %d, want 0", m.cursor)
	}
	m = sendKey(m, "j")
	if m.cursor != 1 {
		t.Errorf("after j, cursor = %d, want 1", m.cursor)
	}
	// Clamped at the bottom.
	m = sendKey(m, "j")
	if m.cursor != 1 {
		t.Errorf("after second j, cursor = %d, want clamp at 1", m.cursor)
	}
	m = sendKey(m, "k")
	if m.cursor != 0 {
		t.Errorf("after k, cursor = %d, want 0", m.cursor)
	}
	// Clamped at the top.
	m = sendKey(m, "k")
	if m.cursor != 0 {
		t.Errorf("after second k, cursor = %d, want clamp at 0", m.cursor)
	}
	m = sendKey(m, "G")
	if m.cursor != 1 {
		t.Errorf("after G, cursor = %d, want bottom", m.cursor)
	}
	m = sendKey(m, "g")
	if m.cursor != 0 {
		t.Errorf("after g, cursor = %d, want top", m.cursor)
	}
}

// Enter / o sets chosenPath so the runner can re-exec. The model
// reports tea.Quit so the program tears down before the exec.
func TestVaultManageOpenSelectsPath(t *testing.T) {
	m := newVaultManageModel(fixtureVaults())
	m, cmd := sendKeyEnter(m)
	if m.chosenPath != "/Users/test/notes" {
		t.Errorf("chosenPath = %q, want /Users/test/notes", m.chosenPath)
	}
	if cmd == nil {
		t.Errorf("expected tea.Quit cmd from Enter")
	}
}

// 'd' enters confirmation mode; 'y' triggers a registry mutation;
// 'n' / Esc cancels. Confirmation mode locks j/k so the user can't
// accidentally pick a different row mid-confirm.
func TestVaultManageDeleteConfirmation(t *testing.T) {
	m := newVaultManageModel(fixtureVaults())
	m = sendKey(m, "d")
	if m.mode != manageModeConfirmDelete {
		t.Fatalf("mode = %v, want confirmDelete", m.mode)
	}
	// j is ignored in confirm mode.
	m = sendKey(m, "j")
	if m.cursor != 0 {
		t.Errorf("cursor moved in confirm mode (was %d, want 0)", m.cursor)
	}
	// n cancels.
	m = sendKey(m, "n")
	if m.mode != manageModeList {
		t.Errorf("mode = %v, want list after n", m.mode)
	}
	if m.status == "" {
		t.Errorf("expected cancellation status")
	}
}

// Esc cancels delete confirmation; from the list view, esc quits.
func TestVaultManageEscBehaviour(t *testing.T) {
	m := newVaultManageModel(fixtureVaults())
	m = sendKey(m, "d")
	if m.mode != manageModeConfirmDelete {
		t.Fatalf("setup: mode = %v", m.mode)
	}
	m, cmd := sendKeyEsc(m)
	if m.mode != manageModeList {
		t.Errorf("after esc in confirm: mode = %v, want list", m.mode)
	}
	if cmd != nil {
		t.Errorf("esc in confirm should NOT quit (cmd != nil)")
	}

	// From list mode, esc DOES quit.
	m2 := newVaultManageModel(fixtureVaults())
	_, cmd = sendKeyEsc(m2)
	if cmd == nil {
		t.Errorf("esc in list mode should quit (cmd == nil)")
	}
}

// 's' on a local vault yields a clear error; on a server-bound vault
// it enters syncing mode + emits the sync tea.Cmd. We don't actually
// run the sync here (would hit the network); we verify the state
// transition.
func TestVaultManageSyncGatesOnServerBinding(t *testing.T) {
	m := newVaultManageModel(fixtureVaults())
	// Cursor 0 is local — sync rejects.
	updated, cmd := m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("s")})
	mm := updated.(vaultManageModel)
	if cmd != nil {
		t.Errorf("expected nil cmd for local sync, got non-nil")
	}
	if mm.mode != manageModeList {
		t.Errorf("local sync should stay in list mode, got %v", mm.mode)
	}
	if !strings.Contains(mm.status, "server-bound") {
		t.Errorf("expected explanatory status, got %q", mm.status)
	}

	// Move to the server-bound vault.
	mm = sendKey(mm, "j")
	mm.status = ""
	updated, cmd = mm.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("s")})
	mm = updated.(vaultManageModel)
	if cmd == nil {
		t.Errorf("expected non-nil cmd for server sync")
	}
	if mm.mode != manageModeSyncing {
		t.Errorf("server sync should enter syncing mode, got %v", mm.mode)
	}
}

// Syncing mode swallows keys so the user can't pile a second sync on
// top while one's in-flight.
func TestVaultManageSyncingLocksInput(t *testing.T) {
	m := newVaultManageModel(fixtureVaults())
	m.mode = manageModeSyncing
	before := m.cursor
	m, cmd := sendKeyEnter(m)
	if cmd != nil {
		t.Errorf("syncing mode should swallow Enter (cmd != nil)")
	}
	if m.cursor != before {
		t.Errorf("syncing mode should swallow nav")
	}
}

// vaultListLoadedMsg drives a successful reload — and clamps the
// cursor when entries shrink underneath it.
func TestVaultManageReloadClampsCursor(t *testing.T) {
	m := newVaultManageModel(fixtureVaults())
	m.cursor = 1
	smaller := []account.VaultEntry{fixtureVaults()[0]}
	updated, _ := m.Update(vaultListLoadedMsg{vaults: smaller})
	mm := updated.(vaultManageModel)
	if mm.cursor != 0 {
		t.Errorf("cursor = %d, want clamp to 0 after reload", mm.cursor)
	}
	if len(mm.vaults) != 1 {
		t.Errorf("vaults len = %d, want 1", len(mm.vaults))
	}
}

func TestVaultManageReloadSurfacesError(t *testing.T) {
	m := newVaultManageModel(fixtureVaults())
	updated, _ := m.Update(vaultListLoadedMsg{err: &fakeErr{"disk gone"}})
	mm := updated.(vaultManageModel)
	if !strings.Contains(mm.status, "disk gone") {
		t.Errorf("status = %q, want error mention", mm.status)
	}
	if mm.statusOK {
		t.Errorf("statusOK = true on error, want false")
	}
}

type fakeErr struct{ msg string }

func (e *fakeErr) Error() string { return e.msg }

// View renders without crashing for empty / non-empty / each mode.
// Plain rendering check — looking for canary substrings rather than
// the full layout (the layout is bound to change).
func TestVaultManageViewRenders(t *testing.T) {
	m := newVaultManageModel(fixtureVaults())
	out := m.View()
	if !strings.Contains(out, "Vault Manager") {
		t.Errorf("header missing: %q", out)
	}
	if !strings.Contains(out, "Personal") || !strings.Contains(out, "Work") {
		t.Errorf("vault names missing: %q", out)
	}
	if !strings.Contains(out, "alice@") {
		t.Errorf("server binding missing: %q", out)
	}

	empty := newVaultManageModel(nil)
	out = empty.View()
	if !strings.Contains(out, "No vaults registered") {
		t.Errorf("empty state message missing: %q", out)
	}

	confirm := newVaultManageModel(fixtureVaults())
	confirm.mode = manageModeConfirmDelete
	out = confirm.View()
	if !strings.Contains(out, "Unlink") || !strings.Contains(out, "(y/n)") {
		t.Errorf("confirm prompt missing: %q", out)
	}

	syncing := newVaultManageModel(fixtureVaults())
	syncing.mode = manageModeSyncing
	out = syncing.View()
	if !strings.Contains(out, "Syncing") {
		t.Errorf("syncing state missing: %q", out)
	}
}

func TestPathBasename(t *testing.T) {
	cases := map[string]string{
		"/Users/test/work":  "work",
		"/Users/test/work/": "work",
		"work":              "work",
		"":                  "",
		"/":                 "",
		"///":               "",
		"/a":                "a",
	}
	for in, want := range cases {
		if got := pathBasename(in); got != want {
			t.Errorf("pathBasename(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSummariseSyncOutput(t *testing.T) {
	cases := []struct {
		name        string
		code        int
		stdout      string
		stderr      string
		wantContain string
	}{
		{
			name:        "success picks last stdout line",
			code:        0,
			stdout:      "  pushed something\nPushed 3; pulled 1; skipped 0 (no id), 0 (unknown id); 0 push errors, 0 pull errors.\n",
			wantContain: "Sync OK — Pushed 3",
		},
		{
			name:        "success with empty stdout falls back to OK",
			code:        0,
			stdout:      "",
			wantContain: "Sync OK.",
		},
		{
			name:        "failure surfaces stderr last line",
			code:        2,
			stderr:      "Error: list vaults: 401 (HTTP 401)\n",
			wantContain: "Sync failed (exit 2): Error: list vaults",
		},
		{
			name:        "failure with empty stderr falls back to stdout",
			code:        1,
			stdout:      "trailing chatter",
			wantContain: "Sync failed (exit 1): trailing chatter",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := summariseSyncOutput(tc.code, tc.stdout, tc.stderr)
			if !strings.Contains(got, tc.wantContain) {
				t.Errorf("summary = %q, want substring %q", got, tc.wantContain)
			}
		})
	}
}
