package ui

import (
	"testing"
)

// Goldens for the Part 2 note views (ASCII colour profile, 100×24).
func noteGoldenModel(t *testing.T) Model {
	t.Helper()
	m := newTreeModel(t, notesVault(t))
	m.width, m.height = 100, 24
	m.cfg.ReduceMotion = true
	return m
}

func TestNoteViewGoldens(t *testing.T) {
	m := noteGoldenModel(t)
	m = openNamed(t, m, "One")
	m = keys(t, m, "tab")
	golden(t, "note-reader-link-100x24", m.View())

	m = keys(t, m, "K")
	golden(t, "note-hover-card-100x24", m.View())
	m = keys(t, m, "esc")

	m = keys(t, m, "i", "G", "0", "w", "v", "e")
	golden(t, "note-editor-visual-100x24", m.View())
	m = keys(t, m, "esc", "i")
	golden(t, "note-editor-insert-100x24", m.View())
	m = keys(t, m, "esc", ":", "p", "r", "e", "v", "i", "e", "w", "enter")
	golden(t, "note-source-preview-100x24", m.View())

	m = keys(t, m, "ctrl+w", "v")
	golden(t, "note-vsplit-100x24", m.View())
	m = keys(t, m, "ctrl+w", "o", ":", "q", "enter", "-")
	golden(t, "note-float-explorer-100x24", m.View())
}

func TestBoardAndCanvasGoldens(t *testing.T) {
	m := noteGoldenModel(t)
	m = openNamed(t, m, "Board")
	m = keys(t, m, "j")
	golden(t, "note-board-100x24", m.View())
	m = keys(t, m, "q")
	m = keys(t, m, "g", "g")
	m = openNamed(t, m, "map")
	m = keys(t, m, "j", "j")
	golden(t, "note-canvas-100x24", m.View())
}

func TestAccountsGolden(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	m := noteGoldenModel(t)
	m = keys(t, m, "g", "a", "a")
	m = typeText(t, m, "https://lumi-api.example.com")
	m = keys(t, m, "tab")
	m = typeText(t, m, "ana")
	m = keys(t, m, "tab")
	m = typeText(t, m, "hunter2")
	golden(t, "accounts-login-form-100x24", m.View())
}
