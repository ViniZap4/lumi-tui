package ui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-runewidth"
	"github.com/vinizap/lumi/tui-client/theme"
)

// Floating explorer mouse affordances (SPEC-PARTE2 §2–§3), besides
// ⏎ / ⌃v / ⌃s:
//   - the selected note row shows inline actions "open · ⇥ right ·
//     ⇩ below" (click: open here / vertical split / horizontal split);
//   - right-click on a row opens a context menu with the same three;
//   - the overlay's footer is a context-aware legend fitted to the width
//     by priority, every item clickable (it presses its key), with the
//     item count right-aligned.

// rowAction is one inline / context-menu action.
type rowAction struct{ label, short, menu, keys, how string }

var rowActions = []rowAction{
	{"open", "open", "Open", "⏎", "replace"},
	{"⇥ right", "⇥", "Open right", "⌃v", "vsplit"},
	{"⇩ below", "⇩", "Open below", "⌃s", "hsplit"},
}

// rowNameRoom is the room a row keeps for the name next to its actions.
const rowNameRoom = 14

const rowActionSep = " · "

// rowActionsText is the inline actions for a row of width innerWidth+1
// (full labels, or "open · ⇥ · ⇩" when narrow; "" when there is no
// room) and their [x0, x1) spans, relative to the start of the text.
func rowActionsText(innerWidth int) (string, [][2]int) {
	for _, short := range []bool{false, true} {
		var b strings.Builder
		var spans [][2]int
		x := 0
		for i, a := range rowActions {
			if i > 0 {
				b.WriteString(rowActionSep)
				x += runewidth.StringWidth(rowActionSep)
			}
			l := a.label
			if short {
				l = a.short
			}
			w := runewidth.StringWidth(l)
			spans = append(spans, [2]int{x, x + w})
			b.WriteString(l)
			x += w
		}
		b.WriteString(" ")
		if innerWidth+1 >= x+1+rowNameRoom {
			return b.String(), spans
		}
	}
	return "", nil
}

// rowActionsShown: the selected row has room for its inline actions.
func rowActionsShown(innerWidth int) bool {
	t, _ := rowActionsText(innerWidth)
	return t != ""
}

// rowActionAt is the action under column x of a selected row of width
// innerWidth+1 (relative to the row start); -1: none.
func rowActionAt(innerWidth, x int) int {
	if !rowActionsShown(innerWidth) {
		return -1
	}
	t, spans := rowActionsText(innerWidth)
	start := innerWidth + 1 - runewidth.StringWidth(t)
	for i, s := range spans {
		if x >= start+s[0] && x < start+s[1] {
			return i
		}
	}
	return -1
}

// centerColX is the x of the active list column (explorer coordinates).
func (m Model) centerColX() int {
	left, _, _, sep := treeColumnWidths(m.width, m.previewHidden)
	if left > 0 {
		return left + sep
	}
	return 0
}

// openItemHow opens the selected note here / in a split.
func (m Model) openItemHow(how string) (Model, tea.Cmd) {
	it, ok := m.currentItem()
	if !ok || it.IsFolder || it.Note == nil {
		return m, nil
	}
	if m.ws == nil || m.ws.root == nil {
		how = "replace"
	}
	m.closeOverlay()
	m.ctxMenu = nil
	cmd := m.openNoteIn(it.Note, how)
	return m, tea.Batch(cmd, m.shapeCmd())
}

// --- context menu ------------------------------------------------------

// ctxMenu is the right-click menu (screen coordinates).
type ctxMenu struct {
	x, y   int
	cursor int
}

func (m Model) ctxMenuBox() (string, rect) {
	t := theme.Current
	w := 0
	for _, a := range rowActions {
		w = max(w, runewidth.StringWidth(a.menu)+runewidth.StringWidth(a.keys)+4)
	}
	var lines []string
	for i, a := range rowActions {
		gap := w - runewidth.StringWidth(a.menu) - runewidth.StringWidth(a.keys) - 2
		l := " " + a.menu + strings.Repeat(" ", gap) + lipgloss.NewStyle().Foreground(t.TextDim).Render(a.keys) + " "
		if i == m.ctxMenu.cursor {
			l = lipgloss.NewStyle().Background(t.SelectedBg).Foreground(t.Text).Bold(true).Render(stripAnsi(l))
		}
		lines = append(lines, l)
	}
	box := lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(t.Accent).
		Background(t.Background).Render(strings.Join(lines, "\n"))
	bw, bh := w+2, len(rowActions)+2
	x := clamp(m.ctxMenu.x, 0, max(0, m.width-bw))
	y := clamp(m.ctxMenu.y, 0, max(0, m.contentHeight()-bh))
	return box, rect{x, y, bw, bh}
}

// updateCtxMenuKey: j/k/↑/↓ move, ⏎ runs, Esc/q close.
func (m Model) updateCtxMenuKey(key string) (Model, tea.Cmd) {
	switch key {
	case "j", "down", "tab":
		m.ctxMenu.cursor = (m.ctxMenu.cursor + 1) % len(rowActions)
	case "k", "up", "shift+tab":
		m.ctxMenu.cursor = (m.ctxMenu.cursor + len(rowActions) - 1) % len(rowActions)
	case "enter", "l":
		return m.openItemHow(rowActions[m.ctxMenu.cursor].how)
	case "ctrl+v", "ctrl+s":
		return m.openItemHow(map[string]string{"ctrl+v": "vsplit", "ctrl+s": "hsplit"}[key])
	default:
		m.ctxMenu = nil
	}
	return m, nil
}

// ctxMenuMouse: a click on an item runs it; elsewhere closes the menu.
func (m Model) ctxMenuMouse(msg tea.MouseMsg) (Model, tea.Cmd) {
	_, r := m.ctxMenuBox()
	if msg.Action != tea.MouseActionPress {
		return m, nil
	}
	if msg.X > r.x && msg.X < r.x+r.w-1 && msg.Y > r.y && msg.Y < r.y+r.h-1 {
		return m.openItemHow(rowActions[msg.Y-r.y-1].how)
	}
	m.ctxMenu = nil
	return m, nil
}

// --- footer legend -------------------------------------------------------

// footSpan is a clickable footer item.
type footSpan struct {
	x0, x1 int
	keys   []string
}

// legendKeys turns a legend item's first key token into key presses.
func legendKeys(token string) []string {
	special := map[string]string{"⏎": "enter", "Esc": "esc", "space": " ", "Tab": "tab", "⇧Tab": "shift+tab",
		"↑": "up", "↓": "down", "←": "left", "→": "right", "1–9": "1"}
	if k, ok := special[token]; ok {
		return []string{k}
	}
	if strings.HasPrefix(token, "⌃") {
		return []string{"ctrl+" + strings.TrimPrefix(token, "⌃")}
	}
	var out []string
	for _, r := range token {
		out = append(out, string(r))
	}
	return out
}

// explorerFooter is the overlay's footer: the context legend fitted to
// width by priority (clickable spans, explorer coordinates) and the
// item count on the right.
func (m Model) explorerFooter(width int) (string, []footSpan) {
	ctx := m.legendContext()
	items := legendFor(ctx, m.pendingInput())
	stat := ""
	switch {
	case m.bk != nil:
		n := 0
		if m.bookMgr != nil {
			n = len(m.bookMgr.Registry())
		}
		stat = fmt.Sprintf("%d livros", n)
	case m.filter != "":
		stat = fmt.Sprintf("%d/%d", len(m.items), len(m.allItems))
	default:
		stat = fmt.Sprintf("%d itens", len(m.items))
		if len(m.items) == 1 {
			stat = "1 item"
		}
	}
	statSt := lipgloss.NewStyle().Foreground(mutedColor).Italic(true)
	sw := runewidth.StringWidth(stat) + 1
	const gap = 2
	widths := make([]int, len(items))
	for i, it := range items {
		widths[i] = legendItemWidth(it)
	}
	avail := max(0, width-sw-2)
	shown, overflow := fitLegend(widths, avail, gap, 2)
	keySt := lipgloss.NewStyle().Foreground(secondaryColor).Bold(true)
	descSt := lipgloss.NewStyle().Foreground(mutedColor)
	var b strings.Builder
	var spans []footSpan
	b.WriteString(" ")
	x := 1
	for n, i := range shown {
		if n > 0 {
			b.WriteString(strings.Repeat(" ", gap))
			x += gap
		}
		it := items[i]
		b.WriteString(keySt.Render(it.Keys) + " " + descSt.Render(it.Label))
		tok := strings.Fields(it.Keys)
		if len(tok) > 0 {
			spans = append(spans, footSpan{x, x + widths[i], legendKeys(tok[0])})
		}
		x += widths[i]
	}
	if overflow {
		b.WriteString(strings.Repeat(" ", gap) + descSt.Render("…"))
		x += gap + 1
	}
	line := b.String()
	pad := max(1, width-x-sw)
	line += strings.Repeat(" ", pad) + statSt.Render(stat) + " "
	return padVisual(line, width), spans
}

// footerClick presses the keys of the footer item under x.
func (m Model) footerClick(x int, width int) (tea.Model, tea.Cmd, bool) {
	_, spans := m.explorerFooter(width)
	for _, s := range spans {
		if x >= s.x0 && x < s.x1 {
			var mm tea.Model = m
			var cmds []tea.Cmd
			for _, k := range s.keys {
				var cmd tea.Cmd
				mm, cmd = mm.(Model).update(keyFor(k))
				cmds = append(cmds, cmd)
			}
			return mm, tea.Batch(cmds...), true
		}
	}
	return m, nil, false
}

// keyFor builds the key message for a key name.
func keyFor(k string) tea.KeyMsg {
	special := map[string]tea.KeyType{
		"enter": tea.KeyEnter, "esc": tea.KeyEsc, "tab": tea.KeyTab, "shift+tab": tea.KeyShiftTab, " ": tea.KeySpace,
		"up": tea.KeyUp, "down": tea.KeyDown, "left": tea.KeyLeft, "right": tea.KeyRight,
		"ctrl+v": tea.KeyCtrlV, "ctrl+s": tea.KeyCtrlS, "ctrl+d": tea.KeyCtrlD, "ctrl+u": tea.KeyCtrlU,
		"ctrl+h": tea.KeyCtrlH, "ctrl+l": tea.KeyCtrlL, "ctrl+j": tea.KeyCtrlJ, "ctrl+k": tea.KeyCtrlK,
	}
	if t, ok := special[k]; ok {
		return tea.KeyMsg{Type: t}
	}
	return keyRunes(k)
}
