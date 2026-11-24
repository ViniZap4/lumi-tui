package ui

import (
	"io/fs"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/vinizap/lumi/tui-client/filesystem"
	"github.com/vinizap/lumi/tui-client/theme"
)

// Command palette (`space`, or `:` for commands only): fuzzy over
// commands, books and every note of the book. Enter runs the pick.

type paletteKind int

const (
	palCommand paletteKind = iota
	palBook
	palNote
)

type paletteEntry struct {
	kind   paletteKind
	label  string
	detail string
	keys   string // shortcut shown at the right (commands)
	id     string // command id / book index / note path
	score  int
}

// paletteCommands is the command list (ids run by runCommand).
var paletteCommands = []paletteEntry{
	{kind: palCommand, label: "New note or folder", keys: "a", id: "create"},
	{kind: palCommand, label: "Rename / move", keys: "r", id: "rename"},
	{kind: palCommand, label: "Delete", keys: "d", id: "delete"},
	{kind: palCommand, label: "Edit (default editor)", keys: "e i", id: "edit"},
	{kind: palCommand, label: "Edit with the other editor (built-in ↔ $EDITOR)", keys: "E", id: "edit-other"},
	{kind: palCommand, label: "View: reader", keys: ":reader", id: "view-reader"},
	{kind: palCommand, label: "View: source (built-in vim editor)", keys: ":source", id: "view-source"},
	{kind: palCommand, label: "View: source + preview", keys: ":preview", id: "view-preview"},
	{kind: palCommand, label: "View: kanban board", keys: ":board", id: "view-board"},
	{kind: palCommand, label: "Split vertically", keys: "⌃w v", id: "vsplit"},
	{kind: palCommand, label: "Split horizontally", keys: "⌃w s", id: "hsplit"},
	{kind: palCommand, label: "Explore notes (floating explorer)", keys: "- :explore", id: "float"},
	{kind: palCommand, label: "Restore the last pane layout", keys: ":layout", id: "layout"},
	{kind: palCommand, label: "Accounts and servers (sign in / out)", keys: "ga", id: "accounts"},
	{kind: palCommand, label: "Filter this folder", keys: "/", id: "filter"},
	{kind: palCommand, label: "Search note contents", keys: "", id: "search"},
	{kind: palCommand, label: "Refresh", keys: "R", id: "refresh"},
	{kind: palCommand, label: "Toggle preview column", keys: "zp", id: "preview"},
	{kind: palCommand, label: "Toggle hidden files", keys: ".", id: "hidden"},
	{kind: palCommand, label: "Toggle command legend", keys: "", id: "legend"},
	{kind: palCommand, label: "Go to book root", keys: "~", id: "root"},
	{kind: palCommand, label: "Books", keys: "gb", id: "books"},
	{kind: palCommand, label: "Sync details", keys: "gs", id: "sync"},
	{kind: palCommand, label: "Previous book", keys: "[", id: "prevbook"},
	{kind: palCommand, label: "Next book", keys: "]", id: "nextbook"},
	{kind: palCommand, label: "Settings", keys: ",", id: "settings"},
	{kind: palCommand, label: "Keys (help)", keys: "?", id: "help"},
	{kind: palCommand, label: "Quit", keys: "q", id: "quit"},
}

// collectNotes walks the book for the palette (skipping dot dirs).
func collectNotes(root string) []paletteEntry {
	var out []paletteEntry
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
		n, _ := filesystem.ReadNote(p)
		if n == nil {
			return nil
		}
		rel, _ := filepath.Rel(root, p)
		out = append(out, paletteEntry{kind: palNote, label: n.Title, detail: filepath.ToSlash(filepath.Dir(rel)), id: p})
		return nil
	})
	return out
}

// paletteResults ranks every entry against the query. A query starting
// with ":" lists commands only.
func (m Model) paletteResults() []paletteEntry {
	q := m.paletteQuery
	cmdsOnly := strings.HasPrefix(q, ":")
	q = strings.TrimSpace(strings.TrimPrefix(q, ":"))
	var all []paletteEntry
	all = append(all, paletteCommands...)
	if !cmdsOnly {
		if m.bookMgr != nil {
			for i, b := range m.bookMgr.Registry() {
				all = append(all, paletteEntry{kind: palBook, label: b.Name, detail: "book", id: strconv.Itoa(i)})
			}
		}
		all = append(all, m.paletteNotes...)
	}
	var out []paletteEntry
	for _, e := range all {
		s := fuzzyScore(q, e.label)
		if d := fuzzyScore(q, e.detail+"/"+e.label); d-5 > s {
			s = d - 5
		}
		if s < 0 {
			continue
		}
		e.score = s
		out = append(out, e)
	}
	if q != "" {
		sort.SliceStable(out, func(i, j int) bool { return out[i].score > out[j].score })
	}
	if len(out) > 200 {
		out = out[:200]
	}
	return out
}

func (m *Model) openPalette(prefix string) {
	m.showPalette = true
	m.paletteQuery = prefix
	m.paletteCursor = 0
	if prefix == "" {
		m.paletteNotes = collectNotes(m.rootDir)
	}
}

func (m Model) updatePalette(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	res := m.paletteResults()
	switch msg.String() {
	case "esc", "ctrl+c":
		m.showPalette = false
		return m, nil
	case "down", "ctrl+n", "ctrl+j", "tab":
		if len(res) > 0 {
			m.paletteCursor = (m.paletteCursor + 1) % len(res)
		}
		return m, nil
	case "up", "ctrl+p", "ctrl+k", "shift+tab":
		if len(res) > 0 {
			m.paletteCursor = (m.paletteCursor - 1 + len(res)) % len(res)
		}
		return m, nil
	case "enter":
		m.showPalette = false
		if m.paletteCursor >= len(res) {
			return m, nil
		}
		return m.runPaletteEntry(res[m.paletteCursor])
	}
	if v, ok := editLine(m.paletteQuery, msg); ok {
		if v == m.paletteQuery {
			return m, nil
		}
		if strings.HasPrefix(m.paletteQuery, ":") && !strings.HasPrefix(v, ":") && m.paletteNotes == nil {
			m.paletteNotes = collectNotes(m.rootDir)
		}
		m.paletteQuery = v
		m.paletteCursor = 0
	}
	return m, nil
}

func (m Model) runPaletteEntry(e paletteEntry) (tea.Model, tea.Cmd) {
	switch e.kind {
	case palBook:
		i, _ := strconv.Atoi(e.id)
		return m.switchToBook(i)
	case palNote:
		dir := filepath.Dir(e.id)
		cmd := m.navigateTo(dir, 0)
		m.clearFilter()
		m.pendingSelect = e.id
		if n, _ := filesystem.ReadNote(e.id); n != nil {
			if m.floatExp {
				m.closeOverlay()
			}
			m.openNote(n)
		}
		return m, tea.Batch(cmd, m.shapeCmd())
	}
	return m.runCommand(e.id)
}

func (m Model) renderPalette() string {
	t := theme.Current
	width := min(m.width-4, 84)
	if width < 20 {
		width = m.width
	}
	inner := width - 4
	res := m.paletteResults()
	prompt := lipgloss.NewStyle().Foreground(t.Primary).Bold(true).Render("› ")
	q := lipgloss.NewStyle().Foreground(t.Text).Render(m.paletteQuery) + lipgloss.NewStyle().Foreground(t.Accent).Render("▏")
	var s strings.Builder
	s.WriteString(prompt + q + "\n")
	s.WriteString(lipgloss.NewStyle().Foreground(t.Separator).Render(strings.Repeat("─", inner)) + "\n")
	rows := max(3, min(14, m.height-10))
	start := 0
	if m.paletteCursor >= rows {
		start = m.paletteCursor - rows + 1
	}
	if len(res) == 0 {
		s.WriteString(lipgloss.NewStyle().Foreground(t.TextDim).Render("  nothing matches"))
	}
	for i := start; i < len(res) && i < start+rows; i++ {
		e := res[i]
		tag := map[paletteKind]string{palCommand: "cmd ", palBook: "book", palNote: "note"}[e.kind]
		right := e.keys
		if e.kind != palCommand {
			right = e.detail
			if right == "." {
				right = ""
			}
		}
		left := lipgloss.NewStyle().Foreground(t.TextDim).Render(tag) + "  " + e.label
		lw := 6 + lipgloss.Width(e.label)
		rw := lipgloss.Width(right)
		var line string
		if lw+rw+2 <= inner {
			line = left + strings.Repeat(" ", inner-lw-rw) + lipgloss.NewStyle().Foreground(t.TextDim).Render(right)
		} else {
			line = truncate(stripAnsi(left), inner)
		}
		if i == m.paletteCursor {
			line = lipgloss.NewStyle().Background(t.SelectedBg).Foreground(t.Text).Bold(true).Render(padRight(stripAnsi(line), inner))
		}
		s.WriteString(line + "\n")
	}
	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(t.Border).
		Padding(0, 1).
		Width(width).
		Render(strings.TrimRight(s.String(), "\n"))
	return box
}
