package ui

import (
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
	if m.bookMgr != nil {
		cmds = append(cmds, m.waitForBooks)
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
		// Clamp the cursor: a history jump (`H`/`L`) restores the
		// selection index that was valid when the folder was last
		// visited, but the folder may have shrunk since.
		if m.cursor >= len(m.items) {
			m.cursor = len(m.items) - 1
			if m.cursor < 0 {
				m.cursor = 0
			}
		}
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
		// Drop stale results: a slow walk for an earlier query must not
		// overwrite the fresh result for the current one. dispatchSearch
		// bumps searchGen on every keystroke; results carry the gen
		// they were dispatched at.
		if msg.gen != m.searchGen {
			return m, nil
		}
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

	case booksChangedMsg:
		return m.onBooksChanged()

	case tea.KeyMsg:
		if m.showBooks {
			return m.updateBooksPicker(msg)
		}
		if m.showBookDetails {
			m.showBookDetails = false
			return m, nil
		}
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
