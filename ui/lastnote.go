package ui

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/vinizap/lumi/tui-client/filesystem"
)

// Opening a book (SPEC-PARTE2 §2) shows its last opened note. The
// record is per book and per device, kept in the user's config dir
// (~/.config/lumi/state/last-notes.json, 0600) and never in the book
// folder, so it does not sync and does not leak into the notes. Keys
// are the book's absolute folder; values the note path relative to it.

const lastNotesCap = 200

// lastNotesFile is where the record lives ("" without a home dir).
func lastNotesFile() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	return filepath.Join(home, ".config", "lumi", "state", "last-notes.json")
}

type lastNotes struct {
	Books map[string]lastNoteEntry `json:"books"`
}

type lastNoteEntry struct {
	Note string    `json:"note"`
	At   time.Time `json:"at"`
}

func readLastNotes() lastNotes {
	ln := lastNotes{Books: map[string]lastNoteEntry{}}
	f := lastNotesFile()
	if f == "" {
		return ln
	}
	data, err := os.ReadFile(f)
	if err != nil {
		return ln
	}
	_ = json.Unmarshal(data, &ln)
	if ln.Books == nil {
		ln.Books = map[string]lastNoteEntry{}
	}
	return ln
}

// lastNoteFor is the absolute path of root's last note ("" when none
// is recorded or it is not inside the book any more).
func lastNoteFor(root string) string {
	e, ok := readLastNotes().Books[filepath.Clean(root)]
	if !ok || e.Note == "" {
		return ""
	}
	abs := filepath.Join(root, filepath.FromSlash(e.Note))
	if !pathIsInsideVault(root, abs) {
		return ""
	}
	return abs
}

// rememberLastNote records note as root's last opened note.
func rememberLastNote(root, note string) error {
	f := lastNotesFile()
	if f == "" {
		return nil
	}
	rel, err := filepath.Rel(root, note)
	if err != nil || strings.HasPrefix(rel, "..") {
		return nil
	}
	ln := readLastNotes()
	ln.Books[filepath.Clean(root)] = lastNoteEntry{Note: filepath.ToSlash(rel), At: saveNow()}
	if len(ln.Books) > lastNotesCap {
		// drop the oldest records
		type kv struct {
			k  string
			at time.Time
		}
		var all []kv
		for k, v := range ln.Books {
			all = append(all, kv{k, v.At})
		}
		sort.Slice(all, func(i, j int) bool { return all[i].at.Before(all[j].at) })
		for _, e := range all[:len(all)-lastNotesCap] {
			delete(ln.Books, e.k)
		}
	}
	data, err := json.MarshalIndent(ln, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(f), 0o700); err != nil {
		return err
	}
	return atomicWrite(f, data)
}

// noteRemembered keeps the record in step with the focused note (called
// after every update; writes only when the note changed).
func (m *Model) noteRemembered() {
	if m.ws == nil || m.ws.focus == nil || m.viewMode != ViewFullNote {
		return
	}
	p := m.ws.focus.buf.path
	if p == m.lastRemembered {
		return
	}
	m.lastRemembered = p
	_ = rememberLastNote(m.rootDir, p)
}

// openBook lands on the book: its last note (with the saved pane layout
// when that layout holds it), else the empty home with the floating
// explorer open.
func (m *Model) openBook() {
	if last := lastNoteFor(m.rootDir); last != "" {
		if note, err := filesystem.ReadNote(last); err == nil && note != nil {
			m.currentDir = filepath.Dir(last)
			m.pendingSelect = last
			if !m.restoreLayout(last) {
				m.openNoteIn(note, "replace")
			}
			if m.ws != nil && m.ws.focus != nil {
				m.fullNote = m.ws.focus.buf.note
				m.lastRemembered = m.ws.focus.buf.path
				return
			}
		}
	}
	m.viewMode = ViewHome
	m.openOverlay()
}

// --- recent notes (empty home) ---------------------------------------

const recentMax = 8

type recentLoadedMsg struct{ items []Item }

// loadRecent lists the book's most recently changed notes.
func (m Model) loadRecent() tea.Msg {
	return recentLoadedMsg{items: recentNotes(m.rootDir, recentMax)}
}

func recentNotes(root string, n int) []Item {
	type cand struct {
		path string
		mod  time.Time
	}
	var all []cand
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if p != root && strings.HasPrefix(d.Name(), ".") {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasPrefix(d.Name(), ".") || !filesystem.IsNoteFile(d.Name()) {
			return nil
		}
		if info, err := d.Info(); err == nil {
			all = append(all, cand{p, info.ModTime()})
		}
		return nil
	})
	sort.SliceStable(all, func(i, j int) bool {
		if !all[i].mod.Equal(all[j].mod) {
			return all[i].mod.After(all[j].mod)
		}
		return all[i].path < all[j].path
	})
	var out []Item
	for _, c := range all {
		if len(out) == n {
			break
		}
		note, _ := filesystem.ReadNote(c.path)
		if note == nil {
			continue
		}
		out = append(out, Item{Name: note.Title, Path: c.path, Note: note})
	}
	return out
}
