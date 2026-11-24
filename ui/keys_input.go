package ui

import (
	"path/filepath"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/vinizap/lumi/tui-client/filesystem"
)

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
	case "esc", "ctrl+c":
		m.showInput = false
		m.inputValue = ""
		return m, nil
	case "enter":
		mode, value := m.inputMode, m.inputValue
		m.showInput = false
		m.inputValue = ""
		return m.submitInput(mode, value)
	}
	if v, ok := editLine(m.inputValue, msg); ok {
		m.inputValue = v
	}
	return m, nil
}

func (m Model) inputErr(err string) (tea.Model, tea.Cmd) {
	return m, m.showToast(err, ToastError, 4*time.Second)
}

// submitInput runs a prompt (a / r / settings) through the plans in
// fileops.go; errors keep everything as it was and show a toast.
func (m Model) submitInput(mode, value string) (tea.Model, tea.Cmd) {
	switch mode {
	case "find":
		if m.ws != nil && m.ws.focus != nil {
			p := m.ws.focus
			p.find = strings.TrimSpace(value)
			if p.find != "" {
				_, w, _ := m.paneRect(p)
				lines, _ := m.readerLines(p, w)
				p.scroll--
				m.findInReader(p, lines, 1)
			}
		}
		return m, nil
	case "create":
		if strings.TrimSpace(value) == "" {
			return m, m.showToast("Cancelled — name required", ToastInfo, 2*time.Second)
		}
		p := planCreate(m.relOf(m.currentDir), value)
		if p.kind == planError {
			return m.inputErr(p.err)
		}
		dir, sel, err := m.applyCreate(p)
		if err != nil {
			return m.inputErr(err.Error())
		}
		m.clearFilter()
		cmd := m.navigateTo(dir, 0)
		if cmd == nil {
			cmd = m.loadItems
		}
		m.pendingSelect = sel
		return m, cmd
	case "rename":
		it, ok := m.currentItem()
		if !ok || it.Note == nil {
			return m, nil
		}
		p := planRename(m.relOf(it.Path), it.Note.Title, value)
		switch p.kind {
		case planError:
			return m.inputErr(p.err)
		case planNone:
			return m, nil
		}
		newPath, err := m.applyRename(it.Note, p)
		if err != nil {
			return m.inputErr(err.Error())
		}
		m.localEdit()
		if dir := filepath.Dir(newPath); dir != m.currentDir {
			m.clearFilter()
			cmd := m.navigateTo(dir, 0)
			m.pendingSelect = newPath
			return m, cmd
		}
		m.pendingSelect = newPath
		return m, m.loadItems
	case "rename_folder":
		it, ok := m.currentItem()
		if !ok || !it.IsFolder {
			return m, nil
		}
		p := planFolderRename(m.relOf(it.Path), value)
		switch p.kind {
		case planError:
			return m.inputErr(p.err)
		case planNone:
			return m, nil
		}
		to, err := m.applyFolderRename(p)
		if err != nil {
			return m.inputErr(err.Error())
		}
		if dir := filepath.Dir(to); dir != m.currentDir {
			m.clearFilter()
			cmd := m.navigateTo(dir, 0)
			m.pendingSelect = to
			return m, cmd
		}
		m.pendingSelect = to
		return m, m.loadItems
	case "config_escape":
		m.applyConfigChange("escape", value)
		m.configItems = m.buildConfigItems(m.configCfg)
		return m, nil
	case "config_editor":
		v := strings.TrimSpace(value)
		if v == "" {
			return m, nil
		}
		m.applyConfigChange("editor", v)
		m.configItems = m.buildConfigItems(m.configCfg)
		return m, nil
	}
	return m, nil
}
