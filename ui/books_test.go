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
	view := stripAnsi(m.View())
	for _, want := range []string{"◫ Livros", "3 livros · 2 contas", "Alpha", "Beta", "Local", "⌂ este aparelho", "⌂ s", "g1", "g3", "aberto"} {
		if !strings.Contains(view, want) {
			t.Errorf("shelf missing %q", want)
		}
	}
	// one list across account groups: j j lands on the remote book
	m, _ = pressKeys(m, "j", "j")
	if v := m.View(); !strings.Contains(v, "Remote") || !strings.Contains(v, "sem pasta") {
		t.Errorf("j/k cross groups: Remote (sem pasta) expected")
	}
	// two accounts: no accounts layer, h stays on Livros and says so
	m, _ = pressKeys(m, "h")
	if m.bk == nil || m.bk.accounts || !strings.Contains(m.toastMsg, "Livros é o topo") {
		t.Fatalf("h with 2 accounts: %+v toast %q", m.bk, m.toastMsg)
	}
	m, _ = pressKeys(m, "k")
	tm, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = tm.(Model)
	if m.SwitchTarget() != b || cmd == nil {
		t.Fatalf("switch target %q", m.SwitchTarget())
	}
}

// newShelfModel has books on four accounts: local, ana@a, bia@b, ana@b
// (interleaved in selector order), enough for "Contas e servidores".
func newShelfModel(t *testing.T) Model {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	a := t.TempDir()
	reg := &books.Registry{}
	reg.Add(books.Book{ID: "l1", Name: "Diário", Kind: books.KindLocal, Path: "~/Diário"})
	reg.Add(books.Book{ID: "r1", Name: "Trabalho", Kind: books.KindBound, ServerURL: "https://lumi.a.dev", Account: "ana", VaultID: "v1"})
	reg.Add(books.Book{ID: "r2", Name: "Família", Kind: books.KindBound, ServerURL: "https://notes.b.org", Account: "bia", VaultID: "v2"})
	reg.Add(books.Book{ID: "r3", Name: "Projetos", Kind: books.KindBound, ServerURL: "https://lumi.a.dev", Account: "ana", VaultID: "v3"})
	reg.Add(books.Book{ID: "r4", Name: "Receitas", Kind: books.KindBound, ServerURL: "https://notes.b.org", Account: "ana", VaultID: "v4"})
	st := &books.Store{Path: filepath.Join(t.TempDir(), "books.yaml"), Registry: reg}
	m := NewModelWithInitialNote(a, "").WithBooks(feed.NewManager(st, nil), "l1")
	m.width, m.height = 100, 30
	return m
}

func TestShelfGroupsAndAccountsLayer(t *testing.T) {
	m := newShelfModel(t)
	m, _ = pressKeys(m, "g", "b")
	m = settle(m, nil)
	// grouped by account, each where its first book appears; the g
	// numbers stay the selector order
	reg := m.bookMgr.Registry()
	var got []string
	for _, ix := range shelfFlat(shelfGroups(reg)) {
		got = append(got, reg[ix].Name)
	}
	if strings.Join(got, ",") != "Diário,Trabalho,Projetos,Família,Receitas" {
		t.Fatalf("shelf order %v", got)
	}
	v := stripAnsi(m.View())
	if !strings.Contains(v, "g3") || !strings.Contains(v, "Contas") || !strings.Contains(v, "4 contas") {
		t.Fatalf("shelf:\n%s", v)
	}
	// j j: Projetos (crosses nothing), j: Família (next group)
	m, _ = pressKeys(m, "j", "j", "j")
	if ix := m.shelfBook(); reg[ix].ID != "r2" {
		t.Fatalf("cursor on %s", reg[ix].Name)
	}
	// h climbs to the accounts layer on Família's account
	m, _ = pressKeys(m, "h")
	if !m.bk.accounts || m.bk.acct != 2 || !strings.Contains(stripAnsi(m.View()), "Contas e servidores") {
		t.Fatalf("accounts layer: %+v", m.bk)
	}
	// j, l: the next account's first book (ana@notes.b.org → Receitas)
	m, _ = pressKeys(m, "j", "l")
	if m.bk.accounts || reg[m.shelfBook()].ID != "r4" {
		t.Fatalf("l on an account lands on its first book: %+v", m.bk)
	}
	// J/K reorder; the cursor follows the book
	m, _ = pressKeys(m, "K")
	reg = m.bookMgr.Registry()
	if ix := m.shelfBook(); ix < 0 || reg[ix].ID != "r4" {
		t.Fatalf("cursor follows the moved book")
	}
	// gh goes back to this book's files
	m, _ = pressKeys(m, "g", "h")
	if m.bk != nil || !m.floatExp {
		t.Fatal("gh: back to the files")
	}
}

func TestShelfMouseSelectsBook(t *testing.T) {
	m := newShelfModel(t)
	m, _ = pressKeys(m, "g", "b")
	m = settle(m, nil)
	x, y := findText(t, m.View(), "Família")
	tm, _ := m.Update(tea.MouseMsg{X: x, Y: y, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	m = tm.(Model)
	if ix := m.shelfBook(); ix < 0 || m.bookMgr.Registry()[ix].ID != "r2" {
		t.Fatalf("click selects Família, got %d", ix)
	}
}

func TestShelfGoldens(t *testing.T) {
	m := newShelfModel(t)
	m, _ = pressKeys(m, "g", "b")
	m = settle(m, nil)
	golden(t, "books-shelf-100x30", stripAnsi(m.View()))
	m, _ = pressKeys(m, "h")
	golden(t, "books-accounts-100x30", stripAnsi(m.View()))
}

func TestG3OnPathlessBookAsksSignInInsteadOfCLIHint(t *testing.T) {
	m, _, _ := newBooksModel(t)
	m, _ = pressKeys(m, "g", "3")
	if m.SwitchTarget() != "" || strings.Contains(m.toastMsg, "<slug>") {
		t.Fatalf("target %q toast %q", m.SwitchTarget(), m.toastMsg)
	}
	// no account for u@s yet: the sign-in card comes first, prefilled
	if m.acct == nil || m.acct.form == nil || m.acct.form.server != "https://s" || m.acct.form.user != "u" ||
		m.acct.then == nil || m.acct.then.bookID != "c" {
		t.Fatalf("sign-in for the book's account expected, got %+v", m.acct)
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
