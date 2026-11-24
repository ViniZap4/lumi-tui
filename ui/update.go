package ui

import (
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/vinizap/lumi/tui-client/books"
)

// yankFlashMsg redraws once the editor's yank flash has expired.
type yankFlashMsg time.Time

func (m Model) Init() tea.Cmd {
	cmds := []tea.Cmd{m.loadItems, m.loadRecent, m.shapeCmd()}
	if m.mouseOn {
		cmds = append(cmds, tea.EnableMouseCellMotion)
	}
	if m.bookMgr != nil {
		cmds = append(cmds, m.waitForBooks)
	}
	m.afterUpdate()
	cmds = append(cmds, m.mo.kick())
	return tea.Batch(cmds...)
}

// Update handles one message, then keeps the per-book last note and
// the motion state in step and schedules a frame only when something
// moves (motion.go).
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	tm, cmd := m.update(msg)
	nm, ok := tm.(Model)
	if !ok {
		return tm, cmd
	}
	if s := nm.acct; s != nil && s.needCheck {
		s.needCheck = false
		cmd = tea.Batch(cmd, s.checkAll())
	}
	nm.afterUpdate()
	if k := nm.mo.kick(); k != nil {
		cmd = tea.Batch(cmd, k)
	}
	return nm, cmd
}

// afterUpdate runs after every message: remembers the focused note,
// starts the mode pill morph when the mode changed and tells the
// motion whether the active book is syncing (the breathing dot).
func (m *Model) afterUpdate() {
	m.noteRemembered()
	if m.mo == nil {
		return
	}
	if mode := m.modeName(); mode != m.mo.pillMode {
		if m.mo.pillMode != "" {
			m.mo.pillFrom = m.mo.pillMode
			m.mo.to(animPill, 0, 1)
		}
		m.mo.pillMode = mode
	}
	m.mo.syncing = m.bookState() == books.StateSyncing
	m.mo.spinning = m.spinnerWanted()
}

// bookState is the active book's state (StateLocal without one).
func (m Model) bookState() books.State {
	if m.bookMgr == nil {
		return books.StateLocal
	}
	if v, ok := m.bookMgr.View(m.activeBookID); ok {
		return v.Status.State
	}
	return books.StateLocal
}

func (m Model) update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case frameMsg:
		return m, m.mo.frame()

	case recentLoadedMsg:
		m.recent = msg.items
		m.homeCursor = clamp(m.homeCursor, 0, max(0, len(m.recent)-1))
		return m, nil

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil

	case yankFlashMsg:
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
		// the rows stagger in whenever the overlay shows a new folder
		if m.floatExp && m.mo != nil && msg.dir != m.mo.revealDir {
			m.startReveal()
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
		m.searchCursor = 0
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
			m.closeOverlay()
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

	case loginDoneMsg:
		return m.onLoginDone(msg)

	case loginSettledMsg:
		return m.onLoginSettled(msg)

	case serverCheckMsg:
		return m.onServerCheck(msg)

	case serverCheckTickMsg:
		return m.onServerCheckTick(msg)

	case cloneProgressMsg:
		return m.onCloneProgress(msg)

	case cloneDoneMsg:
		return m.onCloneDone(msg)

	case signOutDoneMsg:
		return m, nil

	case tea.KeyMsg:
		if m.acct != nil {
			return m.updateAccounts(msg)
		}
		if m.clone != nil {
			return m.updateClone(msg)
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
		if m.showSearch {
			return m.updateSearch(msg)
		}
		if m.floatExp && m.viewMode != ViewConfig {
			return m.updateFloatExplorer(msg)
		}

		switch m.viewMode {
		case ViewHome:
			return m.updateHome(msg)
		case ViewFullNote:
			return m.updateNote(msg)
		case ViewConfig:
			return m.updateConfig(msg)
		}
	}

	return m, nil
}
