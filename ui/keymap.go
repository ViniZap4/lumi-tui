package ui

import (
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/vinizap/lumi/tui-client/config"
	"github.com/vinizap/lumi/tui-client/domain"
	"github.com/vinizap/lumi/tui-client/editor"
	"github.com/vinizap/lumi/tui-client/filesystem"
)

// The explorer keymap (yazi grammar, the same map as the web and Apple
// clients; see help.go for the reference card). The explorer lives only
// in the floating overlay (float.go); these keys apply inside it:
//
//	j k  gg G  ⌃d ⌃u  5j      move (counts allowed)
//	h l ⏎  H L  ~ gh zh       folders, history, book root
//	/ n N                     filter in place, next/prev match
//	a r d y p m .  R          new, rename/move, delete, yank/paste,
//	                          move picker, hidden, refresh
//	⏎ ⌃v ⌃s  e i E            open here / in a split, edit
//	⌃h ⌃l  ⌃j ⌃k              pane focus, preview scroll
//	space :  ?  ,  [ ]  gb gs g1–9  zp  Esc q (close)
//
// Keys are dispatched in layers: overlays (palette, help, prompt,
// confirm, filter) → a pending chord (g/z) → a count → the focused
// pane → app-wide keys.

// clickInfo remembers the last left click for double-click detection.
type clickInfo struct {
	at   time.Time
	x, y int
}

// nowFn is the clock (tests replace it).
var nowFn = time.Now

// persist applies a change to the saved settings.
func (m Model) persist(apply func(c *config.Config)) {
	cfg := config.Load()
	apply(cfg)
	_ = cfg.Save()
}

// editLine applies a text-editing key to value. ok=false means the key
// is not a text-editing key (the caller handles it). Rune-safe: accents
// and pasted text work.
func editLine(value string, msg tea.KeyMsg) (string, bool) {
	switch msg.Type {
	case tea.KeyRunes:
		return value + string(msg.Runes), true
	case tea.KeySpace:
		return value + " ", true
	case tea.KeyBackspace:
		r := []rune(value)
		if len(r) > 0 {
			r = r[:len(r)-1]
		}
		return string(r), true
	case tea.KeyCtrlU:
		return "", true
	case tea.KeyCtrlW:
		r := []rune(strings.TrimRightFunc(value, unicode.IsSpace))
		i := len(r)
		for i > 0 && !unicode.IsSpace(r[i-1]) && r[i-1] != '/' {
			i--
		}
		if i == len(r) && i > 0 {
			i--
		}
		return string(r[:i]), true
	}
	return value, false
}

// halfPage is the ⌃d/⌃u step for the explorer list.
func (m Model) halfPage() int { return max(1, (m.contentHeight()-2)/2) }

// takeCount consumes the pending count (default 1).
func (m *Model) takeCount() int {
	n, err := strconv.Atoi(m.count)
	m.count = ""
	if err != nil || n < 1 {
		return 1
	}
	return n
}

// globalKey handles keys that mean the same everywhere outside text
// input. ok=false lets the view handle the key.
func (m Model) globalKey(key string) (Model, tea.Cmd, bool) {
	switch key {
	case "ctrl+c":
		return m, tea.Quit, true
	case " ":
		m.openPalette("")
		return m, nil, true
	case ":":
		m.openPalette(":")
		return m, nil, true
	case "?":
		m.showHelp = true
		m.helpScroll = 0
		return m, nil, true
	case ",":
		m.enterConfig()
		return m, nil, true
	case "[":
		mm, cmd := m.stepBook(-1)
		return mm, cmd, true
	case "]":
		mm, cmd := m.stepBook(1)
		return mm, cmd, true
	}
	return m, nil, false
}

// stepBook opens the previous/next book in registry order.
func (m Model) stepBook(delta int) (Model, tea.Cmd) {
	if m.bookMgr == nil {
		return m, m.showToast("no books registered (lumi books link <dir>)", ToastInfo, 3*time.Second)
	}
	list := m.bookMgr.Registry()
	if len(list) < 2 {
		return m, nil
	}
	cur := 0
	for i, b := range list {
		if b.ID == m.activeBookID {
			cur = i
		}
	}
	next := (cur + delta + len(list)) % len(list)
	tm, cmd := m.switchToBook(next)
	return tm.(Model), cmd
}

// chord completes a g/z chord. ok=false: not a chord (the key is then
// dispatched normally, so `g` + `j` still moves).
func (m Model) chord(seq string) (Model, tea.Cmd, bool) {
	switch seq {
	case "gg":
		if m.pane == PanePreview {
			m.previewScroll = 0
		} else if m.pane == PaneParent {
			m.parentCursor = 0
		} else {
			m.cursor = 0
		}
		return m, nil, true
	case "ge":
		if m.pane == PanePreview {
			m.previewScroll = 1 << 20
		} else {
			m.cursor = max(0, len(m.items)-1)
		}
		return m, nil, true
	case "gh", "zh":
		cmd := m.goRoot()
		return m, cmd, true
	case "zp":
		m.previewHidden = !m.previewHidden
		if m.previewHidden && m.pane == PanePreview {
			m.pane = PaneList
		}
		return m, nil, true
	case "gb":
		if m.bookMgr != nil {
			m.openBooksLevel()
		}
		if m.bookMgr == nil {
			return m, m.showToast("no books registered (lumi books link <dir>)", ToastInfo, 3*time.Second), true
		}
		return m, nil, true
	case "gs":
		if m.bookMgr != nil {
			m.showBookDetails = true
		}
		return m, nil, true
	case "ga":
		m.openAccounts()
		return m, nil, true
	}
	if len(seq) == 2 && seq[0] == 'g' && seq[1] >= '1' && seq[1] <= '9' {
		tm, cmd := m.switchToBook(int(seq[1] - '1'))
		return tm.(Model), cmd, true
	}
	return m, nil, false
}

func (m *Model) goRoot() tea.Cmd {
	m.clearFilter()
	m.pane = PaneList
	return m.navigateTo(m.rootDir, 0)
}

// pendingInput is the chord/count shown by the legend.
func (m Model) pendingInput() string {
	switch {
	case m.floatExp && m.bk != nil:
		return m.bk.prefix
	case m.floatExp:
		return m.count + m.treePrefix
	}
	return m.notePrefix
}

func (m Model) updateTree(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.showNav {
		return m.updateTreeNavModal(msg)
	}
	if m.filterTyping {
		return m.updateFilter(msg)
	}
	key := msg.String()

	if m.treePrefix != "" {
		prefix := m.treePrefix
		m.treePrefix = ""
		if mm, cmd, ok := m.chord(prefix + key); ok {
			m = mm
			m.count = ""
			return m, cmd
		}
		if key == "esc" {
			m.count = ""
			return m, nil
		}
	}
	if len(key) == 1 && key[0] >= '0' && key[0] <= '9' && (key != "0" || m.count != "") {
		if len(m.count) < 4 {
			m.count += key
		}
		return m, nil
	}

	switch m.pane {
	case PaneParent:
		if mm, cmd, ok := m.updateParentPane(key); ok {
			return mm, cmd
		}
	case PanePreview:
		if mm, cmd, ok := m.updatePreviewPane(key); ok {
			return mm, cmd
		}
	}
	return m.updateListPane(key)
}

// focusPane moves focus left (-1) or right (+1).
func (m *Model) focusPane(delta int) {
	if !m.paneNav {
		return
	}
	left, _, right, _ := treeColumnWidths(m.width, m.previewHidden)
	p := int(m.pane) + delta
	if p == int(PaneParent) && (left == 0 || m.currentDir == m.rootDir) {
		p += delta
	}
	if p == int(PanePreview) && right == 0 {
		p -= delta
	}
	if p < int(PaneParent) || p > int(PanePreview) {
		return
	}
	m.pane = Pane(p)
	if m.pane == PaneParent {
		m.parentCursor = 0
		for i, it := range m.parentItems() {
			if it.Path == m.currentDir {
				m.parentCursor = i
			}
		}
	}
}

// parentItems lists the parent folder (empty at the root).
func (m Model) parentItems() []Item {
	if m.currentDir == m.rootDir {
		return nil
	}
	return listDir(filepath.Dir(m.currentDir), m.showHidden)
}

func (m Model) updateParentPane(key string) (Model, tea.Cmd, bool) {
	items := m.parentItems()
	switch key {
	case "j", "down":
		m.parentCursor = clamp(m.parentCursor+m.takeCount(), 0, len(items)-1)
	case "k", "up":
		m.parentCursor = clamp(m.parentCursor-m.takeCount(), 0, len(items)-1)
	case "G":
		m.parentCursor = max(0, len(items)-1)
	case "g":
		m.treePrefix = "g"
	case "l", "right", "enter":
		m.count = ""
		m.pane = PaneList
		if m.parentCursor < len(items) {
			it := items[m.parentCursor]
			if it.IsFolder {
				m.clearFilter()
				return m, m.navigateTo(it.Path, 0), true
			}
			if it.Note != nil {
				m.openNote(it.Note)
			}
		}
	case "h", "left", "backspace":
		m.count = ""
		m.pane = PaneList
		return m, m.goParent(), true
	case "esc", "ctrl+l":
		m.count = ""
		m.pane = PaneList
	default:
		return m, nil, false
	}
	return m, nil, true
}

func (m Model) updatePreviewPane(key string) (Model, tea.Cmd, bool) {
	switch key {
	case "j", "down":
		m.previewScroll += m.takeCount()
	case "k", "up":
		m.previewScroll = max(0, m.previewScroll-m.takeCount())
	case "ctrl+d":
		m.previewScroll += m.halfPage()
	case "ctrl+u":
		m.previewScroll = max(0, m.previewScroll-m.halfPage())
	case "G":
		m.previewScroll = 1 << 20
	case "g":
		m.treePrefix = "g"
	case "esc", "ctrl+h", "h", "left":
		m.count = ""
		m.pane = PaneList
	default:
		return m, nil, false
	}
	return m, nil, true
}

func (m *Model) goParent() tea.Cmd {
	if m.currentDir == m.rootDir {
		return nil
	}
	from := m.currentDir
	m.clearFilter()
	cmd := m.navigateTo(filepath.Dir(m.currentDir), 0)
	m.pendingSelect = from
	return cmd
}

func (m *Model) moveCursor(delta int) {
	if len(m.items) == 0 {
		m.cursor = 0
		return
	}
	m.cursor = clamp(m.cursor+delta, 0, len(m.items)-1)
	m.previewScroll = 0
}

func (m Model) currentItem() (Item, bool) {
	if m.cursor < len(m.items) {
		return m.items[m.cursor], true
	}
	return Item{}, false
}

// openItem is `l` / ⏎: enter a folder or read a note.
func (m Model) openItem() (Model, tea.Cmd) {
	it, ok := m.currentItem()
	if !ok {
		return m, nil
	}
	if it.IsFolder {
		m.clearFilter()
		m.pane = PaneList
		return m, m.navigateTo(it.Path, 0)
	}
	if it.Note != nil {
		m.openNote(it.Note)
	}
	return m, nil
}

// editCmd hands the note to $EDITOR (the TUI has no in-place editor:
// the reader's vim cursor is read-only). reopen: show the reader after.
func (m Model) editCmd(n *noteRef, reopen bool) tea.Cmd {
	path := n.path
	return tea.ExecProcess(editor.OpenCmdAt(n.note, n.line, n.col), func(err error) tea.Msg {
		return editorDoneMsg{notePath: path, reopen: reopen, err: err}
	})
}

func (m Model) updateListPane(key string) (tea.Model, tea.Cmd) {
	switch key {
	case "j", "down":
		m.moveCursor(m.takeCount())
		return m, nil
	case "k", "up":
		m.moveCursor(-m.takeCount())
		return m, nil
	case "ctrl+d":
		m.count = ""
		m.moveCursor(m.halfPage())
		return m, nil
	case "ctrl+u":
		m.count = ""
		m.moveCursor(-m.halfPage())
		return m, nil
	case "G":
		if m.count != "" {
			m.cursor = clamp(m.takeCount()-1, 0, len(m.items)-1)
		} else {
			m.cursor = max(0, len(m.items)-1)
		}
		return m, nil
	}
	m.count = ""
	switch key {
	case "q":
		m.closeOverlay()
		return m, nil
	case "esc":
		if m.filter != "" {
			m.clearFilter()
		}
		return m, nil
	case "g", "z":
		m.treePrefix = key
	case "h", "left", "backspace":
		return m, m.goParent()
	case "l", "right", "enter":
		return m.openItem()
	case "H":
		m.clearFilter()
		return m, m.historyBack()
	case "L":
		m.clearFilter()
		return m, m.historyForward()
	case "~":
		return m, m.goRoot()
	case "/":
		m.filterTyping = true
		return m, nil
	case "n", "N":
		if len(m.items) > 0 {
			d := 1
			if key == "N" {
				d = -1
			}
			m.cursor = (m.cursor + d + len(m.items)) % len(m.items)
		}
	case "a":
		m.showInput, m.inputMode, m.inputValue = true, "create", ""
	case "r":
		if it, ok := m.currentItem(); ok {
			m.showInput = true
			m.inputMode = "rename"
			m.inputValue = it.Name
			if it.IsFolder {
				m.inputMode = "rename_folder"
			}
		}
	case "d":
		m.askDelete()
	case "y":
		if it, ok := m.currentItem(); ok {
			if it.IsFolder {
				return m, m.showToast("y copies notes; folders move with r or m", ToastInfo, 3*time.Second)
			}
			cp := it
			m.yanked = &cp
			return m, m.showToast("yanked “"+it.Name+"” — p pastes a copy", ToastInfo, 2*time.Second)
		}
	case "p", "D":
		src := m.yanked
		if key == "D" {
			if it, ok := m.currentItem(); ok && it.Note != nil {
				src = &it
			}
		}
		if src == nil || src.Note == nil {
			return m, m.showToast("nothing yanked (y on a note)", ToastInfo, 2*time.Second)
		}
		n, err := pasteNote(src.Note, m.currentDir)
		if err != nil {
			return m, m.showToast("paste: "+err.Error(), ToastError, 4*time.Second)
		}
		m.pendingSelect = n.Path
		return m, m.loadItems
	case "m":
		if it, ok := m.currentItem(); ok {
			if it.IsFolder {
				m.pendingMoveFolder, m.pendingMoveNote = it.Path, nil
			} else {
				m.pendingMoveNote, m.pendingMoveFolder = it.Note, ""
			}
			m.showNav, m.navDir, m.navCursor = true, m.rootDir, 0
			return m, m.loadNavItems
		}
	case ".":
		m.showHidden = !m.showHidden
		if it, ok := m.currentItem(); ok {
			m.pendingSelect = it.Path
		}
		return m, m.loadItems
	case "R":
		m.localEdit()
		if it, ok := m.currentItem(); ok {
			m.pendingSelect = it.Path
		}
		return m, tea.Batch(m.loadItems, m.showToast("refreshed", ToastInfo, time.Second))
	case "e", "i", "E":
		// i/e: the default editor (Settings → Editor padrão); E: the other
		if it, ok := m.currentItem(); ok && it.Note != nil {
			external := m.noteCfg().DefaultEditor == "external"
			if key == "E" {
				external = !external
			}
			if external {
				return m, m.editCmd(&noteRef{note: it.Note, path: it.Path}, false)
			}
			m.openNote(it.Note)
			mm, cmd := m.editInternal(m.ws.focus)
			return mm, cmd
		}
	case "c":
		m.enterConfig()
	case "ctrl+h":
		m.focusPane(-1)
	case "ctrl+l":
		m.focusPane(1)
	case "ctrl+j":
		m.previewScroll++
	case "ctrl+k":
		m.previewScroll = max(0, m.previewScroll-1)
	default:
		if mm, cmd, ok := m.globalKey(key); ok {
			return mm, cmd
		}
	}
	return m, nil
}

func (m *Model) askDelete() {
	it, ok := m.currentItem()
	if !ok {
		return
	}
	if it.IsFolder {
		empty, _ := filesystem.IsFolderEmpty(it.Path)
		m.pendingDeleteFolder = it.Path
		m.confirmMsg = fmt.Sprintf("delete folder “%s”?", it.Name)
		if !empty {
			m.confirmMsg = fmt.Sprintf("delete folder “%s” and everything in it?", it.Name)
		}
	} else if it.Note != nil {
		m.pendingDeleteNote = it.Note
		m.confirmMsg = fmt.Sprintf("delete “%s”?", it.Name)
	} else {
		return
	}
	m.showConfirm = true
}

// updateFilter edits the in-place filter (`/`).
func (m Model) updateFilter(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc", "ctrl+c":
		m.clearFilter()
		return m, nil
	case "enter":
		m.filterTyping = false
		if m.filter == "" {
			m.applyFilter()
		}
		return m, nil
	case "down", "ctrl+n", "ctrl+j":
		m.moveCursor(1)
		return m, nil
	case "up", "ctrl+p", "ctrl+k":
		m.moveCursor(-1)
		return m, nil
	}
	if v, ok := editLine(m.filter, msg); ok {
		m.filter = v
		m.applyFilter()
	}
	return m, nil
}

// noteRef is what the editor handoff needs.
type noteRef struct {
	note      *domain.Note
	path      string
	line, col int
}

// explorerCommands run inside the floating explorer: the palette opens
// it first when it is closed.
var explorerCommands = map[string]bool{
	"create": true, "rename": true, "delete": true, "filter": true, "refresh": true,
	"preview": true, "hidden": true, "root": true,
}

// runCommand runs a palette command id.
func (m Model) runCommand(id string) (tea.Model, tea.Cmd) {
	var open tea.Cmd
	if explorerCommands[id] && (!m.floatExp || m.bk != nil) {
		if !m.floatExp {
			m, open = m.openFloatExplorer()
		} else {
			m.bk = nil
			open = m.loadItems
		}
	}
	switch id {
	case "create":
		m.showInput, m.inputMode, m.inputValue = true, "create", ""
	case "rename":
		mm, cmd := m.updateListPane("r")
		return mm, tea.Batch(open, cmd)
	case "delete":
		m.askDelete()
	case "edit":
		if m.viewMode == ViewFullNote && m.ws != nil && m.ws.focus != nil && !m.floatExp {
			return m.editDefault(m.ws.focus)
		}
		if m.floatExp {
			return m.updateFloatExplorer(keyRunes("e"))
		}
	case "edit-other":
		if m.viewMode == ViewFullNote && m.ws != nil && m.ws.focus != nil && !m.floatExp {
			return m.editOther(m.ws.focus)
		}
		if m.floatExp {
			return m.updateFloatExplorer(keyRunes("E"))
		}
	case "accounts":
		m.openAccounts()
	case "view-reader", "view-source", "view-preview", "view-board":
		if m.ws != nil && m.ws.focus != nil {
			v := map[string]paneView{"view-reader": viewReader, "view-source": viewSource, "view-preview": viewSplit, "view-board": viewBoard}[id]
			m.closeOverlay()
			m.viewMode = ViewFullNote
			m.setView(m.ws.focus, v)
			return m, m.shapeCmd()
		}
	case "vsplit", "hsplit":
		if m.ws != nil && m.ws.focus != nil {
			m.closeOverlay()
			m.viewMode = ViewFullNote
			return m.windowCmd(map[string]string{"vsplit": "v", "hsplit": "s"}[id])
		}
	case "float":
		return m.openFloatExplorer()
	case "layout":
		if !m.restoreLayout("") {
			return m, m.showToast("nenhum layout salvo para este livro", ToastInfo, 2*time.Second)
		}
		m.closeOverlay()
	case "filter":
		m.filterTyping = true
	case "search":
		m.showSearch, m.searchQuery, m.searchType, m.searchCursor = true, "", "content", 0
		return m, m.dispatchSearch()
	case "refresh":
		mm, cmd := m.updateListPane("R")
		return mm, tea.Batch(open, cmd)
	case "preview":
		mm, cmd, _ := m.chord("zp")
		return mm, tea.Batch(open, cmd)
	case "hidden":
		mm, cmd := m.updateListPane(".")
		return mm, tea.Batch(open, cmd)
	case "legend":
		m.legendOn = !m.legendOn
		m.persist(func(c *config.Config) { c.ShowLegend = m.legendOn })
	case "root":
		return m, tea.Batch(open, m.goRoot())
	case "books":
		mm, cmd, _ := m.chord("gb")
		return mm, cmd
	case "sync":
		mm, cmd, _ := m.chord("gs")
		return mm, cmd
	case "prevbook":
		return m.stepBook(-1)
	case "nextbook":
		return m.stepBook(1)
	case "settings":
		m.enterConfig()
	case "help":
		m.showHelp, m.helpScroll = true, 0
	case "quit":
		return m, tea.Quit
	}
	return m, open
}

// keyRunes is the KeyMsg for typed runes.
func keyRunes(s string) tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)} }
