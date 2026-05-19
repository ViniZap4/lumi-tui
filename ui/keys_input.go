package ui

import (
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/vinizap/lumi/tui-client/filesystem"
)

// cancelEmptyInput closes an input modal that was submitted with an
// empty value and emits a toast so the user knows nothing happened.
// Previously the modal closed silently — users frequently assumed
// they'd just created or renamed something when they hadn't.
func (m *Model) cancelEmptyInput(field string) tea.Cmd {
	m.showInput = false
	m.inputValue = ""
	return m.showToast("Cancelled — "+field+" required", ToastInfo, 2*time.Second)
}

func (m Model) updateConfirm(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "y", "enter":
		if m.pendingDeleteFolder != "" {
			if err := filesystem.DeleteFolder(m.pendingDeleteFolder); err == nil {
				m.showConfirm = false
				m.pendingDeleteFolder = ""
				m.confirmMsg = ""
				return m, m.loadItems
			}
		} else if m.pendingDeleteNote != nil {
			if err := filesystem.DeleteNote(m.pendingDeleteNote); err == nil {
				m.showConfirm = false
				m.pendingDeleteNote = nil
				m.confirmMsg = ""
				return m, m.loadItems
			}
		}
		m.showConfirm = false
		m.pendingDeleteNote = nil
		m.pendingDeleteFolder = ""
		m.confirmMsg = ""
		return m, nil
	case "n", "esc":
		m.showConfirm = false
		m.pendingDeleteNote = nil
		m.pendingDeleteFolder = ""
		m.confirmMsg = ""
		return m, nil
	}
	return m, nil
}

func (m Model) updateInput(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.showInput = false
		m.inputValue = ""
		return m, nil

	case "enter":
		switch m.inputMode {
		case "create":
			if m.inputValue == "" {
				return m, m.cancelEmptyInput("title")
			}
			if _, err := filesystem.CreateNote(m.currentDir, m.inputValue); err == nil {
				m.showInput = false
				m.inputValue = ""
				return m, m.loadItems
			}
		case "create_folder":
			if m.inputValue == "" {
				return m, m.cancelEmptyInput("folder name")
			}
			if err := filesystem.CreateFolder(m.currentDir, m.inputValue); err == nil {
				m.showInput = false
				m.inputValue = ""
				return m, m.loadItems
			}
		case "rename":
			if m.inputValue == "" {
				return m, m.cancelEmptyInput("new title")
			}
			if m.cursor < len(m.items) && m.items[m.cursor].Note != nil {
				if err := filesystem.RenameNote(m.items[m.cursor].Note, m.inputValue); err == nil {
					m.showInput = false
					m.inputValue = ""
					return m, m.loadItems
				}
			}
		case "rename_folder":
			if m.inputValue == "" {
				return m, m.cancelEmptyInput("new folder name")
			}
			if m.cursor < len(m.items) && m.items[m.cursor].IsFolder {
				if err := filesystem.RenameFolder(m.items[m.cursor].Path, m.inputValue); err == nil {
					m.showInput = false
					m.inputValue = ""
					return m, m.loadItems
				}
			}
		case "config_server_url", "config_server_token":
			key := m.inputMode[len("config_"):]
			m.applyConfigChange(key, m.inputValue)
			m.showInput = false
			m.inputValue = ""
			return m, nil
		}
		m.showInput = false
		m.inputValue = ""
		return m, nil

	case "backspace":
		if len(m.inputValue) > 0 {
			m.inputValue = m.inputValue[:len(m.inputValue)-1]
		}
		return m, nil

	default:
		if len(msg.String()) == 1 && msg.String() >= " " && msg.String() <= "~" {
			m.inputValue += msg.String()
		}
		return m, nil
	}
}
