package ui

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/vinizap/lumi/tui-client/books"
	"github.com/vinizap/lumi/tui-client/theme"
)

// The empty home (SPEC-PARTE2 §2): shown when the book has no last
// note. Calm: the book's name, its sync state and the recent notes; the
// floating explorer opens over it on arrival.

// notesButton is the top-line explorer button (mouse; also `-`).
const notesButton = " ≡ notes "

// notesButtonWidth is its width in columns (the click target).
var notesButtonWidth = lipgloss.Width(notesButton)

// renderNotesButton paints the button; lit while the overlay is open.
func (m Model) renderNotesButton() string {
	t := theme.Current
	st := lipgloss.NewStyle().Foreground(t.Primary).Background(t.SelectedBg).Bold(true)
	if m.floatExp {
		st = st.Foreground(t.Background).Background(t.Accent)
	}
	return st.Render(notesButton)
}

// bookName is the active book's name (the folder name otherwise).
func (m Model) bookName() string {
	if m.bookMgr != nil {
		if v, ok := m.bookMgr.View(m.activeBookID); ok && v.Book.Name != "" {
			return v.Book.Name
		}
	}
	n := filepath.Base(m.rootDir)
	if n == "" || n == "." || n == "/" {
		return "(root)"
	}
	return n
}

// syncGlyph is the book state dot, alive: it breathes (braille) while
// syncing and pulses right after a remote change.
func (m Model) syncGlyph(st books.State) string {
	g := st.Glyph()
	style := lipgloss.NewStyle().Foreground(stateColor(st))
	if st == books.StateSyncing {
		if b := m.mo.breatheGlyph(); b != "" {
			g = b
		}
	}
	if a, _ := m.mo.get(animPulse); a.active && a.pos > 0.15 {
		g = "◉"
		style = style.Foreground(theme.Current.Accent).Bold(true)
	}
	return style.Render(g)
}

// renderHome paints the empty home.
func (m Model) renderHome() string {
	t := theme.Current
	h := m.contentHeight()
	dim := lipgloss.NewStyle().Foreground(t.TextDim)
	text := lipgloss.NewStyle().Foreground(t.Text)
	key := lipgloss.NewStyle().Foreground(secondaryColor).Bold(true)

	top := m.renderNotesButton()
	brand := lipgloss.NewStyle().Foreground(primaryColor).Bold(true).Render("lumi ")
	top = padVisual(top, m.width-lipgloss.Width(brand)) + brand

	w := min(m.width-4, 64)
	var block []string
	block = append(block, lipgloss.NewStyle().Foreground(primaryColor).Bold(true).Render(truncate(m.bookName(), w)))
	state := dim.Render("pasta local")
	if m.bookMgr != nil {
		if v, ok := m.bookMgr.View(m.activeBookID); ok {
			st := v.Status.State
			state = m.syncGlyph(st) + " " + lipgloss.NewStyle().Foreground(stateColor(st)).Render(st.Label())
			if n := len(v.Pending); n > 0 {
				state += lipgloss.NewStyle().Foreground(t.Warning).Render(" · " + pendingLabel(n))
			}
			if v.Book.IsBound() {
				state += dim.Render(" · " + v.Book.Account + "@" + books.Host(v.Book.ServerURL))
			}
		}
	}
	block = append(block, state, "")
	block = append(block, dim.Render("Notas recentes"))
	if len(m.recent) == 0 {
		block = append(block, dim.Render("  nenhuma nota ainda — a cria uma no explorador"))
	}
	for i, it := range m.recent {
		num := "  "
		if i < 9 {
			num = fmt.Sprintf("%d ", i+1)
		}
		rel := m.displayRel(filepath.Dir(it.Path))
		if rel == "~/." {
			rel = "~"
		}
		name := truncate(it.Name, max(8, w-lipgloss.Width(rel)-8))
		row := " " + num + name
		pad := max(1, w-lipgloss.Width(row)-lipgloss.Width(rel))
		line := text.Render(row) + strings.Repeat(" ", pad) + dim.Render(rel)
		if i == m.homeCursor {
			line = lipgloss.NewStyle().Foreground(t.Background).Background(accentColor).Bold(true).
				Render(padRight(row+strings.Repeat(" ", pad)+rel, w))
		}
		block = append(block, line)
	}
	block = append(block, "", key.Render("-")+dim.Render(" explorar   ")+key.Render("space")+dim.Render(" comandos   ")+
		key.Render("gb")+dim.Render(" livros   ")+key.Render("?")+dim.Render(" teclas"))

	for i := range block {
		block[i] = padVisual(block[i], w)
	}
	left := max(0, (m.width-w)/2)
	topPad := max(1, (h-len(block))/2)
	out := []string{top}
	for i := 1; i < topPad; i++ {
		out = append(out, "")
	}
	for _, l := range block {
		out = append(out, strings.Repeat(" ", left)+l)
	}
	return strings.Join(out, "\n")
}

// homeRowAt maps a body row to a recent-note index (-1: none).
func (m Model) homeRowAt(y int) int {
	h := m.contentHeight()
	n := 4 + max(1, len(m.recent)) + 2 // name, state, blank, label, rows, blank, keys
	if len(m.recent) == 0 {
		return -1
	}
	topPad := max(1, (h-n)/2)
	i := y - topPad - 4
	if i < 0 || i >= len(m.recent) {
		return -1
	}
	return i
}
