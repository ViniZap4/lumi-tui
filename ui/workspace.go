package ui

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/vinizap/lumi/tui-client/canvas"
	"github.com/vinizap/lumi/tui-client/config"
	"github.com/vinizap/lumi/tui-client/domain"
	"github.com/vinizap/lumi/tui-client/filesystem"
	"github.com/vinizap/lumi/tui-client/kanban"
	"github.com/vinizap/lumi/tui-client/vim"
)

// The note area is a tree of panes (SPEC-PARTE2 §3). Each pane shows
// one note in a view (reader, source editor, source+preview, kanban
// board, canvas outline). Panes on the same note share one buffer (one
// vim engine = one session/doc); each pane keeps its own cursor.

type paneView int

const (
	viewReader paneView = iota
	viewSource
	viewSplit // source + rendered preview
	viewBoard
	viewCanvas
)

func (v paneView) String() string {
	return [...]string{"leitura", "fonte", "fonte+prévia", "quadro", "canvas"}[v]
}

// noteBuffer is one open note: the editor engine and save state.
type noteBuffer struct {
	path      string
	note      *domain.Note
	eng       *vim.Engine
	savedGen  int
	savedBody string // body as on disk after the last load/save
	isBoard   bool
	isCanvas  bool
	editSeq   int // bumps on every change (autosave debounce)
	saveErr   string
	// lastCanvas is the last valid parse (JSON may be invalid mid-edit).
	lastCanvas *canvas.Canvas
}

func (b *noteBuffer) dirty() bool { return b.eng.Gen() != b.savedGen }

// notePane is one leaf of the layout.
type notePane struct {
	buf  *noteBuffer
	view paneView
	cur  vim.Pos // editor cursor while unfocused
	top  int
	// reader: the read-only prose engine (reader.go); scroll mirrors
	// its top line, link the last Tab-selected link
	rd     *readerDoc
	scroll int
	link   int // focused link (-1: none)
	// source+preview: the preview's read-only prose engine
	pv *readerDoc
	// source+preview: the preview has keyboard focus
	previewFocus bool
	// board: bedit is the Edit mode (SPEC-PARTE2 §5a); false is Reading,
	// where nothing changes the note
	bedit       bool
	bcol, bcard int
	binput      string // "", "card", "edit", "column", "filter"
	bvalue      string
	bfilter     string
	bconfirm    bool
	bpending    string
	// canvas
	crow int
}

type layoutNode struct {
	dir    byte // 0 leaf, 'v' side by side, 'h' stacked
	pane   *notePane
	kids   []*layoutNode
	parent *layoutNode
}

type workspace struct {
	root  *layoutNode
	focus *notePane
	prev  *notePane
	// zoom is the pane filling the note area (⌃w z); nil: none. Never
	// persisted.
	zoom *notePane
	bufs map[string]*noteBuffer
}

func (w *workspace) leaves() []*notePane {
	var out []*notePane
	var walk func(n *layoutNode)
	walk = func(n *layoutNode) {
		if n == nil {
			return
		}
		if n.dir == 0 {
			out = append(out, n.pane)
			return
		}
		for _, k := range n.kids {
			walk(k)
		}
	}
	walk(w.root)
	return out
}

func (w *workspace) nodeOf(p *notePane) *layoutNode {
	var found *layoutNode
	var walk func(n *layoutNode)
	walk = func(n *layoutNode) {
		if n == nil || found != nil {
			return
		}
		if n.dir == 0 && n.pane == p {
			found = n
			return
		}
		for _, k := range n.kids {
			walk(k)
		}
	}
	walk(w.root)
	return found
}

// --- engine options --------------------------------------------------

// systemClipboard is the clipboard the editor uses (TestMain swaps in a
// memory clipboard; production: OS clipboard with an OSC 52 fallback).
var systemClipboard vim.Clipboard = newTermClipboard()

// vimrcCache keeps the parsed vimrc between buffers.
type vimrcState struct {
	loaded bool
	rc     vim.Vimrc
	err    string
}

var vimrcCache vimrcState

// loadVimrc parses ~/.config/lumi/vimrc (safe subset; never executed).
func loadVimrc(base vim.Options) vim.Vimrc {
	path := config.VimrcPath()
	data, err := os.ReadFile(path)
	if err != nil {
		rc := vim.Vimrc{Options: base}
		return rc
	}
	if len(data) > vim.MaxVimrcBytes*2 {
		data = data[:vim.MaxVimrcBytes*2]
	}
	return vim.ParseVimrc(string(data), base)
}

func (m Model) engineOptions() vim.Options {
	cfg := m.noteCfg()
	o := vim.DefaultOptions()
	o.EscapeSeqs = cfg.EscapeSequences
	o.EscapeTimeout = time.Duration(cfg.EscapeTimeoutMs) * time.Millisecond
	o.ClipboardUnnamed = cfg.ClipboardSync
	o.Number = cfg.ShowLineNumbers
	if cfg.Vimrc {
		rc := loadVimrc(o)
		vimrcCache = vimrcState{loaded: true, rc: rc}
		o = rc.Options
		if rc.Set["clipboard"] {
			o.ClipboardUnnamed = rc.Options.ClipboardUnnamed
		}
	}
	o.HostMaps = map[string]string{"<leader>e": "explorer", "<leader>z": "zen"}
	return o
}

// noteCfg is the cached config (reloaded when settings change).
func (m Model) noteCfg() *config.Config {
	if m.cfg != nil {
		return m.cfg
	}
	return config.Load()
}

// --- buffers ---------------------------------------------------------

func isCanvasPath(p string) bool { return strings.HasSuffix(strings.ToLower(p), ".canvas") }

func (m *Model) bufferFor(note *domain.Note) (*noteBuffer, error) {
	if m.ws == nil {
		m.ws = &workspace{bufs: map[string]*noteBuffer{}}
	}
	if b, ok := m.ws.bufs[note.Path]; ok {
		return b, nil
	}
	head, body, err := filesystem.ReadRaw(note.Path)
	if err != nil {
		return nil, err
	}
	if isCanvasPath(note.Path) {
		data, _ := os.ReadFile(note.Path)
		body = string(data)
	}
	eng := vim.New(body, m.engineOptions())
	eng.Clip = systemClipboard
	b := &noteBuffer{path: note.Path, note: note, eng: eng, savedGen: eng.Gen(), savedBody: body,
		isBoard: kanban.IsBoard(head), isCanvas: isCanvasPath(note.Path)}
	m.ws.bufs[note.Path] = b
	return b, nil
}

func (m Model) defaultView(b *noteBuffer) paneView {
	switch {
	case b.isCanvas:
		return viewCanvas
	case b.isBoard:
		return viewBoard
	case m.noteCfg().OpenIn == "source":
		return viewSource
	}
	return viewReader
}

// openNoteIn opens note in the focused pane ("replace"), a new vertical
// split ("vsplit") or a horizontal split ("hsplit").
func (m *Model) openNoteIn(note *domain.Note, how string) tea.Cmd {
	if note == nil {
		return nil
	}
	b, err := m.bufferFor(note)
	if err != nil {
		return m.showToast("abrir: "+err.Error(), ToastError, 4*time.Second)
	}
	p := &notePane{buf: b, view: m.defaultView(b), link: -1}
	var cmd tea.Cmd
	switch {
	case m.ws.root == nil:
		m.ws.root = &layoutNode{pane: p}
		m.ws.focus = p
	case how == "vsplit" || how == "hsplit":
		m.syncFocusOut()
		dir := byte('v')
		if how == "hsplit" {
			dir = 'h'
		}
		m.splitLeaf(m.ws.focus, p, dir)
	default:
		old := m.ws.focus
		cmd = m.releaseBuffer(old, b)
		n := m.ws.nodeOf(old)
		n.pane = p
		m.ws.focus = p
	}
	m.focusIn(p)
	m.viewMode = ViewFullNote
	m.fullNote = note
	m.saveLayout()
	return cmd
}

// splitLeaf puts p next to target (after it) in direction dir.
func (m *Model) splitLeaf(target, p *notePane, dir byte) {
	n := m.ws.nodeOf(target)
	if n == nil {
		return
	}
	m.unzoom() // a split shows the layout again
	leaf := &layoutNode{pane: p}
	if par := n.parent; par != nil && par.dir == dir {
		for i, k := range par.kids {
			if k == n {
				leaf.parent = par
				par.kids = append(par.kids[:i+1], append([]*layoutNode{leaf}, par.kids[i+1:]...)...)
				break
			}
		}
	} else {
		old := &layoutNode{pane: n.pane, parent: n}
		leaf.parent = n
		n.dir, n.pane, n.kids = dir, nil, []*layoutNode{old, leaf}
	}
	m.ws.prev = target
	m.ws.focus = p
	// the new pane slides open
	if m.mo != nil && !m.mo.reduced {
		m.mo.splitPane = p
		m.mo.to(animSplit, 0, 1)
	}
}

// releaseBuffer saves and forgets old's buffer when no other pane (and
// not the replacement) uses it.
func (m *Model) releaseBuffer(old *notePane, keep *noteBuffer) tea.Cmd {
	if old == nil || old.buf == keep {
		return nil
	}
	for _, p := range m.ws.leaves() {
		if p != old && p.buf == old.buf {
			return nil
		}
	}
	var cmd tea.Cmd
	if old.buf.dirty() {
		cmd = m.saveBuffer(old.buf)
	}
	delete(m.ws.bufs, old.buf.path)
	return cmd
}

// focusIn loads p's cursor into its engine; syncFocusOut stores the
// focused pane's cursor (panes on one note keep separate cursors).
func (m *Model) focusIn(p *notePane) {
	if p == nil {
		return
	}
	e := p.buf.eng
	if e.Mode() != vim.ModeNormal {
		e.Feed("esc")
		e.Feed("esc")
	}
	e.SetCursor(p.cur)
	e.Top = p.top
	m.fullNote = p.buf.note
}

func (m *Model) syncFocusOut() {
	if m.ws == nil || m.ws.focus == nil {
		return
	}
	p := m.ws.focus
	e := p.buf.eng
	if e.Mode() == vim.ModeInsert || e.Mode() == vim.ModeReplace || e.Mode().IsVisual() || e.Mode() == vim.ModeCmdline {
		e.Feed("esc")
		e.Feed("esc")
	}
	p.cur, p.top = e.Cursor(), e.Top
}

func (m *Model) setFocus(p *notePane) {
	if m.ws == nil || p == nil || p == m.ws.focus {
		return
	}
	m.unzoom() // moving to another pane shows the layout again
	m.syncFocusOut()
	m.ws.prev = m.ws.focus
	m.ws.focus = p
	m.focusIn(p)
}

// closePane closes the focused pane; the last pane leaves the empty
// home with the floating explorer open at that note.
func (m *Model) closePane() tea.Cmd {
	if m.ws == nil || m.ws.focus == nil {
		return nil
	}
	p := m.ws.focus
	m.unzoom()
	cmd := m.releaseBuffer(p, nil)
	n := m.ws.nodeOf(p)
	par := n.parent
	if par == nil {
		m.ws.root, m.ws.focus = nil, nil
		m.viewMode = ViewHome
		m.clearFilter()
		m.pane = PaneList
		m.openOverlay()
		var nav tea.Cmd
		if p.buf != nil {
			nav = m.navigateTo(filepath.Dir(p.buf.path), 0)
			m.pendingSelect = p.buf.path
		}
		m.saveLayout()
		return tea.Batch(cmd, nav, m.loadItems, m.loadRecent)
	}
	for i, k := range par.kids {
		if k == n {
			par.kids = append(par.kids[:i], par.kids[i+1:]...)
			break
		}
	}
	if len(par.kids) == 1 {
		only := par.kids[0]
		par.dir, par.pane, par.kids = only.dir, only.pane, only.kids
		for _, k := range par.kids {
			k.parent = par
		}
	}
	next := m.ws.prev
	if next == nil || m.ws.nodeOf(next) == nil {
		next = m.ws.leaves()[0]
	}
	m.ws.focus = nil
	m.ws.prev = nil
	m.ws.focus = next
	m.focusIn(next)
	m.saveLayout()
	return cmd
}

// onlyPane keeps the focused pane (Ctrl-w o).
func (m *Model) onlyPane() tea.Cmd {
	if m.ws == nil || m.ws.focus == nil {
		return nil
	}
	var cmds []tea.Cmd
	keep := m.ws.focus
	for _, p := range m.ws.leaves() {
		if p != keep && p.buf != keep.buf {
			if _, open := m.ws.bufs[p.buf.path]; open && p.buf.dirty() {
				cmds = append(cmds, m.saveBuffer(p.buf))
			}
			delete(m.ws.bufs, p.buf.path)
		}
	}
	m.ws.root = &layoutNode{pane: keep}
	m.ws.prev, m.ws.zoom = nil, nil
	m.saveLayout()
	return tea.Batch(cmds...)
}

// --- saving ----------------------------------------------------------

// autosaveMsg fires after the autosave debounce.
type autosaveMsg struct {
	path string
	seq  int
}

// changed records an edit and returns the autosave tick.
func (m *Model) changed(b *noteBuffer) tea.Cmd {
	b.editSeq++
	cfg := m.noteCfg()
	if !cfg.Autosave {
		return nil
	}
	path, seq := b.path, b.editSeq
	d := time.Duration(cfg.AutosaveMs) * time.Millisecond
	return tea.Tick(d, func(time.Time) tea.Msg { return autosaveMsg{path: path, seq: seq} })
}

// saveNow is the clock for saves (tests pin it).
var saveNow = time.Now

// saveBuffer writes the buffer through the same path as an external
// edit: the file is written (frontmatter kept byte for byte), then the
// feed manager pushes it 3-way with the epoch check (CRDT §7.1).
// When the file changed on disk since it was loaded (a pull from the
// server), the edits are merged when they do not overlap; otherwise
// this version is kept as a conflict copy and the disk version loaded.
func (m *Model) saveBuffer(b *noteBuffer) tea.Cmd {
	if b == nil {
		return nil
	}
	body := b.eng.Text()
	if b.isCanvas {
		return m.saveCanvas(b, body)
	}
	_, disk, err := filesystem.ReadRaw(b.path)
	if err != nil && !os.IsNotExist(err) {
		b.saveErr = err.Error()
		return m.showToast("salvar: "+err.Error(), ToastError, 4*time.Second)
	}
	var notice tea.Cmd
	if err == nil && disk != b.savedBody && disk != body {
		if merged, ok := merge3(b.savedBody, body, disk); ok {
			body = merged
			cur := b.eng.Cursor()
			b.eng.SetText(merged)
			b.eng.SetCursor(cur)
			notice = m.showToast("nota mudou no servidor — edições combinadas", ToastInfo, 3*time.Second)
		} else {
			cp, cerr := writeConflictCopy(b.path, body)
			if cerr != nil {
				return m.showToast("conflito: "+cerr.Error(), ToastError, 5*time.Second)
			}
			b.eng.SetText(disk)
			b.savedBody, b.savedGen = disk, b.eng.Gen()
			return m.showToast("conflito: sua versão ficou em "+filepath.Base(cp), ToastError, 6*time.Second)
		}
	}
	if body == b.savedBody && err == nil && disk == body {
		b.savedGen = b.eng.Gen()
		return notice
	}
	if err := filesystem.WriteBody(b.path, body, true, saveNow()); err != nil {
		b.saveErr = err.Error()
		return m.showToast("salvar: "+err.Error(), ToastError, 4*time.Second)
	}
	b.saveErr = ""
	b.savedBody, b.savedGen = body, b.eng.Gen()
	if b.note != nil {
		b.note.Content = body
	}
	m.localEdit()
	return notice
}

func (m *Model) saveCanvas(b *noteBuffer, text string) tea.Cmd {
	if text == b.savedBody {
		b.savedGen = b.eng.Gen()
		return nil
	}
	var warn tea.Cmd
	if _, err := canvas.Parse([]byte(text)); err != nil {
		// the server tolerates invalid JSON mid-edit; say so and save
		warn = m.showToast("canvas com JSON inválido (salvo assim mesmo)", ToastInfo, 3*time.Second)
	}
	if err := atomicWrite(b.path, []byte(text)); err != nil {
		return m.showToast("salvar: "+err.Error(), ToastError, 4*time.Second)
	}
	b.savedBody, b.savedGen = text, b.eng.Gen()
	m.localEdit()
	return warn
}

func atomicWrite(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".lumi-*")
	if err != nil {
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return err
	}
	tmp.Close()
	if info, err := os.Stat(path); err == nil {
		os.Chmod(tmp.Name(), info.Mode().Perm())
	}
	return os.Rename(tmp.Name(), path)
}

func writeConflictCopy(path, body string) (string, error) {
	dir, base := filepath.Dir(path), strings.TrimSuffix(filepath.Base(path), ".md")
	for i := 1; i < 100; i++ {
		name := fmt.Sprintf("%s (conflito %d).md", base, i)
		if i == 1 {
			name = base + " (conflito).md"
		}
		p := filepath.Join(dir, name)
		if _, err := os.Stat(p); os.IsNotExist(err) {
			return p, atomicWrite(p, []byte(strings.TrimRight(body, "\n")+"\n"))
		}
	}
	return "", fmt.Errorf("too many conflict copies")
}

// merge3 merges ours and theirs (both edited from base) when their
// changed regions (common prefix/suffix against base) do not overlap.
func merge3(base, ours, theirs string) (string, bool) {
	if ours == base {
		return theirs, true
	}
	if theirs == base || theirs == ours {
		return ours, true
	}
	oa, ob := changedRange(base, ours)
	ta, tb := changedRange(base, theirs)
	if (oa < tb && ta < ob) || (oa == ob && ta == tb && oa == ta) {
		return "", false
	}
	oIns := ours[oa : len(ours)-(len(base)-ob)]
	tIns := theirs[ta : len(theirs)-(len(base)-tb)]
	if oa >= tb {
		r := base[:oa] + oIns + base[ob:]
		return r[:ta] + tIns + r[tb:], true
	}
	r := base[:ta] + tIns + base[tb:]
	return r[:oa] + oIns + r[ob:], true
}

// changedRange is [a, b) in base that differs from s.
func changedRange(base, s string) (int, int) {
	a := 0
	for a < len(base) && a < len(s) && base[a] == s[a] {
		a++
	}
	bb, sb := len(base), len(s)
	for bb > a && sb > a && base[bb-1] == s[sb-1] {
		bb--
		sb--
	}
	return a, bb
}

// --- layout persistence (per book, per device) ------------------------

type savedPane struct {
	Dir  string      `json:"dir,omitempty"`
	Kids []savedPane `json:"kids,omitempty"`
	Path string      `json:"path,omitempty"`
	View int         `json:"view,omitempty"`
	Line int         `json:"line,omitempty"`
}

type savedLayout struct {
	Root  savedPane `json:"root"`
	Focus string    `json:"focus"`
}

func layoutFile(root string) string {
	d, err := os.UserCacheDir()
	if err != nil {
		return ""
	}
	h := sha256.Sum256([]byte(filepath.Clean(root)))
	return filepath.Join(d, "lumi", "layout", hex.EncodeToString(h[:8])+".json")
}

func (m *Model) saveLayout() {
	if m.ws == nil {
		return
	}
	f := layoutFile(m.rootDir)
	if f == "" {
		return
	}
	if m.ws.root == nil || len(m.ws.leaves()) < 2 {
		os.Remove(f)
		return
	}
	var conv func(n *layoutNode) savedPane
	conv = func(n *layoutNode) savedPane {
		if n.dir == 0 {
			rel, _ := filepath.Rel(m.rootDir, n.pane.buf.path)
			return savedPane{Path: filepath.ToSlash(rel), View: int(n.pane.view), Line: n.pane.cur.Line}
		}
		sp := savedPane{Dir: string(n.dir)}
		for _, k := range n.kids {
			sp.Kids = append(sp.Kids, conv(k))
		}
		return sp
	}
	sl := savedLayout{Root: conv(m.ws.root)}
	if m.ws.focus != nil {
		sl.Focus, _ = filepath.Rel(m.rootDir, m.ws.focus.buf.path)
	}
	data, _ := json.Marshal(sl)
	os.MkdirAll(filepath.Dir(f), 0o700)
	_ = os.WriteFile(f, data, 0o600)
}

// restoreLayout rebuilds the saved layout when it holds focusPath (or
// any note when focusPath is ""). ok=false: nothing restored.
func (m *Model) restoreLayout(focusPath string) bool {
	data, err := os.ReadFile(layoutFile(m.rootDir))
	if err != nil {
		return false
	}
	var sl savedLayout
	if json.Unmarshal(data, &sl) != nil {
		return false
	}
	if focusPath != "" {
		rel, _ := filepath.Rel(m.rootDir, focusPath)
		if !layoutHas(sl.Root, filepath.ToSlash(rel)) {
			return false
		}
		sl.Focus = rel
	}
	m.ws = &workspace{bufs: map[string]*noteBuffer{}}
	var build func(sp savedPane, parent *layoutNode) *layoutNode
	build = func(sp savedPane, parent *layoutNode) *layoutNode {
		if sp.Dir == "" {
			abs := filepath.Join(m.rootDir, filepath.FromSlash(sp.Path))
			if !pathIsInsideVault(m.rootDir, abs) {
				return nil
			}
			note, err := filesystem.ReadNote(abs)
			if err != nil {
				return nil
			}
			b, err := m.bufferFor(note)
			if err != nil {
				return nil
			}
			v := paneView(sp.View)
			if v < viewReader || v > viewCanvas {
				v = m.defaultView(b)
			}
			p := &notePane{buf: b, view: v, link: -1, cur: vim.Pos{Line: sp.Line}}
			if filepath.ToSlash(sp.Path) == filepath.ToSlash(sl.Focus) && m.ws.focus == nil {
				m.ws.focus = p
			}
			return &layoutNode{pane: p, parent: parent}
		}
		n := &layoutNode{dir: sp.Dir[0], parent: parent}
		for _, k := range sp.Kids {
			if c := build(k, n); c != nil {
				n.kids = append(n.kids, c)
			}
		}
		switch len(n.kids) {
		case 0:
			return nil
		case 1:
			c := n.kids[0]
			c.parent = parent
			return c
		}
		return n
	}
	m.ws.root = build(sl.Root, nil)
	if m.ws.root == nil {
		m.ws = nil
		return false
	}
	if m.ws.focus == nil {
		m.ws.focus = m.ws.leaves()[0]
	}
	m.focusIn(m.ws.focus)
	m.viewMode = ViewFullNote
	return true
}

func layoutHas(sp savedPane, rel string) bool {
	if sp.Dir == "" {
		return sp.Path == rel
	}
	for _, k := range sp.Kids {
		if layoutHas(k, rel) {
			return true
		}
	}
	return false
}

func vimPos(line, col int) vim.Pos { return vim.Pos{Line: line, Col: col} }

// FlushEdits saves every unsaved built-in edit (main calls it when the
// program ends, whatever the exit path). The push happens on the next
// sync; the file is on disk now.
func (m Model) FlushEdits() {
	if m.ws == nil {
		return
	}
	for _, b := range m.ws.bufs {
		if b.dirty() {
			m.bookMgr = nil // no background push while exiting
			m.saveBuffer(b)
		}
	}
}
