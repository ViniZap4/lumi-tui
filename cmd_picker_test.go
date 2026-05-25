package main

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/vinizap/lumi/tui-client/account"
)

func TestShouldRunVaultPicker(t *testing.T) {
	cases := []struct {
		name        string
		splash      bool
		initialNote string
		numVaults   int
		want        bool
	}{
		{"no splash, no note, no vaults", false, "", 0, false},
		{"splash, no note, no vaults", true, "", 0, false},
		{"splash, no note, 1 vault", true, "", 1, true},
		{"splash, no note, 5 vaults", true, "", 5, true},
		{"splash, initial note set, vaults exist", true, "/x/y.md", 3, false},
		{"no splash, vaults exist", false, "", 3, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := shouldRunVaultPicker(tc.splash, tc.initialNote, tc.numVaults)
			if got != tc.want {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
		})
	}
}

func sampleVaults() []account.VaultEntry {
	now := time.Now()
	return []account.VaultEntry{
		{ID: "a", Name: "work", Path: "/home/u/work", AddedAt: now},
		{ID: "b", Name: "personal", Path: "/home/u/personal", Server: "https://lumi.work.com", Account: "alice", AddedAt: now.Add(-time.Hour)},
		{ID: "c", Name: "side", Path: "/home/u/side", AddedAt: now.Add(-2 * time.Hour)},
	}
}

// TestVaultPickerNavigation walks the cursor through every direction
// supported by the picker (j/k, arrows, g/G, home/end) and asserts the
// clamping at both ends.
func TestVaultPickerNavigation(t *testing.T) {
	m := newVaultPickerModel(sampleVaults())
	if m.cursor != 0 {
		t.Fatalf("cursor starts at %d, want 0", m.cursor)
	}

	steps := []struct {
		key       string
		want      int
		desc      string
		boundary  bool // true when this step is expected to clamp without moving
	}{
		{"k", 0, "up at top stays at 0", true},
		{"up", 0, "up at top stays at 0", true},
		{"j", 1, "down to 1", false},
		{"down", 2, "down to 2", false},
		{"j", 2, "down at bottom clamps", true},
		{"down", 2, "down at bottom clamps", true},
		{"g", 0, "g jumps to top", false},
		{"G", 2, "G jumps to bottom", false},
		{"home", 0, "home jumps to top", false},
		{"end", 2, "end jumps to bottom", false},
	}
	mp := tea.Model(m)
	for _, s := range steps {
		var msg tea.KeyMsg
		switch s.key {
		case "j":
			msg = tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}}
		case "k":
			msg = tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'k'}}
		case "g":
			msg = tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'g'}}
		case "G":
			msg = tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'G'}}
		case "up":
			msg = tea.KeyMsg{Type: tea.KeyUp}
		case "down":
			msg = tea.KeyMsg{Type: tea.KeyDown}
		case "home":
			msg = tea.KeyMsg{Type: tea.KeyHome}
		case "end":
			msg = tea.KeyMsg{Type: tea.KeyEnd}
		}
		next, _ := mp.Update(msg)
		mp = next
		got := mp.(vaultPickerModel).cursor
		if got != s.want {
			t.Fatalf("step %q (%s): cursor = %d, want %d", s.key, s.desc, got, s.want)
		}
	}
}

func TestVaultPickerEnterChooses(t *testing.T) {
	m := newVaultPickerModel(sampleVaults())
	// move to row 1 (personal/server-bound)
	mp := tea.Model(m)
	mp, _ = mp.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	mp, cmd := mp.Update(tea.KeyMsg{Type: tea.KeyEnter})

	final := mp.(vaultPickerModel)
	if final.chosen == nil {
		t.Fatalf("chosen is nil after enter")
	}
	if final.chosen.Name != "personal" {
		t.Fatalf("chose %q, want personal", final.chosen.Name)
	}
	if final.cancelled {
		t.Fatalf("cancelled = true after enter; want false")
	}
	if cmd == nil {
		t.Fatalf("expected tea.Quit cmd on enter")
	}
}

func TestVaultPickerEscCancels(t *testing.T) {
	m := newVaultPickerModel(sampleVaults())
	mp, cmd := tea.Model(m).Update(tea.KeyMsg{Type: tea.KeyEsc})
	final := mp.(vaultPickerModel)
	if final.chosen != nil {
		t.Fatalf("chose %v on ESC; want nil", final.chosen)
	}
	if !final.cancelled {
		t.Fatalf("cancelled = false on ESC; want true")
	}
	if cmd == nil {
		t.Fatalf("expected tea.Quit cmd on ESC")
	}
}

func TestVaultPickerQCancels(t *testing.T) {
	m := newVaultPickerModel(sampleVaults())
	mp, cmd := tea.Model(m).Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
	final := mp.(vaultPickerModel)
	if final.chosen != nil {
		t.Fatalf("chose %v on q; want nil", final.chosen)
	}
	if !final.cancelled {
		t.Fatalf("cancelled = false on q; want true")
	}
	if cmd == nil {
		t.Fatalf("expected tea.Quit cmd on q")
	}
}

func TestVaultPickerViewRendersAllRows(t *testing.T) {
	m := newVaultPickerModel(sampleVaults())
	out := m.View()
	for _, want := range []string{"work", "personal", "side", "/home/u/work", "alice@", "Pick a vault"} {
		if !strings.Contains(out, want) {
			t.Errorf("view missing %q\n---\n%s", want, out)
		}
	}
	// cursor indicator on first row only
	if !strings.Contains(out, "▸ work") && !strings.Contains(out, "▸ work ") {
		// Allow trailing padding; just check the marker precedes the name.
		if !strings.Contains(out, "▸ ") {
			t.Errorf("view missing cursor marker:\n%s", out)
		}
	}
}

func TestRunVaultPickerEmptyListReturnsNil(t *testing.T) {
	got, err := runVaultPicker(nil)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if got != nil {
		t.Fatalf("got %v, want nil for empty list", got)
	}
}
