package ui

import (
	"strings"
	"testing"

	"github.com/vinizap/lumi/tui-client/config"
)

// Modo zen: no legend / status line at rest; both come back over the
// bottom rows while a prefix is pending or ? help is open.

// zenModel starts with zen off whatever an earlier test persisted.
func zenModel(t *testing.T) Model {
	t.Helper()
	m := noteGoldenModel(t)
	m.zen = false
	return m
}

func TestZenLeaderTogglesAndPersists(t *testing.T) {
	m := zenModel(t)
	m = openNamed(t, m, "One")
	if m.zen {
		t.Fatal("zen on by default")
	}
	m = keys(t, m, `\`, "z")
	if !m.zen {
		t.Fatal("<leader>z did not turn zen on")
	}
	if !config.Load().Zen {
		t.Fatal("zen not persisted to config.yaml")
	}
	if m.legendVisible() || m.statusVisible() || m.bottomRows() != 0 {
		t.Fatal("zen at rest still shows chrome")
	}
	if m.contentHeight() != m.height {
		t.Fatalf("body height %d, want the full %d rows", m.contentHeight(), m.height)
	}
	golden(t, "note-reader-zen-100x24", m.View())

	// a pending prefix reveals the chrome over the body (no reflow)
	m = keys(t, m, "g")
	if !m.legendVisible() || !m.statusVisible() || m.contentHeight() != m.height {
		t.Fatal("pending g should reveal legend + status without reflow")
	}
	golden(t, "note-reader-zen-pending-g-100x24", m.View())
	m = keys(t, m, "esc")
	if m.legendVisible() {
		t.Fatal("chrome stayed after the prefix ended")
	}

	// ? help reveals it too
	m = keys(t, m, "?")
	if !m.legendVisible() || !m.statusVisible() {
		t.Fatal("help should reveal the chrome")
	}
	m = keys(t, m, "esc")

	m = keys(t, m, `\`, "z")
	if m.zen || config.Load().Zen {
		t.Fatal("<leader>z did not turn zen off")
	}
	if !m.statusVisible() {
		t.Fatal("status line missing after zen off")
	}
}

func TestZenExCommandAndEditorLeader(t *testing.T) {
	m := zenModel(t)
	m = openNamed(t, m, "One")
	m = keys(t, m, "i", "esc")
	m = keys(t, m, ":", "z", "e", "n", "enter")
	if !m.zen {
		t.Fatal(":zen did not turn zen on")
	}
	// the editor's own leader map toggles it back
	m = keys(t, m, `\`, "z")
	if m.zen {
		t.Fatal("editor <leader>z did not turn zen off")
	}
}

func TestZenEditorCmdlineAndOperatorReveal(t *testing.T) {
	m := zenModel(t)
	m = openNamed(t, m, "One")
	m = keys(t, m, `\`, "z", "i", "esc")
	if m.legendVisible() {
		t.Fatal("chrome visible at rest in the editor")
	}
	m = keys(t, m, "d")
	if !m.legendVisible() {
		t.Fatal("a pending operator should reveal the chrome")
	}
	m = keys(t, m, "esc", ":")
	if !m.legendVisible() {
		t.Fatal("the : command line must stay visible in zen")
	}
	if !strings.Contains(stripAnsi(m.View()), ":") {
		t.Fatal("command line not drawn")
	}
	m = keys(t, m, "esc")
	// typing in insert mode never reveals
	m = keys(t, m, "i", "a", "b")
	if m.legendVisible() || m.statusVisible() {
		t.Fatal("typing revealed the chrome")
	}
}

func TestZenPaletteEntry(t *testing.T) {
	m := noteGoldenModel(t)
	before := m.zen
	mm, _ := m.runCommand("zen")
	if mm.(Model).zen == before {
		t.Fatal("palette command did not toggle zen")
	}
}

func TestOverlayBottom(t *testing.T) {
	got := overlayBottom("a\nb\nc\nd", []string{"L", "S"})
	if got != "a\nb\nL\nS" {
		t.Fatalf("got %q", got)
	}
	if overlayBottom("a\nb", nil) != "a\nb" {
		t.Fatal("no rows must be a no-op")
	}
}
