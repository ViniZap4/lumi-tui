package ui

import (
	"os"
	"path/filepath"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// TestInitialNoteAutoOpen drives the message flow that fires when a
// user runs `lumi some/note.md`: NewModelWithInitialNote → Init →
// itemsLoadedMsg → openNote. Verifies the model lands on ViewFullNote
// with the requested file open.
//
// The test creates a temp dir with two .md files and runs the harness
// for both `lumi <dir>` and `lumi <file>` cases so a regression on
// either path surfaces here, not in production.
func TestInitialNoteAutoOpen(t *testing.T) {
	dir := t.TempDir()
	plainPath := filepath.Join(dir, "plain.md")
	hrPath := filepath.Join(dir, "with-hr.md")
	mustWrite(t, plainPath, "# plain\n\nbody.\n")
	mustWrite(t, hrPath, "# heading\n\nfirst.\n\n---\n\nsecond.\n")

	t.Run("dir-only opens at tree without auto-opening any note", func(t *testing.T) {
		m := NewModelWithInitialNote(dir, "")
		m = drive(m)
		if got := m.viewMode; got != ViewTree {
			t.Fatalf("viewMode = %v, want ViewTree", got)
		}
		if m.fullNote != nil {
			t.Fatalf("fullNote should be nil when no initialNotePath, got %s", m.fullNote.Path)
		}
		if len(m.items) == 0 {
			t.Fatalf("items should include the two .md files; got 0")
		}
	})

	t.Run("file arg auto-opens the matching note", func(t *testing.T) {
		m := NewModelWithInitialNote(dir, hrPath)
		m = drive(m)
		if got := m.viewMode; got != ViewFullNote {
			t.Fatalf("viewMode = %v, want ViewFullNote", got)
		}
		if m.fullNote == nil {
			t.Fatalf("fullNote should be set after auto-open")
		}
		if m.fullNote.Path != hrPath {
			t.Fatalf("fullNote.Path = %q, want %q", m.fullNote.Path, hrPath)
		}
		// The horizontal-rule file used to silently disappear under
		// the old SplitN parser; this assertion locks in that we now
		// load it.
		if len(m.fullNote.Content) == 0 {
			t.Fatalf("auto-opened note has empty content")
		}
	})

	t.Run("file arg outside the listing falls back to direct read", func(t *testing.T) {
		// Simulate a file in a sibling directory that ListNotes will
		// never surface for the current m.currentDir.
		other := t.TempDir()
		otherFile := filepath.Join(other, "stray.md")
		mustWrite(t, otherFile, "# stray\n")

		m := NewModelWithInitialNote(dir, otherFile) // dir != other
		m = drive(m)
		if m.viewMode != ViewFullNote {
			t.Fatalf("viewMode = %v, want ViewFullNote (fallback path)", m.viewMode)
		}
		if m.fullNote == nil || m.fullNote.Path != otherFile {
			t.Fatalf("fallback read failed; got %+v", m.fullNote)
		}
	})
}

// drive feeds the model an initial WindowSizeMsg and runs whatever
// commands Init returns until the queue drains. Returns the resulting
// model.
func drive(m Model) Model {
	m2, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	current := m2.(Model)

	cmd := current.Init()
	for _, msg := range drainCmd(cmd) {
		next, _ := current.Update(msg)
		current = next.(Model)
	}
	return current
}

func drainCmd(cmd tea.Cmd) []tea.Msg {
	if cmd == nil {
		return nil
	}
	msg := cmd()
	if msg == nil {
		return nil
	}
	if batch, ok := msg.(tea.BatchMsg); ok {
		var out []tea.Msg
		for _, sub := range batch {
			out = append(out, drainCmd(sub)...)
		}
		return out
	}
	return []tea.Msg{msg}
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
