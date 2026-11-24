package ui

import (
	"fmt"
	"time"

	"github.com/vinizap/lumi/tui-client/vim"

	tea "github.com/charmbracelet/bubbletea"
)

// Mouse (keyboard-first, mouse-friendly): click selects and focuses a
// pane, double-click opens, the wheel scrolls whatever is under the
// pointer. The "≡ notes" button (top-left) opens the floating explorer;
// a click outside the overlay closes it. Clicking the status line's
// book segment opens `gs`.

const doubleClickWindow = 400 * time.Millisecond

// treeTop is the first row of the explorer columns inside the overlay
// (header + rule).
const treeTop = 2

// listWindow is the first visible list index and the number of rows,
// the same arithmetic renderCenterCol uses.
func listWindow(cursor, n, height int) (start, rows int) {
	overflow := n > height-1
	rows = height - 1
	if overflow {
		rows--
	}
	if rows < 1 {
		rows = 1
	}
	if cursor >= rows {
		start = cursor - rows + 1
	}
	return start, rows
}

// columnAt maps an x coordinate to the explorer pane under it.
func (m Model) columnAt(x int) (Pane, bool) {
	left, center, right, sep := treeColumnWidths(m.width, m.previewHidden)
	if left > 0 {
		if x < left {
			return PaneParent, true
		}
		x -= left + sep
	}
	if x >= 0 && x < center {
		return PaneList, true
	}
	if right > 0 && x >= center+sep {
		return PanePreview, true
	}
	return PaneList, false
}

func (m *Model) isDoubleClick(x, y int) bool {
	now := nowFn()
	dbl := !m.lastClick.at.IsZero() && now.Sub(m.lastClick.at) <= doubleClickWindow &&
		m.lastClick.x == x && m.lastClick.y == y
	if dbl {
		m.lastClick = clickInfo{}
	} else {
		m.lastClick = clickInfo{at: now, x: x, y: y}
	}
	return dbl
}

func (m Model) updateMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	if !m.mouseOn {
		return m, nil
	}
	wheel := 0
	switch msg.Button {
	case tea.MouseButtonWheelDown:
		wheel = 1
	case tea.MouseButtonWheelUp:
		wheel = -1
	}
	press := msg.Action == tea.MouseActionPress && msg.Button == tea.MouseButtonLeft

	switch {
	case m.showPalette:
		if wheel != 0 {
			if n := len(m.paletteResults()); n > 0 {
				m.paletteCursor = clamp(m.paletteCursor+wheel, 0, n-1)
			}
		}
		return m, nil
	case m.showHelp:
		if wheel != 0 {
			m.helpScroll = max(0, m.helpScroll+wheel*3)
		}
		return m, nil
	case m.acct != nil:
		return m.accountsMouse(msg, wheel, press)
	case m.clone != nil:
		return m.cloneMouse(msg)
	case m.showConfirm, m.showInput, m.showNav, m.showSearch:
		return m, nil
	case m.hover != nil:
		if press {
			m.hover = nil
		} else if wheel != 0 {
			m.hover.scroll = max(0, m.hover.scroll+wheel)
		}
		return m, nil
	case m.showBookDetails:
		if press {
			m.showBookDetails = false
		}
		return m, nil
	case m.floatExp && m.viewMode != ViewConfig:
		if m.ctxMenu != nil {
			return m.ctxMenuMouse(msg)
		}
		// click outside the overlay closes it; inside, the explorer
		r := m.overlayRect()
		// the footer legend: a click presses the item's key
		if press && msg.Y == r.y+r.h-2 && msg.X > r.x && msg.X < r.x+r.w-1 {
			if mm, cmd, ok := m.footerClick(msg.X-r.x-1, r.w-2); ok {
				return mm, cmd
			}
			return m, nil
		}
		if press && (msg.X < r.x || msg.X >= r.x+r.w || msg.Y < r.y || msg.Y >= r.y+r.h) {
			m.closeOverlay()
			return m, m.shapeCmd()
		}
		if m.bk != nil {
			if wheel != 0 {
				return m.updateBooksLevel(keyRunes(map[int]string{1: "j", -1: "k"}[wheel]))
			}
			if press {
				return m.booksLevelMouse(msg.X-r.x-1, msg.Y-r.y-1, r.w-2)
			}
			return m, nil
		}
		inner, off := m.explorerFrame()
		if msg.Button == tea.MouseButtonRight && msg.Action == tea.MouseActionPress {
			// right-click: select the row, then the context menu
			sx, sy := msg.X, msg.Y
			msg.X -= off.x
			msg.Y -= off.y
			lm := msg
			lm.Button = tea.MouseButtonLeft
			tm, _ := inner.updateExplorerMouse(lm, 0, true)
			nm := tm.(Model)
			nm.width, nm.height = m.width, m.height
			nm.lastClick = clickInfo{}
			if it, ok := nm.currentItem(); ok && !it.IsFolder && it.Note != nil {
				nm.ctxMenu = &ctxMenu{x: sx, y: sy}
			}
			return nm, nil
		}
		msg.X -= off.x
		msg.Y -= off.y
		before := m.focusedPane()
		tm, cmd := inner.updateExplorerMouse(msg, wheel, press)
		nm := tm.(Model)
		nm.width, nm.height = m.width, m.height
		if nm.focusedPane() != before && nm.focusedPane() != nil {
			nm.closeOverlay() // double-click opened a note
		}
		return nm, cmd
	}

	// Status line: the right side holds the book segment.
	if press && m.statusVisible() && msg.Y == m.height-1 && msg.X > m.width/2 && m.bookMgr != nil {
		m.showBookDetails = true
		return m, nil
	}
	if msg.Y >= m.contentHeight() {
		return m, nil
	}
	// The "≡ notes" button in the top line opens the explorer.
	if press && msg.Y == 0 && msg.X < notesButtonWidth && m.viewMode != ViewConfig {
		return m.openFloatExplorer()
	}

	switch m.viewMode {
	case ViewFullNote:
		return m.updateNoteMouse(msg, wheel, press)
	case ViewConfig:
		if wheel > 0 {
			m.configMoveDown()
		} else if wheel < 0 {
			m.configMoveUp()
		}
		return m, nil
	}
	// empty home: wheel moves, click selects, double-click opens
	if wheel != 0 {
		m.homeCursor = clamp(m.homeCursor+wheel, 0, max(0, len(m.recent)-1))
		return m, nil
	}
	if press {
		if i := m.homeRowAt(msg.Y); i >= 0 {
			dbl := m.isDoubleClick(msg.X, msg.Y)
			m.homeCursor = i
			if dbl {
				return m.openRecent(i)
			}
		}
	}
	return m, nil
}

// updateExplorerMouse handles the explorer columns; coordinates are
// relative to the explorer's area (m sized to it, see explorerFrame).
func (m Model) updateExplorerMouse(msg tea.MouseMsg, wheel int, press bool) (tea.Model, tea.Cmd) {
	pane, ok := m.columnAt(msg.X)
	if !ok || msg.X < 0 || msg.Y < 0 {
		return m, nil
	}
	colH := m.contentHeight() - treeTop
	row := msg.Y - treeTop
	if wheel != 0 {
		switch pane {
		case PanePreview:
			m.previewScroll = max(0, m.previewScroll+wheel*3)
		case PaneList:
			m.moveCursor(wheel)
		case PaneParent:
			if items := m.parentItems(); len(items) > 0 {
				m.parentCursor = clamp(m.parentCursor+wheel, 0, len(items)-1)
				m.pane = PaneParent
			}
		}
		return m, nil
	}
	if !press || row < 0 {
		return m, nil
	}
	dbl := m.isDoubleClick(msg.X, msg.Y)
	switch pane {
	case PaneList:
		start, rows := listWindow(m.cursor, len(m.items), colH)
		if row >= rows {
			return m, nil
		}
		i := start + row
		if i >= len(m.items) {
			return m, nil
		}
		// the selected row's inline actions: open · ⇥ right · ⇩ below
		if i == m.cursor && m.pane == PaneList {
			_, cw, _, _ := treeColumnWidths(m.width, m.previewHidden)
			if a := rowActionAt(max(4, cw-1), msg.X-m.centerColX()); a >= 0 {
				if it, ok := m.currentItem(); ok && it.Note != nil {
					return m.openItemHow(rowActions[a].how)
				}
			}
		}
		m.pane = PaneList
		if m.cursor != i {
			m.cursor = i
			m.previewScroll = 0
		}
		if dbl {
			return m.openItem()
		}
	case PaneParent:
		items := m.parentItems()
		start, rows := listWindow(m.parentSel(items), len(items), colH)
		if row >= rows || start+row >= len(items) {
			return m, nil
		}
		m.pane = PaneParent
		m.parentCursor = start + row
		if dbl {
			mm, cmd, _ := m.updateParentPane("l")
			return mm, cmd
		}
	case PanePreview:
		m.pane = PanePreview
		if dbl {
			m.pane = PaneList
			return m.openItem()
		}
	}
	return m, nil
}

// updateNoteMouse: the wheel scrolls the pane under the pointer. A
// click focuses the pane and performs the click in the same press (no
// dead first click): it places the cursor (editor, reader, preview);
// on a link in the reader or preview it follows it. A double-click
// follows a link in the editor. Ctrl-click opens a link in a vertical
// split, Alt-click in a horizontal one (Shift-click is usually taken
// by the terminal).
func (m Model) updateNoteMouse(msg tea.MouseMsg, wheel int, press bool) (tea.Model, tea.Cmd) {
	if m.ws == nil {
		return m, nil
	}
	var hit *notePane
	var r rect
	for p, pr := range m.layoutRects() {
		if msg.X >= pr.x && msg.X < pr.x+pr.w && msg.Y >= pr.y && msg.Y < pr.y+pr.h {
			hit, r = p, pr
		}
	}
	if hit == nil {
		return m, nil
	}
	p := hit
	if wheel != 0 {
		switch p.view {
		case viewReader:
			d, h := m.readerDoc(p)
			d.scrollBy(wheel*3, h)
			p.scroll = d.eng.Top
		case viewSource, viewSplit:
			if p.view == viewSplit && msg.X > r.x+r.w/2 {
				d, h := m.previewDoc(p)
				if p != m.ws.focus || !p.previewFocus {
					// scrolling the unfocused preview reads it
					m.setFocus(p)
					p.previewFocus = true
				}
				d.scrollBy(wheel*3, h)
				return m, nil
			}
			if p == m.ws.focus {
				e := p.buf.eng
				e.Top = clamp(e.Top+wheel*3, 0, max(0, e.LineCount()-1))
				c := e.Cursor()
				if c.Line < e.Top {
					e.SetCursor(vimPos(e.Top, c.Col))
				}
			}
		case viewCanvas:
			p.crow = max(0, p.crow+wheel)
		case viewBoard:
			p.bcard = max(0, p.bcard+wheel)
		}
		return m, nil
	}
	if !press {
		return m, nil
	}
	row := msg.Y - r.y - 1
	if row < 0 {
		// the pane header: double-click (or its zoom button) zooms
		if mm, cmd, ok := m.headerClick(p, r, msg); ok {
			return mm, cmd
		}
		m.setFocus(p)
		return m, nil
	}
	m.setFocus(p)
	how := "replace"
	if msg.Ctrl {
		how = "vsplit"
	} else if msg.Alt {
		how = "hsplit"
	}
	dbl := m.isDoubleClick(msg.X, msg.Y)
	switch p.view {
	case viewSource, viewSplit:
		lw := r.w
		if p.view == viewSplit {
			lw = r.w / 2
			if msg.X > r.x+lw {
				p.previewFocus = true
				d, _ := m.previewDoc(p)
				return m.clickProse(p, d, row, msg.X-(r.x+lw+1)-previewIndent, how)
			}
			p.previewFocus = false
		}
		e := p.buf.eng
		gutter := 0
		if e.Opts.Number || e.Opts.RelativeNumber {
			gutter = len(fmt.Sprint(e.LineCount())) + 2
		}
		line := e.Top + row
		if line < e.LineCount() {
			l := e.Lines()[line]
			e.SetCursor(vimPos(line, vim.ColForDisplay(l, max(0, msg.X-r.x-1-gutter), e.Opts.TabStop, false)))
		}
		if dbl || how != "replace" {
			if l, ok := m.linkUnderCursor(p); ok {
				return m.followLink(p, l, how)
			}
		}
	case viewReader:
		d, _ := m.readerDoc(p)
		return m.clickProse(p, d, row, msg.X-r.x-readerIndent, how)
	case viewCanvas:
		p.crow = row
		if dbl || how != "replace" {
			return m.openCanvasRow(p, how)
		}
	}
	return m, nil
}

// clickProse places the read-only cursor at (row, col) of the visible
// prose; a click on a link follows it.
func (m Model) clickProse(p *notePane, d *readerDoc, row, col int, how string) (tea.Model, tea.Cmd) {
	line := d.eng.Top + row
	if line >= len(d.plain) || col < 0 {
		return m, nil
	}
	if d.eng.Mode().IsVisual() {
		d.eng.Feed("esc")
	}
	if i := d.linkAt(line, col); i >= 0 {
		d.eng.SetCursor(vim.Pos{Line: line, Col: colAt(d.plain[line], col)})
		p.link = i
		return m.followLink(p, d.links[i], how)
	}
	top := d.eng.Top
	d.eng.SetCursor(vim.Pos{Line: line, Col: colAt(d.plain[line], col)})
	d.eng.Top = top
	return m, nil
}
