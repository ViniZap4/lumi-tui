package ui

import tea "github.com/charmbracelet/bubbletea"

// navHistoryCap bounds both history stacks so a long browsing session
// can't grow the model without limit. 50 matches yazi's default feel:
// deep enough that "where was I five folders ago" always works, small
// enough to be free.
const navHistoryCap = 50

// navEntry is one remembered location in the tree browser: the folder
// that was open and which row was selected in it. Comparable so
// pushHistory can drop consecutive duplicates.
type navEntry struct {
	dir    string
	cursor int
}

// pushHistory records the current location on the back stack and
// clears the forward stack. Call it *before* mutating m.currentDir —
// history stores where the user came from, not where they're going.
func (m *Model) pushHistory() {
	entry := navEntry{dir: m.currentDir, cursor: m.cursor}
	if n := len(m.histBack); n > 0 && m.histBack[n-1] == entry {
		// Re-visiting the same spot twice in a row adds no information;
		// skipping it keeps `H` meaning "somewhere else".
		return
	}
	m.histBack = append(m.histBack, entry)
	if len(m.histBack) > navHistoryCap {
		m.histBack = m.histBack[1:]
	}
	m.histFwd = nil
}

// navigateTo pushes the current location onto the back stack and jumps
// the browser to dir with the given cursor. Returns the loadItems Cmd,
// or nil when dir is already the current folder (no-op, no history
// entry — pressing `gh` at the root shouldn't pollute the stack).
func (m *Model) navigateTo(dir string, cursor int) tea.Cmd {
	if dir == m.currentDir {
		return nil
	}
	m.pushHistory()
	m.currentDir = dir
	m.cursor = cursor
	return m.loadItems
}

// historyBack pops the back stack (`H`). The current location moves to
// the forward stack so `L` can undo the jump. Returns nil when there
// is nowhere to go back to.
func (m *Model) historyBack() tea.Cmd {
	n := len(m.histBack)
	if n == 0 {
		return nil
	}
	dest := m.histBack[n-1]
	m.histBack = m.histBack[:n-1]
	m.histFwd = appendCapped(m.histFwd, navEntry{dir: m.currentDir, cursor: m.cursor})
	m.currentDir = dest.dir
	m.cursor = dest.cursor
	return m.loadItems
}

// historyForward pops the forward stack (`L`), the mirror of
// historyBack. Returns nil when there is nowhere to go forward to.
func (m *Model) historyForward() tea.Cmd {
	n := len(m.histFwd)
	if n == 0 {
		return nil
	}
	dest := m.histFwd[n-1]
	m.histFwd = m.histFwd[:n-1]
	m.histBack = appendCapped(m.histBack, navEntry{dir: m.currentDir, cursor: m.cursor})
	m.currentDir = dest.dir
	m.cursor = dest.cursor
	return m.loadItems
}

// appendCapped appends entry and drops the oldest element once the
// stack exceeds navHistoryCap.
func appendCapped(stack []navEntry, entry navEntry) []navEntry {
	stack = append(stack, entry)
	if len(stack) > navHistoryCap {
		stack = stack[1:]
	}
	return stack
}
