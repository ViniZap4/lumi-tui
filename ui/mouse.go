package ui

import (
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// Mouse (keyboard-first, mouse-friendly): click selects and focuses a
// pane, double-click opens, the wheel scrolls whatever is under the
// pointer. Clicking the status line's book segment opens `gs`.

const doubleClickWindow = 400 * time.Millisecond

// treeTop is the first body row of the explorer columns (header + rule).
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
	case m.showBooks, m.showConfirm, m.showInput, m.showNav, m.showSearch, m.acct != nil:
		return m, nil
	case m.hover != nil:
		if press {
			m.hover = nil
		} else if wheel != 0 {
			m.hover.scroll = max(0, m.hover.scroll+wheel)
		}
		return m, nil
	case m.floatExp && m.viewMode == ViewFullNote:
		// click outside the overlay closes it; wheel moves the list
		bw, bh := m.width*80/100, m.contentHeight()*80/100
		x0, y0 := (m.width-bw)/2, (m.contentHeight()-bh)/2
		if press && (msg.X < x0 || msg.X >= x0+bw || msg.Y < y0 || msg.Y >= y0+bh) {
			m.floatExp = false
		} else if wheel != 0 {
			m.moveCursor(wheel)
		}
		return m, nil
	case m.showBookDetails:
		if press {
			m.showBookDetails = false
		}
		return m, nil
	}

	// Status line: the right side holds the book segment.
	if press && m.statusVisible() && msg.Y == m.height-1 && msg.X > m.width/2 && m.bookMgr != nil {
		m.showBookDetails = true
		return m, nil
	}
	if msg.Y >= m.contentHeight() {
		return m, nil
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
	case ViewHome:
		if press {
			m.viewMode = ViewTree
		}
		return m, nil
	}

	// Explorer.
	pane, ok := m.columnAt(msg.X)
	if !ok {
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

// updateNoteMouse: wheel scrolls the pane under the pointer, a click
// focuses it (and places the editor cursor), a double-click follows a
// link; Ctrl-click opens a link in a vertical split, Alt-click in a
// horizontal one (Shift-click is usually taken by the terminal).
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
	if press && hit != m.ws.focus {
		m.setFocus(hit)
	}
	p := hit
	switch {
	case wheel != 0:
		switch p.view {
		case viewReader:
			p.scroll = max(0, p.scroll+wheel*3)
		case viewSource, viewSplit:
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
	case !press:
		return m, nil
	}
	row := msg.Y - r.y - 1
	if row < 0 {
		return m, nil
	}
	how := "replace"
	if msg.Ctrl {
		how = "vsplit"
	} else if msg.Alt {
		how = "hsplit"
	}
	dbl := m.isDoubleClick(msg.X, msg.Y)
	switch p.view {
	case viewSource, viewSplit:
		e := p.buf.eng
		line := e.Top + row
		if line < e.LineCount() {
			e.SetCursor(vimPos(line, max(0, msg.X-r.x-1)))
		}
		if dbl || how != "replace" {
			if l, ok := m.linkUnderCursor(p); ok {
				return m.followLink(p, l, how)
			}
		}
	case viewReader:
		_, w, _ := m.paneRect(p)
		_, pos := m.readerLines(p, w)
		line := p.scroll + row
		col := msg.X - r.x - 2
		for i, lp := range pos {
			if lp.line == line && col >= lp.col && col < lp.col+lp.w {
				p.link = i
				if dbl || how != "replace" {
					links := findLinks(p.buf.eng.Text())
					return m.followLink(p, links[i], how)
				}
			}
		}
	case viewCanvas:
		p.crow = row
		if dbl || how != "replace" {
			return m.openCanvasRow(p, how)
		}
	}
	return m, nil
}
