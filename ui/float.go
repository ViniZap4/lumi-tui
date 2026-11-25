package ui

import (
	"math"
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/vinizap/lumi/tui-client/theme"
)

// Floating explorer (SPEC-PARTE2 §2): the main and only navigation.
// A centred bordered overlay (~80% × 80%) with yazi Miller columns and
// a preview, or a full-height sheet on small terminals. It opens over
// the notes or over the empty home with `-`, `<leader>e`, `:explore`,
// the palette and the "≡ notes" button in the top line. The same
// overlay has a books level, the Livros shelf (shelf.go), reached with
// `gb` or `h` at the book root.
//
// Inside it the whole explorer keymap works (j/k/h/l, /, a r d y p m .
// R H L ~ …); ⏎ opens the note in the current pane, ⌃v / ⌃s in a
// vertical / horizontal split, Esc / q close it.

// sheetCols / sheetRows: below either the overlay becomes a sheet.
const (
	sheetCols = 100
	sheetRows = 30
)

// isSheet: small terminal, the overlay takes the whole body.
func (m Model) isSheet() bool { return m.width < sheetCols || m.height < sheetRows }

// overlayRect is the settled overlay box (border included).
func (m Model) overlayRect() rect {
	h := m.contentHeight()
	if m.isSheet() {
		return rect{0, 0, m.width, h}
	}
	bw := max(30, m.width*80/100)
	bh := max(8, h*80/100)
	return rect{(m.width - bw) / 2, (h - bh) / 2, bw, bh}
}

// explorerFrame is a copy of m sized to the overlay's inner area, so
// the explorer renders and hit-tests exactly as inside the box.
func (m Model) explorerFrame() (Model, rect) {
	r := m.overlayRect()
	inner := m
	inner.width = max(1, r.w-2)
	// the last inner row is the footer legend
	inner.height = max(1, r.h-3) + m.bottomRows()
	return inner, rect{r.x + 1, r.y + 1, inner.width, max(1, r.h-3)}
}

// openOverlay shows the overlay (files level) and starts its springs.
func (m *Model) openOverlay() {
	wasOpen := m.floatExp
	m.floatExp = true
	m.bk = nil
	if !wasOpen {
		from := math.NaN()
		if a, ok := m.mo.get(animOverlay); ok && a.pos < 0.05 {
			from = 0
		}
		m.mo.to(animOverlay, from, 1)
	}
	m.startReveal()
}

// closeOverlay hides it; it keeps shrinking away on screen while keys
// already go to the view underneath.
func (m *Model) closeOverlay() {
	m.floatExp = false
	m.bk = nil
	m.ctxMenu = nil
	m.filterTyping = false
	m.treePrefix, m.count = "", ""
	m.mo.to(animOverlay, math.NaN(), 0)
}

// flashTitle: the note just opened from the explorer flashes its title
// accent once (DESIGN-LANGUAGE §6.10 row 3, TUI column).
func (m *Model) flashTitle() { m.mo.to(animTitle, 1, 0) }

// startReveal staggers the explorer rows in again.
func (m *Model) startReveal() {
	if m.mo == nil {
		return
	}
	m.mo.revealDir = m.currentDir
	m.mo.to(animReveal, 0, 1)
}

// openFloatExplorer opens the overlay at the focused note's folder
// (or where the explorer last was, on the empty home).
func (m Model) openFloatExplorer() (Model, tea.Cmd) {
	m.syncFocusOut()
	dir := m.currentDir
	if m.ws != nil && m.ws.focus != nil {
		dir = filepath.Dir(m.ws.focus.buf.path)
		m.pendingSelect = m.ws.focus.buf.path
	}
	m.clearFilter()
	m.pane = PaneList
	m.openOverlay()
	cmd := m.navigateTo(dir, 0)
	if cmd == nil {
		cmd = m.loadItems
	}
	return m, cmd
}

// focusedPane is the focused pane pointer (nil without a workspace).
func (m Model) focusedPane() *notePane {
	if m.ws == nil {
		return nil
	}
	return m.ws.focus
}

func (m Model) updateFloatExplorer(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.ctxMenu != nil {
		return m.updateCtxMenuKey(msg.String())
	}
	if m.bk != nil {
		return m.updateBooksLevel(msg)
	}
	key := msg.String()
	busy := m.filterTyping || m.showInput || m.showConfirm || m.showNav || m.treePrefix != "" || m.count != ""
	if !busy {
		switch key {
		case "esc":
			if m.filter != "" {
				m.clearFilter()
				return m, m.loadItems
			}
			m.closeOverlay()
			return m, m.shapeCmd()
		case "q":
			m.closeOverlay()
			return m, m.shapeCmd()
		case "enter", "l", "right", "ctrl+v", "ctrl+s":
			if m.pane == PaneParent {
				break
			}
			it, ok := m.currentItem()
			if ok && !it.IsFolder && it.Note != nil {
				how := map[string]string{"ctrl+v": "vsplit", "ctrl+s": "hsplit"}[key]
				if how == "" || m.ws == nil || m.ws.root == nil {
					how = "replace"
				}
				m.closeOverlay()
				cmd := m.openNoteIn(it.Note, how)
				m.flashTitle()
				return m, tea.Batch(cmd, m.shapeCmd())
			}
			if key == "ctrl+v" || key == "ctrl+s" {
				return m, nil
			}
		case "h", "left", "backspace":
			if m.pane == PaneList && m.currentDir == m.rootDir && m.bookMgr != nil {
				m.openBooksLevel()
				return m, nil
			}
		case "e", "i", "E":
			it, ok := m.currentItem()
			if ok && it.Note != nil && m.pane != PaneParent {
				external := m.noteCfg().DefaultEditor == "external"
				if key == "E" {
					external = !external
				}
				if external {
					return m, m.editCmd(&noteRef{note: it.Note, path: it.Path}, false)
				}
				m.closeOverlay()
				cmd := m.openNoteIn(it.Note, "replace")
				var c2 tea.Cmd
				m, c2 = m.editInternal(m.ws.focus)
				return m, tea.Batch(cmd, c2)
			}
		}
	}
	before := m.focusedPane()
	mm, cmd := m.updateTree(msg)
	nm := mm.(Model)
	switch {
	case nm.viewMode == ViewConfig:
		nm.closeOverlay()
	case nm.focusedPane() != before && nm.focusedPane() != nil:
		// a note was opened from inside (parent column, palette jump)
		nm.closeOverlay()
		nm.flashTitle()
	}
	return nm, cmd
}

// renderFloatExplorer draws the overlay over base. While the overlay
// spring runs, the box grows from (or shrinks to) its centre: the
// content is clipped around the centre and re-bordered.
func (m Model) renderFloatExplorer(base string) string {
	s := 1.0
	if m.mo != nil {
		a, _ := m.mo.get(animOverlay)
		s = a.value(0, 1)
	}
	if s <= 0.02 {
		return base
	}
	r := m.overlayRect()
	iw, ih := max(1, r.w-2), max(1, r.h-2)
	var content string
	if m.bk != nil {
		content = m.renderBooksLevel(iw, max(1, ih-1))
	} else {
		inner, _ := m.explorerFrame()
		inner.floatExp = false
		content = inner.renderTree()
	}
	content = fitHeight(content, max(1, ih-1))
	if ih > 1 {
		foot, _ := m.explorerFooter(iw)
		content += "\n" + foot
	}
	lines := strings.Split(fitHeight(content, ih), "\n")
	cw, ch := iw, ih
	if s < 1 {
		cw = max(1, lerp(min(10, iw), iw, s))
		ch = max(1, lerp(min(2, ih), ih, s))
		top, left := (ih-ch)/2, (iw-cw)/2
		clip := make([]string, ch)
		for i := range clip {
			clip[i] = cutVisual(padVisual(lines[top+i], iw), left, left+cw)
		}
		lines = clip
	}
	for i := range lines {
		lines[i] = padVisual(lines[i], cw)
	}
	border := theme.Current.Accent
	box := lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(border).
		Background(theme.Current.Background).Render(strings.Join(lines, "\n"))
	x := r.x + (iw-cw)/2
	y := r.y + (ih-ch)/2
	out := overlayBox(base, box, x, y, m.width)
	if m.ctxMenu != nil && s >= 1 {
		mb, mr := m.ctxMenuBox()
		out = overlayBox(out, mb, mr.x, mr.y, m.width)
	}
	return out
}

// revealRows is how many list rows the stagger has revealed so far
// (all of them when settled).
func (m Model) revealRows(rows int) int {
	a, ok := m.mo.get(animReveal)
	if !ok || !a.active {
		return rows + 1<<20
	}
	return int(a.value(0, 1)*float64(rows)*1.4) + 1
}
