package ui

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// These tests cover the yazi-style navigation grammar in the tree
// browser (SPEC-V3 Phase U): h/l column walking, H/L history, the
// g/z key chords (gg, gh, zp), and — because the grammar shares a
// dispatch switch with everything else — that the pre-existing tree
// bindings still fire.

// newYaziVault builds a small vault with two levels of nesting:
//
//	root/
//	  alpha/
//	    nested/
//	      deep.md
//	    inner.md
//	  beta/
//	    b1.md
//	  one.md
//	  two.md
//
// Items in the root list come out as [alpha/, beta/, One, Two]
// (ReadDir order: folders first via loadItems, filename-sorted).
func newYaziVault(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	mustMkdir(t, filepath.Join(dir, "alpha", "nested"))
	mustMkdir(t, filepath.Join(dir, "beta"))
	mustWrite(t, filepath.Join(dir, "alpha", "inner.md"), "# inner\n\nbody.\n")
	mustWrite(t, filepath.Join(dir, "alpha", "nested", "deep.md"), "# deep\n\nbody.\n")
	mustWrite(t, filepath.Join(dir, "beta", "b1.md"), "# b1\n\nbody.\n")
	mustWrite(t, filepath.Join(dir, "one.md"), "# one\n\nbody.\n")
	mustWrite(t, filepath.Join(dir, "two.md"), "# two\n\nbody.\n")
	return dir
}

func mustMkdir(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0755); err != nil {
		t.Fatalf("mkdir %s: %v", path, err)
	}
}

// newTreeModel returns a Model sitting on the tree view of dir with
// the initial item listing loaded.
func newTreeModel(t *testing.T, dir string) Model {
	t.Helper()
	m := NewModelWithInitialNote(dir, "")
	m = drive(m)
	if m.viewMode != ViewTree {
		t.Fatalf("setup: viewMode = %v, want ViewTree", m.viewMode)
	}
	if len(m.items) == 0 {
		t.Fatalf("setup: no items loaded from %s", dir)
	}
	return m
}

// press dispatches each key through Update and drains any resulting
// Cmd (loadItems, etc.) so the model settles between keys — the same
// message pump Bubbletea runs, minus the goroutines.
func press(t *testing.T, m Model, keys ...string) Model {
	t.Helper()
	for _, key := range keys {
		var msg tea.KeyMsg
		if key == "enter" {
			msg = tea.KeyMsg{Type: tea.KeyEnter}
		} else {
			msg = tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(key)}
		}
		next, cmd := m.Update(msg)
		m = next.(Model)
		for _, out := range drainCmd(cmd) {
			n, _ := m.Update(out)
			m = n.(Model)
		}
	}
	return m
}

// moveTo presses `j` until the cursor sits on the item with the given
// display name. Fails the test if the item isn't in the current list.
func moveTo(t *testing.T, m Model, name string) Model {
	t.Helper()
	for range m.items {
		if m.cursor < len(m.items) && m.items[m.cursor].Name == name {
			return m
		}
		m = press(t, m, "j")
	}
	if m.cursor < len(m.items) && m.items[m.cursor].Name == name {
		return m
	}
	t.Fatalf("moveTo: item %q not found in %s", name, m.currentDir)
	return m
}

func TestTreeYaziColumnWalk(t *testing.T) {
	root := newYaziVault(t)

	tests := []struct {
		name   string
		keys   []string
		assert func(t *testing.T, m Model)
	}{
		{
			name: "l on a folder descends into it",
			keys: []string{"l"}, // cursor starts on alpha/
			assert: func(t *testing.T, m Model) {
				if want := filepath.Join(root, "alpha"); m.currentDir != want {
					t.Fatalf("currentDir = %q, want %q", m.currentDir, want)
				}
				if m.cursor != 0 {
					t.Fatalf("cursor = %d, want 0 after descending", m.cursor)
				}
			},
		},
		{
			name: "enter on a folder descends the same way",
			keys: []string{"enter"},
			assert: func(t *testing.T, m Model) {
				if want := filepath.Join(root, "alpha"); m.currentDir != want {
					t.Fatalf("currentDir = %q, want %q", m.currentDir, want)
				}
			},
		},
		{
			name: "l walks two levels deep",
			keys: []string{"l", "l"}, // alpha/ then nested/
			assert: func(t *testing.T, m Model) {
				if want := filepath.Join(root, "alpha", "nested"); m.currentDir != want {
					t.Fatalf("currentDir = %q, want %q", m.currentDir, want)
				}
			},
		},
		{
			name: "l on a note opens it like enter",
			keys: []string{"G", "l"}, // G lands on the last item (note Two)
			assert: func(t *testing.T, m Model) {
				if m.viewMode != ViewFullNote {
					t.Fatalf("viewMode = %v, want ViewFullNote", m.viewMode)
				}
				if m.fullNote == nil || m.fullNote.Title != "Two" {
					t.Fatalf("fullNote = %+v, want note Two", m.fullNote)
				}
			},
		},
		{
			name: "h ascends to the parent folder",
			keys: []string{"l", "l", "h"}, // down to nested, back to alpha
			assert: func(t *testing.T, m Model) {
				if want := filepath.Join(root, "alpha"); m.currentDir != want {
					t.Fatalf("currentDir = %q, want %q", m.currentDir, want)
				}
			},
		},
		{
			name: "h at the vault root is a no-op",
			keys: []string{"h"},
			assert: func(t *testing.T, m Model) {
				if m.currentDir != root {
					t.Fatalf("currentDir = %q, want root %q", m.currentDir, root)
				}
				if m.viewMode != ViewTree {
					t.Fatalf("viewMode = %v, want ViewTree", m.viewMode)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := newTreeModel(t, root)
			m = press(t, m, tt.keys...)
			tt.assert(t, m)
		})
	}
}

func TestTreeNavigationHistory(t *testing.T) {
	root := newYaziVault(t)

	t.Run("H returns to the previous folder and selection", func(t *testing.T) {
		m := newTreeModel(t, root)
		m = moveTo(t, m, "beta")
		betaCursor := m.cursor
		m = press(t, m, "l") // descend into beta
		if want := filepath.Join(root, "beta"); m.currentDir != want {
			t.Fatalf("setup: currentDir = %q, want %q", m.currentDir, want)
		}

		m = press(t, m, "H")
		if m.currentDir != root {
			t.Fatalf("after H: currentDir = %q, want root %q", m.currentDir, root)
		}
		if m.cursor != betaCursor {
			t.Fatalf("after H: cursor = %d, want restored %d", m.cursor, betaCursor)
		}
	})

	t.Run("L re-applies the jump H undid", func(t *testing.T) {
		m := newTreeModel(t, root)
		m = press(t, m, "l", "H", "L") // into alpha, back, forward
		if want := filepath.Join(root, "alpha"); m.currentDir != want {
			t.Fatalf("after L: currentDir = %q, want %q", m.currentDir, want)
		}
	})

	t.Run("fresh navigation clears the forward stack", func(t *testing.T) {
		m := newTreeModel(t, root)
		m = press(t, m, "l", "H") // into alpha, back — forward now holds alpha
		m = moveTo(t, m, "beta")
		m = press(t, m, "l") // navigating anew must drop the forward stack
		beta := filepath.Join(root, "beta")
		if m.currentDir != beta {
			t.Fatalf("setup: currentDir = %q, want %q", m.currentDir, beta)
		}

		m = press(t, m, "L")
		if m.currentDir != beta {
			t.Fatalf("after stale L: currentDir = %q, want unchanged %q", m.currentDir, beta)
		}
	})

	t.Run("H with no history is a no-op", func(t *testing.T) {
		m := newTreeModel(t, root)
		m = press(t, m, "H")
		if m.currentDir != root {
			t.Fatalf("currentDir = %q, want root %q", m.currentDir, root)
		}
	})

	t.Run("back stack is capped at navHistoryCap entries", func(t *testing.T) {
		m := newTreeModel(t, root)
		for i := 0; i < navHistoryCap+10; i++ {
			m.currentDir = filepath.Join(root, fmt.Sprintf("dir-%03d", i))
			m.pushHistory()
		}
		if got := len(m.histBack); got != navHistoryCap {
			t.Fatalf("len(histBack) = %d, want cap %d", got, navHistoryCap)
		}
		// The oldest entries fell off the bottom; the newest survives.
		newest := m.histBack[len(m.histBack)-1]
		if want := filepath.Join(root, fmt.Sprintf("dir-%03d", navHistoryCap+9)); newest.dir != want {
			t.Fatalf("newest entry = %q, want %q", newest.dir, want)
		}
	})

	t.Run("H clamps the restored cursor when the folder shrank", func(t *testing.T) {
		shrinkRoot := t.TempDir()
		sub := filepath.Join(shrinkRoot, "sub")
		mustMkdir(t, sub)
		mustWrite(t, filepath.Join(sub, "s1.md"), "# s1\n")
		mustWrite(t, filepath.Join(sub, "s2.md"), "# s2\n")
		mustWrite(t, filepath.Join(sub, "s3.md"), "# s3\n")

		m := newTreeModel(t, shrinkRoot)
		m = press(t, m, "l", "G") // into sub, cursor on last note (index 2)
		m = press(t, m, "h")      // ascend — history remembers (sub, 2)

		// Two of the three notes disappear before the user goes back.
		if err := os.Remove(filepath.Join(sub, "s2.md")); err != nil {
			t.Fatalf("remove: %v", err)
		}
		if err := os.Remove(filepath.Join(sub, "s3.md")); err != nil {
			t.Fatalf("remove: %v", err)
		}

		m = press(t, m, "H")
		if m.currentDir != sub {
			t.Fatalf("currentDir = %q, want %q", m.currentDir, sub)
		}
		if len(m.items) != 1 || m.cursor != 0 {
			t.Fatalf("items = %d, cursor = %d; want 1 item with clamped cursor 0", len(m.items), m.cursor)
		}
	})
}

func TestTreeGPrefixChords(t *testing.T) {
	root := newYaziVault(t)

	t.Run("gg jumps to the top", func(t *testing.T) {
		m := newTreeModel(t, root)
		m = press(t, m, "G")
		if m.cursor == 0 {
			t.Fatalf("setup: G should move cursor off the top")
		}
		m = press(t, m, "g", "g")
		if m.cursor != 0 {
			t.Fatalf("cursor = %d, want 0 after gg", m.cursor)
		}
	})

	t.Run("gh jumps to the vault root", func(t *testing.T) {
		m := newTreeModel(t, root)
		m = press(t, m, "l", "l") // alpha/nested
		m = press(t, m, "g", "h")
		if m.currentDir != root {
			t.Fatalf("currentDir = %q, want root %q", m.currentDir, root)
		}
	})

	t.Run("gh pushes history so H returns", func(t *testing.T) {
		m := newTreeModel(t, root)
		m = press(t, m, "l", "l") // alpha/nested
		m = press(t, m, "g", "h", "H")
		if want := filepath.Join(root, "alpha", "nested"); m.currentDir != want {
			t.Fatalf("after gh+H: currentDir = %q, want %q", m.currentDir, want)
		}
	})

	t.Run("gh at the root is a no-op and pollutes no history", func(t *testing.T) {
		m := newTreeModel(t, root)
		m = press(t, m, "g", "h")
		if m.currentDir != root {
			t.Fatalf("currentDir = %q, want root %q", m.currentDir, root)
		}
		if len(m.histBack) != 0 {
			t.Fatalf("histBack has %d entries, want 0", len(m.histBack))
		}
	})

	t.Run("unknown chord cancels the prefix and dispatches the key", func(t *testing.T) {
		m := newTreeModel(t, root)
		m = press(t, m, "g", "j")
		if m.cursor != 1 {
			t.Fatalf("cursor = %d, want 1 (g cancelled, j dispatched)", m.cursor)
		}
		if m.treePrefix != "" {
			t.Fatalf("treePrefix = %q, want cleared", m.treePrefix)
		}
	})
}

func TestTreePreviewToggle(t *testing.T) {
	root := newYaziVault(t)

	t.Run("zp toggles the preview column on and off", func(t *testing.T) {
		m := newTreeModel(t, root)
		if m.previewHidden {
			t.Fatalf("preview should start visible")
		}
		m = press(t, m, "z", "p")
		if !m.previewHidden {
			t.Fatalf("previewHidden = false, want true after zp")
		}
		if out := m.View(); out == "" {
			t.Fatalf("View() rendered empty with preview hidden")
		}
		m = press(t, m, "z", "p")
		if m.previewHidden {
			t.Fatalf("previewHidden = true, want false after second zp")
		}
	})

	t.Run("z followed by another key cancels the prefix", func(t *testing.T) {
		m := newTreeModel(t, root)
		m = press(t, m, "z", "j")
		if m.previewHidden {
			t.Fatalf("zj must not toggle the preview")
		}
		if m.cursor != 1 {
			t.Fatalf("cursor = %d, want 1 (z cancelled, j dispatched)", m.cursor)
		}
	})

	t.Run("treeColumnWidths drops the preview when hidden", func(t *testing.T) {
		tests := []struct {
			name        string
			total       int
			hidePreview bool
			wantRight   bool // whether a preview column should exist
			wantLeft    bool // whether a parent column should exist
		}{
			{"wide with preview", 120, false, true, true},
			{"wide without preview", 120, true, false, true},
			{"medium with preview", 60, false, true, false},
			{"medium without preview", 60, true, false, false},
			{"narrow ignores toggle", 40, true, false, false},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				left, center, right, sep := treeColumnWidths(tt.total, tt.hidePreview)
				if (right > 0) != tt.wantRight {
					t.Fatalf("right = %d, want present=%v", right, tt.wantRight)
				}
				if (left > 0) != tt.wantLeft {
					t.Fatalf("left = %d, want present=%v", left, tt.wantLeft)
				}
				// Columns + separators must never exceed the terminal.
				seps := 0
				if left > 0 {
					seps++
				}
				if right > 0 {
					seps++
				}
				if used := left + center + right + seps*3; used > tt.total {
					t.Fatalf("layout uses %d cols, terminal has %d", used, tt.total)
				}
				_ = sep
			})
		}
	})
}

// TestTreeExistingBindingsStillDispatch locks in that wiring the yazi
// grammar into the shared switch didn't shadow any pre-existing tree
// binding.
func TestTreeExistingBindingsStillDispatch(t *testing.T) {
	root := newYaziVault(t)

	tests := []struct {
		name   string
		setup  func(t *testing.T, m Model) Model
		keys   []string
		assert func(t *testing.T, m Model)
	}{
		{
			name: "j moves the cursor down",
			keys: []string{"j"},
			assert: func(t *testing.T, m Model) {
				if m.cursor != 1 {
					t.Fatalf("cursor = %d, want 1", m.cursor)
				}
			},
		},
		{
			name: "k moves the cursor back up",
			keys: []string{"j", "j", "k"},
			assert: func(t *testing.T, m Model) {
				if m.cursor != 1 {
					t.Fatalf("cursor = %d, want 1", m.cursor)
				}
			},
		},
		{
			name: "G jumps to the bottom",
			keys: []string{"G"},
			assert: func(t *testing.T, m Model) {
				if want := len(m.items) - 1; m.cursor != want {
					t.Fatalf("cursor = %d, want %d", m.cursor, want)
				}
			},
		},
		{
			name: "enter on a note opens it",
			setup: func(t *testing.T, m Model) Model {
				return moveTo(t, m, "One")
			},
			keys: []string{"enter"},
			assert: func(t *testing.T, m Model) {
				if m.viewMode != ViewFullNote || m.fullNote == nil {
					t.Fatalf("viewMode = %v, fullNote = %v; want open note", m.viewMode, m.fullNote)
				}
			},
		},
		{
			name: "n opens the create-note input",
			keys: []string{"n"},
			assert: func(t *testing.T, m Model) {
				if !m.showInput || m.inputMode != "create" {
					t.Fatalf("showInput = %v, inputMode = %q; want create input", m.showInput, m.inputMode)
				}
			},
		},
		{
			name: "N opens the create-folder input",
			keys: []string{"N"},
			assert: func(t *testing.T, m Model) {
				if !m.showInput || m.inputMode != "create_folder" {
					t.Fatalf("showInput = %v, inputMode = %q; want create_folder input", m.showInput, m.inputMode)
				}
			},
		},
		{
			name: "d on a note opens the delete confirm",
			setup: func(t *testing.T, m Model) Model {
				return moveTo(t, m, "One")
			},
			keys: []string{"d"},
			assert: func(t *testing.T, m Model) {
				if !m.showConfirm || m.pendingDeleteNote == nil {
					t.Fatalf("showConfirm = %v, pendingDeleteNote = %v; want confirm", m.showConfirm, m.pendingDeleteNote)
				}
			},
		},
		{
			name: "d on a folder opens the delete confirm",
			keys: []string{"d"}, // cursor starts on alpha/
			assert: func(t *testing.T, m Model) {
				if !m.showConfirm || m.pendingDeleteFolder == "" {
					t.Fatalf("showConfirm = %v, pendingDeleteFolder = %q; want confirm", m.showConfirm, m.pendingDeleteFolder)
				}
			},
		},
		{
			name: "r on a note opens the rename input",
			setup: func(t *testing.T, m Model) Model {
				return moveTo(t, m, "One")
			},
			keys: []string{"r"},
			assert: func(t *testing.T, m Model) {
				if !m.showInput || m.inputMode != "rename" {
					t.Fatalf("showInput = %v, inputMode = %q; want rename input", m.showInput, m.inputMode)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := newTreeModel(t, root)
			if tt.setup != nil {
				m = tt.setup(t, m)
			}
			m = press(t, m, tt.keys...)
			tt.assert(t, m)
		})
	}
}
