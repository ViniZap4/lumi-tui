package ui

import (
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/vinizap/lumi/tui-client/books"
	"github.com/vinizap/lumi/tui-client/feed"
)

func runeKey(s string) tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)} }

func newBooksModel(t *testing.T) (Model, string, string) {
	t.Helper()
	a, b := t.TempDir(), t.TempDir()
	reg := &books.Registry{}
	reg.Add(books.Book{ID: "a", Name: "Alpha", Kind: books.KindLocal, Path: a})
	reg.Add(books.Book{ID: "b", Name: "Beta", Kind: books.KindLocal, Path: b})
	reg.Add(books.Book{ID: "c", Name: "Remote", Kind: books.KindBound, ServerURL: "https://s", Account: "u", VaultID: "v"})
	st := &books.Store{Path: filepath.Join(t.TempDir(), "books.yaml"), Registry: reg}
	mgr := feed.NewManager(st, nil)
	m := NewModelWithInitialNote(a, "").WithBooks(mgr, "a")
	m.width, m.height = 120, 40
	return m, a, b
}

func pressKeys(m Model, keys ...string) (Model, tea.Cmd) {
	var cmd tea.Cmd
	for _, k := range keys {
		var tm tea.Model
		tm, cmd = m.Update(runeKey(k))
		m = tm.(Model)
	}
	return m, cmd
}

func TestGbOpensBooksLevelAndEnterSwitches(t *testing.T) {
	m, _, b := newBooksModel(t)
	m, _ = pressKeys(m, "g", "b")
	if !m.floatExp || m.bk == nil || m.legendContext() != CtxBooks {
		t.Fatal("gb should open the floating explorer at the books level")
	}
	view := m.View()
	for _, want := range []string{"livros", "Alpha", "Beta", "local", "u@s"} {
		if !strings.Contains(view, want) {
			t.Errorf("books level missing %q", want)
		}
	}
	// accounts → books: the remote account lists its pathless book
	m, _ = pressKeys(m, "h", "j")
	if v := m.View(); !strings.Contains(v, "Remote") || !strings.Contains(v, "sem pasta") {
		t.Errorf("remote account should list Remote (sem pasta)")
	}
	m, _ = pressKeys(m, "k", "l")
	m, cmd := pressKeys(m, "j")
	tm, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = tm.(Model)
	if m.SwitchTarget() != b || cmd == nil {
		t.Fatalf("switch target %q", m.SwitchTarget())
	}
}

func TestG3OnPathlessBookShowsHintAndStays(t *testing.T) {
	m, _, _ := newBooksModel(t)
	m, _ = pressKeys(m, "g", "3")
	if m.SwitchTarget() != "" || !strings.Contains(m.toastMsg, "clone") {
		t.Fatalf("target %q toast %q", m.SwitchTarget(), m.toastMsg)
	}
}

func TestGsDetailsAndStatusSegment(t *testing.T) {
	m, _, _ := newBooksModel(t)
	if !strings.Contains(m.View(), "não ligado") {
		t.Error("status line should show the book state")
	}
	m, _ = pressKeys(m, "g", "s")
	if !m.showBookDetails || !strings.Contains(m.View(), "estado") {
		t.Fatal("gs should open details")
	}
	m, _ = pressKeys(m, "x")
	if m.showBookDetails {
		t.Fatal("any key closes details")
	}
}

func TestPickerJKReorders(t *testing.T) {
	m, _, _ := newBooksModel(t)
	m, _ = pressKeys(m, "g", "b", "J")
	if got := m.bookMgr.Registry()[1].ID; got != "a" {
		t.Fatalf("J should move Alpha down, order now has %s at 2", got)
	}
}
