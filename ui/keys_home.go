package ui

import (
	tea "github.com/charmbracelet/bubbletea"
)

// updateHome handles keys on the empty home (the floating explorer is
// closed): pick a recent note, open the explorer, books, search.
func (m Model) updateHome(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()
	if m.notePrefix != "" {
		pre := m.notePrefix
		m.notePrefix = ""
		switch pre + key {
		case "gg":
			m.homeCursor = 0
			return m, nil
		case "gb":
			m.openBooksLevel()
			return m, nil
		case "gs":
			m.showBookDetails = m.bookMgr != nil
			return m, nil
		case "ga":
			m.openAccounts()
			return m, nil
		case `\e`:
			return m.openFloatExplorer()
		}
		if pre == "g" && len(key) == 1 && key[0] >= '1' && key[0] <= '9' {
			return m.switchToBook(int(key[0] - '1'))
		}
		if key == "esc" {
			return m, nil
		}
	}
	switch key {
	case "q", "ctrl+c":
		return m, tea.Quit
	case "-", "enter", "l":
		if key != "-" && m.homeCursor < len(m.recent) {
			return m.openRecent(m.homeCursor)
		}
		return m.openFloatExplorer()
	case "j", "down":
		m.homeCursor = clamp(m.homeCursor+1, 0, max(0, len(m.recent)-1))
	case "k", "up":
		m.homeCursor = clamp(m.homeCursor-1, 0, max(0, len(m.recent)-1))
	case "G":
		m.homeCursor = max(0, len(m.recent)-1)
	case "g", `\`:
		m.notePrefix = key
	case "/":
		m.showSearch, m.searchQuery, m.searchType, m.searchCursor = true, "", "content", 0
		return m, m.dispatchSearch()
	default:
		if len(key) == 1 && key[0] >= '1' && key[0] <= '9' {
			if i := int(key[0] - '1'); i < len(m.recent) {
				return m.openRecent(i)
			}
			return m, nil
		}
		if mm, cmd, ok := m.globalKey(key); ok {
			return mm, cmd
		}
	}
	return m, nil
}

// openRecent opens the i-th recent note.
func (m Model) openRecent(i int) (tea.Model, tea.Cmd) {
	if i < 0 || i >= len(m.recent) || m.recent[i].Note == nil {
		return m, nil
	}
	m.openNote(m.recent[i].Note)
	return m, m.shapeCmd()
}
