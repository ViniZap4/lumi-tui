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
	if m.mouseOn {
		cmds = append(cmds, tea.EnableMouseCellMotion)
	}
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
		if msg.dir != "" && msg.dir != m.currentDir {
			return m, nil // a listing for a folder we already left
		}
		m.allItems = msg.items
		if m.filter != "" {
			m.applyFilter()
		} else {
			m.items = msg.items
		}
		if m.pendingSelect != "" {
			m.selectPath(m.pendingSelect)
			m.pendingSelect = ""
		}
		if m.pane == PaneParent && m.currentDir == m.rootDir {
			m.pane = PaneList
		}
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
		// The feed manager pushes the edit (3-way, CRDT §7) right away.
		cmds := []tea.Cmd{m.loadItems}
		if m.mouseOn {
			cmds = append(cmds, tea.EnableMouseCellMotion)
		}
		if msg.err != nil {
			cmds = append(cmds, m.showToast("editor: "+msg.err.Error(), ToastError, 4*time.Second))
		}
		m.reloadBuffer(msg.notePath)
		if m.ws != nil && m.ws.root != nil && msg.reopen {
			m.viewMode = ViewFullNote
		}
		m.pendingSelect = msg.notePath
		m.localEdit()
		m.lastShape = ""
		cmds = append(cmds, m.shapeCmd())
		return m, tea.Batch(cmds...)

	case toastDismissMsg:
		if msg.id == m.toastID {
			m.toastMsg = ""
		}
		return m, nil

	case booksChangedMsg:
		return m.onBooksChanged()

	case tea.MouseMsg:
		return m.updateMouse(msg)

	case autosaveMsg:
		if m.ws != nil {
			if b, ok := m.ws.bufs[msg.path]; ok && b.editSeq == msg.seq && b.dirty() {
				return m, m.saveBuffer(b)
			}
		}
		return m, nil

	case overlayTickMsg:
		if m.overlayFrame > 0 {
			m.overlayFrame--
			if m.overlayFrame > 0 {
				return m, overlayTick()
			}
		}
		return m, nil

	case loginDoneMsg:
		return m.onLoginDone(msg)

	case signOutDoneMsg:
		return m, nil

	case tea.KeyMsg:
		if m.acct != nil {
			return m.updateAccounts(msg)
		}
		if m.hover != nil && m.viewMode == ViewFullNote && !m.showPalette && !m.showHelp {
			return m.updateHover(msg.String())
		}
		if m.showPalette {
			return m.updatePalette(msg)
		}
		if m.showHelp {
			return m.updateHelp(msg)
		}
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
		if m.floatExp && m.viewMode == ViewFullNote {
			return m.updateFloatExplorer(msg)
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
