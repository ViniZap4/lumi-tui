package ui

import (
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/vinizap/lumi/tui-client/filesystem"
)

// updateTreeNavModal handles keys when the navigation modal is open from tree view (for move).
func (m Model) updateTreeNavModal(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.showNav = false
		m.pendingMoveNote = nil
		m.pendingMoveFolder = ""
		return m, nil
	case "j", "down":
		if m.navCursor < len(m.navItems)-1 {
			m.navCursor++
		}
	case "k", "up":
		if m.navCursor > 0 {
			m.navCursor--
		}
	case "h":
		if m.navDir != m.rootDir {
			m.navDir = filepath.Dir(m.navDir)
			m.navCursor = 0
			return m, m.loadNavItems
		}
	case "l":
		if m.navCursor < len(m.navItems) {
			item := m.navItems[m.navCursor]
			if item.IsFolder {
				m.navDir = item.Path
				m.navCursor = 0
				return m, m.loadNavItems
			}
		}
	case "enter":
		destDir := m.navDir
		if m.navCursor < len(m.navItems) {
			item := m.navItems[m.navCursor]
			if item.IsFolder {
				destDir = item.Path
			}
		}

		if m.pendingMoveNote != nil {
			filesystem.MoveNote(m.pendingMoveNote, destDir)
			m.pendingMoveNote = nil
			m.showNav = false
			return m, m.loadItems
		}
		if m.pendingMoveFolder != "" {
			if strings.HasPrefix(destDir+string(filepath.Separator), m.pendingMoveFolder+string(filepath.Separator)) {
				return m, nil
			}
			filesystem.MoveFolder(m.pendingMoveFolder, destDir)
			m.pendingMoveFolder = ""
			m.showNav = false
			return m, m.loadItems
		}
	case "g":
		m.navCursor = 0
	case "G":
		if len(m.navItems) > 0 {
			m.navCursor = len(m.navItems) - 1
		}
	}

	return m, nil
}

func (m Model) updateTreeSearch(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.showSearch = false
		m.searchQuery = ""
		m.cursor = 0
		return m, nil
	case "ctrl+f":
		if m.searchType == "filename" {
			m.searchType = "content"
		} else {
			m.searchType = "filename"
		}
		return m, m.dispatchSearch()
	case "down":
		if m.cursor < len(m.searchResults)-1 {
			m.cursor++
		}
	case "up":
		if m.cursor > 0 {
			m.cursor--
		}
	case "enter":
		if m.cursor < len(m.searchResults) && m.searchResults[m.cursor].Note != nil {
			m.openNote(m.searchResults[m.cursor].Note)
			m.showSearch = false
			m.cursor = 0
		}
	case "backspace":
		if len(m.searchQuery) > 0 {
			m.searchQuery = m.searchQuery[:len(m.searchQuery)-1]
			return m, m.dispatchSearch()
		}
	default:
		if len(msg.String()) == 1 && msg.String() >= " " && msg.String() <= "~" {
			m.searchQuery += msg.String()
			return m, m.dispatchSearch()
		}
	}

	return m, nil
}
