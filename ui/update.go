package ui

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/vinizap/lumi/tui-client/filesystem"
)

// animTickMsg drives the home screen animation.
type animTickMsg time.Time

// yankFlashMsg clears the yank highlight after a brief delay.
type yankFlashMsg time.Time

func animTick() tea.Cmd {
	return tea.Tick(60*time.Millisecond, func(t time.Time) tea.Msg {
		return animTickMsg(t)
	})
}

func (m Model) Init() tea.Cmd {
	cmds := []tea.Cmd{m.loadItems, animTick()}
	if m.syncClient != nil {
		m.syncClient.Start()
		cmds = append(cmds, m.waitForSyncEvent, m.waitForSyncStatus)
	}
	return tea.Batch(cmds...)
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		if m.fullNote != nil {
			m.renderMarkdown()
		}
		return m, nil

	case yankFlashMsg:
		m.yankHighlight = false
		return m, nil

	case animTickMsg:
		if m.viewMode == ViewHome && !m.animDone {
			m.animCol += 5
			if m.animCol >= logoMaxRunes()+logoStagger() {
				m.animCol = logoMaxRunes() + logoStagger()
				m.animDone = true
				return m, nil
			}
			return m, animTick()
		}
		return m, nil

	case itemsLoadedMsg:
		m.items = msg.items
		// One-shot auto-open: if the user passed `lumi some/note.md`
		// at the command line, find the matching item and jump into
		// its Note view. Falls back to a direct disk read if for some
		// reason the file didn't make it into the listing.
		if m.initialNotePath != "" {
			target := m.initialNotePath
			m.initialNotePath = ""
			for i, it := range m.items {
				if it.IsFolder || it.Note == nil {
					continue
				}
				if it.Path == target {
					m.cursor = i
					m.openNote(it.Note)
					return m, nil
				}
			}
			// Listing didn't include the target (e.g. the file lives
			// outside m.currentDir). Read it directly.
			if note, err := filesystem.ReadNote(target); err == nil && note != nil {
				m.openNote(note)
			}
		}
		return m, nil

	case navItemsLoadedMsg:
		m.navItems = msg.items
		return m, nil

	case searchResultsMsg:
		m.searchResults = msg.results
		m.cursor = 0
		return m, nil

	case editorDoneMsg:
		// Re-read note from disk after editor exits, then refresh items.
		reloaded, err := filesystem.ReadNote(msg.notePath)
		if err == nil {
			m.openNote(reloaded)
		}
		return m, m.loadItems

	case toastDismissMsg:
		if msg.id == m.toastID {
			m.toastMsg = ""
		}
		return m, nil

	case syncConnectedMsg:
		cmd := m.showToast("Server connected", ToastSuccess, 3*time.Second)
		return m, tea.Batch(cmd, m.waitForSyncStatus)

	case syncErrorMsg:
		toastText := classifySyncError(msg.err)
		cmd := m.showToast(toastText, ToastError, 4*time.Second)
		return m, tea.Batch(cmd, m.waitForSyncStatus)

	case syncDroppedMsg:
		// Some sync events were dropped — prompt the user to refresh so
		// the on-screen list isn't silently stale. We also do an
		// implicit reload here because users rarely act on warnings.
		toast := fmt.Sprintf("Sync: %d event(s) lost — refreshing", msg.count)
		cmd := m.showToast(toast, ToastError, 4*time.Second)
		return m, tea.Batch(cmd, m.loadItems, m.waitForSyncStatus)

	case syncEventMsg:
		// A note was changed on the server — reload items.
		// If viewing a note that was updated, re-read it.
		if m.fullNote != nil && msg.event.Type == "note_updated" {
			reloaded, err := filesystem.ReadNote(m.fullNote.Path)
			if err == nil {
				m.openNote(reloaded)
			}
		}
		cmds := []tea.Cmd{m.loadItems, m.waitForSyncEvent}
		// Also refresh nav modal items if it's open
		if m.showNav {
			cmds = append(cmds, m.loadNavItems)
		}
		return m, tea.Batch(cmds...)

	case tea.KeyMsg:
		if m.showConfirm {
			return m.updateConfirm(msg)
		}
		if m.showInput {
			return m.updateInput(msg)
		}

		switch m.viewMode {
		case ViewHome:
			return m.updateHome(msg)
		case ViewTree:
			return m.updateTree(msg)
		case ViewFullNote:
			return m.updateNote(msg)
		case ViewConfig:
			return m.updateConfig(msg)
		}
	}

	return m, nil
}

// classifySyncError returns a user-friendly toast message for sync errors.
func classifySyncError(err error) string {
	if err == nil {
		return "Sync: unknown error"
	}
	msg := err.Error()
	switch {
	case strings.Contains(msg, "connection refused"):
		return "Sync: connection refused"
	case strings.Contains(msg, "no such host"):
		return "Sync: server not found"
	case strings.Contains(msg, "401") || strings.Contains(msg, "403"):
		return "Sync: auth failed"
	case strings.Contains(msg, "timeout") || strings.Contains(msg, "deadline exceeded"):
		return "Sync: timeout"
	case strings.Contains(msg, "EOF") || strings.Contains(msg, "connection reset"):
		return "Sync: connection lost"
	default:
		text := "Sync: " + msg
		if len(text) > 45 {
			text = text[:42] + "..."
		}
		return text
	}
}
