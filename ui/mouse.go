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
	case m.showBooks, m.showConfirm, m.showInput, m.showNav, m.showSearch:
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
		if wheel != 0 {
			if wheel > 0 {
				m.moveDown(3)
			} else {
				m.moveUp(3)
			}
			return m, nil
		}
		if press && msg.Y >= 2 {
			total := len(m.contentLines)
			line := m.scrollOffset(total) + msg.Y - 2
			if line >= 0 && line < total {
				m.lineCursor = line
				m.colCursor = max(0, msg.X-2)
				m.desiredCol = m.colCursor
				m.clampCursor()
				if m.isDoubleClick(msg.X, msg.Y) {
					return m, m.followLinkAtCursor()
				}
			}
		}
		return m, nil
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
