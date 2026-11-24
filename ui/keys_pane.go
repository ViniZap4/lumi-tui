package ui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/vinizap/lumi/tui-client/domain"
	"github.com/vinizap/lumi/tui-client/editor"
	"github.com/vinizap/lumi/tui-client/filesystem"
	"github.com/vinizap/lumi/tui-client/theme"
	"github.com/vinizap/lumi/tui-client/vim"
)

// Note keymap (SPEC-PARTE2 §2–§5, §9). The focused pane's view decides:
//
//	reader   j k d u f b  gg G  Tab ⇧Tab ⏎/gf K  y  /n N  i e E  zv  q
//	editor   the vim engine; :w :q :wq :x :edit!/:ext :vs :sp :only …
//	board    h l j k  H L J K  a o A  ⏎/i  x  dd  /  zv
//	canvas   j k Tab  ⏎/gf  ⌃w f  zv
//
// everywhere: ⌃w v s q o = w p h j k l f F, ⌃h/j/k/l, - and <leader>e
// (floating explorer), : / space (palette), ? help, , settings.

// updateNote routes a key to the focused pane.
func (m Model) updateNote(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.ws == nil || m.ws.focus == nil {
		m.viewMode = ViewTree
		return m, m.loadItems
	}
	m.statusMsg = ""
	key := msg.String()
	if m.winPending {
		m.winPending = false
		return m.windowCmd(key)
	}
	p := m.ws.focus
	switch p.view {
	case viewSource, viewSplit:
		if p.view == viewSplit && p.previewFocus {
			return m.updatePreviewFocus(key)
		}
		return m.updateEditor(msg)
	case viewBoard:
		return m.updateBoard(msg)
	case viewCanvas:
		return m.updateCanvas(key)
	}
	return m.updateReader(key)
}

// commonNoteKey handles keys shared by the reader, board and canvas.
func (m Model) commonNoteKey(key string) (Model, tea.Cmd, bool) {
	p := m.ws.focus
	if m.notePrefix != "" {
		pre := m.notePrefix
		m.notePrefix = ""
		switch pre + key {
		case "zv":
			m.cycleView(p)
			return m, nil, true
		case "gs":
			if m.bookMgr != nil {
				m.showBookDetails = true
			}
			return m, nil, true
		case "gb":
			mm, cmd, _ := m.chord("gb")
			return mm, cmd, true
		case "ga":
			m.openAccounts()
			return m, nil, true
		}
		if pre == m.leaderKey() && key == "e" {
			mm, cmd := m.openFloatExplorer()
			return mm, cmd, true
		}
		if pre == "g" || pre == "z" || pre == m.leaderKey() {
			return m, nil, false
		}
	}
	switch key {
	case "ctrl+c":
		cmd := m.saveAll()
		return m, tea.Sequence(cmd, tea.Quit), true
	case "ctrl+w":
		m.winPending = true
		return m, nil, true
	case "ctrl+h", "ctrl+j", "ctrl+k", "ctrl+l":
		if m.paneNav {
			m.moveFocusDir(key[len(key)-1:])
		}
		return m, nil, true
	case "-":
		mm, cmd := m.openFloatExplorer()
		return mm, cmd, true
	case "z", "g":
		m.notePrefix = key
		return m, nil, true
	case "q", "esc":
		m.syncFocusOut()
		cmd := m.saveAll()
		m.viewMode = ViewTree
		m.pendingSelect = p.buf.path
		return m, tea.Batch(cmd, m.loadItems), true
	case "i", "e":
		mm, cmd := m.editDefault(p)
		return mm, cmd, true
	case "E":
		mm, cmd := m.editOther(p)
		return mm, cmd, true
	}
	if key == m.leaderKey() {
		m.notePrefix = key
		return m, nil, true
	}
	if mm, cmd, ok := m.globalKey(key); ok {
		return mm, cmd, true
	}
	return m, nil, false
}

// leaderKey is the vimrc leader as a key ("\\" by default).
func (m Model) leaderKey() string {
	l := `\`
	if m.ws != nil && m.ws.focus != nil {
		l = m.ws.focus.buf.eng.Opts.Leader
	}
	ks, err := vim.ParseKeys(l, `\`)
	if err != nil || len(ks) != 1 {
		return `\`
	}
	return ks[0]
}

// cycleView is zv: reader → source → source+preview (→ board/canvas).
func (m *Model) cycleView(p *notePane) {
	order := []paneView{viewReader, viewSource, viewSplit}
	if p.buf.isBoard {
		order = []paneView{viewBoard, viewSource, viewReader}
	}
	if p.buf.isCanvas {
		order = []paneView{viewCanvas, viewSource}
	}
	next := order[0]
	for i, v := range order {
		if v == p.view {
			next = order[(i+1)%len(order)]
		}
	}
	m.setView(p, next)
}

func (m *Model) setView(p *notePane, v paneView) {
	if (v == viewBoard && !p.buf.isBoard) || (v == viewCanvas && !p.buf.isCanvas) {
		m.statusMsg = "esta nota não é um " + v.String()
		return
	}
	if p.view == viewSource || p.view == viewSplit {
		m.syncFocusOut()
	}
	p.view = v
	p.previewFocus = false
	m.statusMsg = "vista: " + v.String()
	m.saveLayout()
}

// editDefault is i/e: the editor chosen in settings.
func (m Model) editDefault(p *notePane) (Model, tea.Cmd) {
	if m.noteCfg().DefaultEditor == "external" {
		return m, m.editExternal(p)
	}
	return m.editInternal(p)
}

// editOther is E: the editor that is not the default.
func (m Model) editOther(p *notePane) (Model, tea.Cmd) {
	if m.noteCfg().DefaultEditor == "external" {
		return m.editInternal(p)
	}
	return m, m.editExternal(p)
}

func (m Model) editInternal(p *notePane) (Model, tea.Cmd) {
	if p.view != viewSource && p.view != viewSplit {
		m.setView(p, viewSource)
		m.statusMsg = ""
	}
	return m, nil
}

// editExternal saves the buffer and hands the file to $VISUAL/$EDITOR
// (or the editor command in settings); the edit is pushed 3-way after.
func (m Model) editExternal(p *notePane) tea.Cmd {
	var cmds []tea.Cmd
	if p.buf.dirty() {
		cmds = append(cmds, m.saveBuffer(p.buf))
	}
	c := p.buf.eng.Cursor()
	if p.view == viewReader {
		c = vim.Pos{}
	}
	note := p.buf.note
	path := p.buf.path
	// hand the terminal over with its default cursor shape
	cmds = append(cmds, cursorShapeCmd(vim.ResetCursorShape))
	cmds = append(cmds, tea.ExecProcess(editor.OpenCmdAt(note, c.Line, c.Col), func(err error) tea.Msg {
		return editorDoneMsg{notePath: path, reopen: true, err: err}
	}))
	return tea.Sequence(cmds...)
}

// reloadBuffer re-reads a buffer from disk (after $EDITOR or a pull).
func (m *Model) reloadBuffer(path string) {
	if m.ws == nil {
		return
	}
	b, ok := m.ws.bufs[path]
	if !ok {
		return
	}
	body, err := readBody(path)
	if err != nil {
		return
	}
	if body == b.savedBody {
		return
	}
	if b.dirty() {
		// keep the edits; saving merges or keeps both (saveBuffer)
		m.statusMsg = "a nota mudou no disco — ao salvar, as edições são combinadas"
		return
	}
	cur := b.eng.Cursor()
	b.eng.SetText(body)
	b.eng.SetCursor(cur)
	b.savedBody, b.savedGen = body, b.eng.Gen()
	if n, err := filesystem.ReadNote(path); err == nil {
		b.note = n
	}
}

func readBody(path string) (string, error) {
	if isCanvasPath(path) {
		data, err := os.ReadFile(path)
		return string(data), err
	}
	_, body, err := filesystem.ReadRaw(path)
	return body, err
}

// saveAll saves every dirty buffer.
func (m *Model) saveAll() tea.Cmd {
	if m.ws == nil {
		return nil
	}
	var cmds []tea.Cmd
	for _, b := range m.ws.bufs {
		if b.dirty() {
			cmds = append(cmds, m.saveBuffer(b))
		}
	}
	return tea.Batch(cmds...)
}

// --- editor ----------------------------------------------------------

func (m Model) updateEditor(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	p := m.ws.focus
	e := p.buf.eng
	key := msg.String()
	_, _, h := m.paneRect(p)
	e.Height = max(1, h-1)
	idle := e.Mode() == vim.ModeNormal && e.Pending() == ""
	if idle {
		switch key {
		case "ctrl+w":
			m.winPending = true
			return m, nil
		case "ctrl+h", "ctrl+j", "ctrl+k", "ctrl+l":
			if m.paneNav {
				if key == "ctrl+l" && p.view == viewSplit {
					p.previewFocus = true
					return m, nil
				}
				m.moveFocusDir(key[len(key)-1:])
				return m, nil
			}
		case "-":
			return m.openFloatExplorer()
		case "enter":
			if l, ok := m.linkUnderCursor(p); ok {
				return m.followLink(p, l, "replace")
			}
		case "ctrl+c":
			cmd := m.saveAll()
			return m, tea.Sequence(cmd, tea.Quit)
		}
	}
	before := e.Gen()
	var effs []vim.Effect
	if msg.Type == tea.KeyRunes && len(msg.Runes) > 1 && !msg.Alt {
		if e.Mode() == vim.ModeInsert || e.Mode() == vim.ModeReplace || e.Mode() == vim.ModeCmdline {
			effs = e.Feed(string(msg.Runes))
		} else {
			for _, r := range msg.Runes {
				effs = append(effs, e.Feed(string(r))...)
			}
		}
	} else {
		effs = e.Feed(key)
	}
	var cmds []tea.Cmd
	if e.Gen() != before {
		cmds = append(cmds, m.changed(p.buf))
	}
	if _, _, _, ok := e.Flash(time.Second); ok {
		cmds = append(cmds, tea.Tick(200*time.Millisecond, func(t time.Time) tea.Msg { return yankFlashMsg(t) }))
	}
	mm := m
	for _, ef := range effs {
		var cmd tea.Cmd
		mm, cmd = mm.runEffect(p, ef)
		cmds = append(cmds, cmd)
	}
	cmds = append(cmds, mm.shapeCmd())
	return mm, tea.Batch(cmds...)
}

// shapeCmd emits DECSCUSR when the focused editor's mode shape changed.
func (m *Model) shapeCmd() tea.Cmd {
	seq := vim.ResetCursorShape
	if m.viewMode == ViewFullNote && m.ws != nil && m.ws.focus != nil {
		if v := m.ws.focus.view; v == viewSource || v == viewSplit {
			seq = m.ws.focus.buf.eng.Shape(m.normalShape()).DECSCUSR()
		}
	}
	if seq == m.lastShape || (m.lastShape == "" && seq == vim.ResetCursorShape) {
		return nil
	}
	m.lastShape = seq
	return cursorShapeCmd(seq)
}

func (m Model) normalShape() vim.CursorShape {
	switch m.noteCfg().CursorStyle {
	case "bar":
		return vim.ShapeBar
	case "underline":
		return vim.ShapeUnderline
	}
	return vim.ShapeBlock
}

func (m Model) runEffect(p *notePane, ef vim.Effect) (Model, tea.Cmd) {
	switch ef.Kind {
	case vim.EffWrite:
		if ef.Name == "all" {
			return m, m.saveAll()
		}
		cmd := m.saveBuffer(p.buf)
		m.statusMsg = fmt.Sprintf("%q gravado", filepath.Base(p.buf.path))
		return m, cmd
	case vim.EffQuit, vim.EffWriteQuit:
		cmd := m.saveAll()
		if ef.Name == "all" {
			return m, tea.Sequence(cmd, tea.Quit)
		}
		if len(m.ws.leaves()) > 1 {
			return m, tea.Batch(cmd, m.closePane())
		}
		// one pane: leave the editor for the reader (web parity)
		if p.buf.isCanvas {
			m.setView(p, viewCanvas)
		} else if p.buf.isBoard {
			m.setView(p, viewBoard)
		} else {
			m.setView(p, viewReader)
		}
		m.statusMsg = ""
		return m, tea.Batch(cmd, m.shapeCmd())
	case vim.EffEditExternal:
		return m, m.editExternal(p)
	case vim.EffWindow:
		return m.windowCmd(ef.Arg)
	case vim.EffFollowLink:
		if l, ok := m.linkUnderCursor(p); ok {
			return m.followLink(p, l, "replace")
		}
		m.statusMsg = "nenhum link no cursor"
	case vim.EffHover:
		if l, ok := m.linkUnderCursor(p); ok {
			return m.openHover(p, l)
		}
		m.statusMsg = "nenhum link no cursor"
	case vim.EffOpenURL:
		if l, ok := m.linkUnderCursor(p); ok && l.external {
			openURLFn(l.target)
			m.statusMsg = "aberto no navegador"
		}
	case vim.EffHostMap:
		if ef.Arg == "explorer" {
			return m.openFloatExplorer()
		}
	case vim.EffEx:
		return m.exCommand(p, ef)
	}
	return m, nil
}

// exCommand runs host ex commands (:vsplit, :only, :reader, …).
func (m Model) exCommand(p *notePane, ef vim.Effect) (Model, tea.Cmd) {
	switch ef.Name {
	case "vs", "vsp", "vsplit", "sp", "split", "new", "vnew":
		how := "hsplit"
		if strings.HasPrefix(ef.Name, "v") {
			how = "vsplit"
		}
		note := p.buf.note
		if ef.Args != "" {
			note = m.resolveLink(p.buf.path, ef.Args)
			if note == nil {
				m.statusMsg = "nota não encontrada: " + ef.Args
				return m, nil
			}
		}
		return m, m.splitWith(note, how)
	case "edit", "e":
		if n := m.resolveLink(p.buf.path, ef.Args); n != nil {
			return m, m.openNoteIn(n, "replace")
		}
		m.statusMsg = "nota não encontrada: " + ef.Args
	case "on", "only":
		return m, m.onlyPane()
	case "reader", "read", "leitura":
		m.setView(p, viewReader)
		return m, m.shapeCmd()
	case "source", "fonte":
		m.setView(p, viewSource)
	case "preview", "prévia":
		if p.view == viewSplit {
			m.setView(p, viewSource)
		} else {
			m.setView(p, viewSplit)
		}
	case "board", "quadro":
		m.setView(p, viewBoard)
		return m, m.shapeCmd()
	case "canvas":
		m.setView(p, viewCanvas)
		return m, m.shapeCmd()
	case "Ex", "Explore", "explore", "explorer":
		return m.openFloatExplorer()
	case "settings", "set":
		m.enterConfig()
	case "books", "livros":
		m.openBooksPicker()
	case "accounts", "contas":
		m.openAccounts()
	case "help", "h":
		m.showHelp, m.helpScroll = true, 0
	case "layout":
		if !m.restoreLayout("") {
			m.statusMsg = "nenhum layout salvo para este livro"
		}
	default:
		m.statusMsg = "E492: comando desconhecido: " + ef.Name
	}
	return m, nil
}

// splitWith opens note in a split next to the focused pane, keeping the
// current view and cursor when it is the same note.
func (m *Model) splitWith(note *domain.Note, how string) tea.Cmd {
	cur := m.ws.focus
	cmd := m.openNoteIn(note, how)
	if p := m.ws.focus; p != cur && p.buf == cur.buf {
		p.view = cur.view
		p.cur, p.top, p.scroll = cur.cur, cur.top, cur.scroll
		m.focusIn(p)
	}
	m.saveLayout()
	return cmd
}

// windowCmd runs Ctrl-w <key>.
func (m Model) windowCmd(key string) (Model, tea.Cmd) {
	p := m.ws.focus
	switch key {
	case "v", "ctrl+v":
		m.syncFocusOut()
		return m, m.splitWith(p.buf.note, "vsplit")
	case "s", "S", "ctrl+s":
		m.syncFocusOut()
		return m, m.splitWith(p.buf.note, "hsplit")
	case "q", "c", "ctrl+q":
		return m, m.closePane()
	case "o", "ctrl+o":
		return m, m.onlyPane()
	case "=":
		m.statusMsg = "painéis igualados"
	case "w", "ctrl+w":
		m.cycleFocus(1)
	case "W":
		m.cycleFocus(-1)
	case "p", "ctrl+p":
		if prev := m.ws.prev; prev != nil && m.ws.nodeOf(prev) != nil {
			m.setFocus(prev)
		}
	case "h", "j", "k", "l", "left", "down", "up", "right", "ctrl+h", "ctrl+j", "ctrl+k", "ctrl+l":
		d := map[string]string{"left": "h", "down": "j", "up": "k", "right": "l"}[key]
		if d == "" {
			d = key[len(key)-1:]
		}
		m.moveFocusDir(d)
	case "f", "enter", "F", "ctrl+f":
		how := "vsplit"
		if key == "F" {
			how = "hsplit"
		}
		if l, ok := m.focusedLink(p); ok {
			mm, cmd := m.followLink(p, l, how)
			return mm, cmd
		}
		if p.view == viewCanvas {
			return m.openCanvasRow(p, how)
		}
		m.statusMsg = "nenhum link em foco"
	}
	return m, nil
}

func (m *Model) cycleFocus(d int) {
	ls := m.ws.leaves()
	for i, p := range ls {
		if p == m.ws.focus {
			m.setFocus(ls[(i+d+len(ls))%len(ls)])
			return
		}
	}
}

// moveFocusDir moves focus to the nearest pane in direction h/j/k/l.
func (m *Model) moveFocusDir(d string) {
	rects := m.layoutRects()
	cur, ok := rects[m.ws.focus]
	if !ok {
		return
	}
	var best *notePane
	bestDist := 1 << 30
	cx, cy := cur.x+cur.w/2, cur.y+cur.h/2
	for p, r := range rects {
		if p == m.ws.focus {
			continue
		}
		var ok bool
		var dist int
		switch d {
		case "h":
			ok = r.x+r.w <= cur.x && r.y < cur.y+cur.h && cur.y < r.y+r.h
			dist = cur.x - (r.x + r.w)
		case "l":
			ok = r.x >= cur.x+cur.w && r.y < cur.y+cur.h && cur.y < r.y+r.h
			dist = r.x - (cur.x + cur.w)
		case "k":
			ok = r.y+r.h <= cur.y && r.x < cur.x+cur.w && cur.x < r.x+r.w
			dist = cur.y - (r.y + r.h)
		case "j":
			ok = r.y >= cur.y+cur.h && r.x < cur.x+cur.w && cur.x < r.x+r.w
			dist = r.y - (cur.y + cur.h)
		}
		if !ok {
			continue
		}
		dist = dist*1000 + abs(r.x+r.w/2-cx) + abs(r.y+r.h/2-cy)
		if dist < bestDist {
			best, bestDist = p, dist
		}
	}
	if best != nil {
		m.setFocus(best)
	}
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

// --- links -----------------------------------------------------------

func (m Model) linkUnderCursor(p *notePane) (noteLink, bool) {
	c := p.buf.eng.Cursor()
	return linkAt(findLinks(p.buf.eng.Text()), c.Line, c.Col)
}

// focusedLink is the link the pane points at: the cursor's in the
// editor, the Tab-selected one in the reader.
func (m Model) focusedLink(p *notePane) (noteLink, bool) {
	switch p.view {
	case viewSource, viewSplit:
		return m.linkUnderCursor(p)
	case viewReader:
		ls := findLinks(p.buf.eng.Text())
		if p.link >= 0 && p.link < len(ls) {
			return ls[p.link], true
		}
	}
	return noteLink{}, false
}

// followLink opens a link: notes in the pane / a split, URLs in the
// browser.
func (m Model) followLink(p *notePane, l noteLink, how string) (Model, tea.Cmd) {
	if l.external {
		openURLFn(l.target)
		m.statusMsg = "aberto no navegador: " + truncate(l.target, 40)
		return m, nil
	}
	n := m.resolveLink(p.buf.path, l.target)
	if n == nil {
		m.statusMsg = "nota não encontrada: " + l.target
		return m, nil
	}
	m.syncFocusOut()
	return m, m.openNoteIn(n, how)
}

// --- reader ----------------------------------------------------------

func (m Model) updateReader(key string) (tea.Model, tea.Cmd) {
	p := m.ws.focus
	_, w, h := m.paneRect(p)
	lines, pos := m.readerLines(p, w)
	body := max(1, h-1)
	maxScroll := max(0, len(lines)-body)
	if m.notePrefix == "g" {
		switch key {
		case "g":
			m.notePrefix = ""
			p.scroll = 0
			return m, nil
		case "f":
			m.notePrefix = ""
			if l, ok := m.focusedLink(p); ok {
				return m.followLink(p, l, "replace")
			}
			return m, nil
		case "x":
			m.notePrefix = ""
			if l, ok := m.focusedLink(p); ok && l.external {
				openURLFn(l.target)
			}
			return m, nil
		}
	}
	if mm, cmd, ok := m.commonNoteKey(key); ok {
		return mm, cmd
	}
	links := findLinks(p.buf.eng.Text())
	switch key {
	case "j", "down":
		p.scroll++
	case "k", "up":
		p.scroll--
	case "d", "ctrl+d":
		p.scroll += body / 2
	case "u", "ctrl+u":
		p.scroll -= body / 2
	case "f", "ctrl+f", "pgdown":
		p.scroll += body - 1
	case "b", "ctrl+b", "pgup":
		p.scroll -= body - 1
	case "G":
		p.scroll = maxScroll
	case "tab", "shift+tab", "n", "N":
		if len(links) == 0 {
			m.statusMsg = "sem links nesta nota"
			return m, nil
		}
		d := 1
		if key == "shift+tab" || key == "N" {
			d = -1
		}
		if key == "n" || key == "N" {
			if p.find != "" {
				m.findInReader(p, lines, d)
				return m, nil
			}
		}
		if p.link < 0 {
			if d > 0 {
				p.link = 0
			} else {
				p.link = len(links) - 1
			}
		} else {
			p.link = (p.link + d + len(links)) % len(links)
		}
		if p.link < len(pos) && pos[p.link].line >= 0 {
			ln := pos[p.link].line
			if ln < p.scroll || ln >= p.scroll+body {
				p.scroll = ln - body/3
			}
		}
		m.statusMsg = fmt.Sprintf("link %d/%d: %s", p.link+1, len(links), truncate(links[p.link].target, 50))
	case "enter", "l":
		if l, ok := m.focusedLink(p); ok {
			return m.followLink(p, l, "replace")
		}
		if key == "enter" && len(links) > 0 {
			m.statusMsg = "Tab escolhe um link"
		}
	case "K":
		if l, ok := m.focusedLink(p); ok {
			return m.openHover(p, l)
		}
		m.statusMsg = "Tab escolhe um link; K mostra o cartão"
	case "y":
		_ = systemClipboard.Write(p.buf.eng.Text())
		m.statusMsg = "texto da nota copiado"
	case "/":
		m.showInput, m.inputMode, m.inputValue = true, "find", ""
	case "h", "backspace":
		mm, cmd, _ := m.commonNoteKey("q")
		return mm, cmd
	}
	p.scroll = clamp(p.scroll, 0, maxScroll)
	return m, nil
}

// findInReader jumps to the next/previous rendered line containing the
// find text.
func (m *Model) findInReader(p *notePane, lines []string, d int) {
	q := strings.ToLower(p.find)
	n := len(lines)
	for i := 1; i <= n; i++ {
		ln := ((p.scroll+d*i)%n + n) % n
		if strings.Contains(strings.ToLower(stripAnsi(lines[ln])), q) {
			p.scroll = ln
			m.statusMsg = "/" + p.find
			return
		}
	}
	m.statusMsg = "não encontrado: " + p.find
}

// updatePreviewFocus: the split preview has the keyboard (web parity).
func (m Model) updatePreviewFocus(key string) (tea.Model, tea.Cmd) {
	p := m.ws.focus
	switch key {
	case "j", "down":
		p.previewTop++
	case "k", "up":
		p.previewTop = max(0, p.previewTop-1)
	case "d", "ctrl+d":
		p.previewTop += 10
	case "u", "ctrl+u":
		p.previewTop = max(0, p.previewTop-10)
	case "g":
		p.previewTop = 0
	case "G":
		p.previewTop = 1 << 20
	case "q", "esc", "ctrl+h", "h":
		p.previewFocus = false
	case "ctrl+w":
		m.winPending = true
	case "ctrl+c":
		return m, tea.Quit
	}
	return m, nil
}

// --- floating explorer -----------------------------------------------

// openFloatExplorer shows the explorer overlay at the note's folder.
func (m Model) openFloatExplorer() (Model, tea.Cmd) {
	m.syncFocusOut()
	m.floatExp = true
	dir := m.currentDir
	if m.ws != nil && m.ws.focus != nil {
		dir = filepath.Dir(m.ws.focus.buf.path)
		m.pendingSelect = m.ws.focus.buf.path
	}
	m.clearFilter()
	m.pane = PaneList
	cmds := []tea.Cmd{m.navigateTo(dir, 0), m.startOverlayAnim()}
	return m, tea.Batch(cmds...)
}

func (m Model) updateFloatExplorer(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()
	busy := m.filterTyping || m.showInput || m.showConfirm || m.showNav || m.treePrefix != ""
	if !busy {
		switch key {
		case "esc":
			if m.filter != "" {
				m.clearFilter()
				return m, m.loadItems
			}
			m.floatExp = false
			return m, nil
		case "q":
			m.floatExp = false
			return m, nil
		case "enter", "l", "right", "ctrl+v", "ctrl+s":
			it, ok := m.currentItem()
			if ok && !it.IsFolder && it.Note != nil {
				how := map[string]string{"ctrl+v": "vsplit", "ctrl+s": "hsplit"}[key]
				if how == "" {
					how = "replace"
				}
				m.floatExp = false
				if m.ws == nil || m.ws.root == nil {
					how = "replace"
				}
				return m, m.openNoteIn(it.Note, how)
			}
			if key == "ctrl+v" || key == "ctrl+s" {
				return m, nil
			}
		case "e", "i", "E":
			it, ok := m.currentItem()
			if ok && it.Note != nil {
				m.floatExp = false
				cmd := m.openNoteIn(it.Note, "replace")
				var c2 tea.Cmd
				if key == "E" {
					m, c2 = m.editOther(m.ws.focus)
				} else {
					m, c2 = m.editDefault(m.ws.focus)
				}
				return m, tea.Batch(cmd, c2)
			}
		}
	}
	mm, cmd := m.updateTree(msg)
	nm := mm.(Model)
	if nm.viewMode == ViewConfig {
		nm.floatExp = false
		nm.previousView = ViewFullNote
		return nm, cmd
	}
	nm.viewMode = ViewFullNote
	if nm.ws == nil || nm.ws.root == nil {
		nm.viewMode = ViewTree
		nm.floatExp = false
	}
	return nm, cmd
}

// --- overlay animation ------------------------------------------------

type overlayTickMsg struct{}

const overlayFrames = 3

func (m *Model) startOverlayAnim() tea.Cmd {
	if m.noteCfg().ReduceMotion {
		m.overlayFrame = 0
		return nil
	}
	m.overlayFrame = overlayFrames
	return overlayTick()
}

func overlayTick() tea.Cmd {
	return tea.Tick(45*time.Millisecond, func(time.Time) tea.Msg { return overlayTickMsg{} })
}

// renderFloatExplorer draws the explorer as a centred bordered overlay
// (~80% × 80%, Miller columns + preview) over the notes.
func (m Model) renderFloatExplorer(base string) string {
	pct := 80 - m.overlayFrame*6
	bw := max(30, m.width*pct/100)
	bh := max(8, m.contentHeight()*pct/100)
	inner := m
	inner.floatExp = false
	inner.width = bw - 2
	inner.height = bh - 2 + inner.bottomRows()
	tree := fitHeight(inner.renderTree(), bh-2)
	box := lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(theme.Current.Accent).
		Background(theme.Current.Background).Render(tree)
	return m.overlayCenter(base, box, false)
}
