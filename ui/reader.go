package ui

import (
	"fmt"
	"regexp"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-runewidth"
	"github.com/vinizap/lumi/tui-client/theme"
	"github.com/vinizap/lumi/tui-client/vim"
)

// The prose reader (and the focused preview of source+preview) is a
// read-only vim buffer over the rendered text: the same engine as the
// built-in editor with Options.ReadOnly, so h j k l w b e 0 $ gg G,
// counts, / ? n N * #, f t, v V ⌃v, y yy yw … behave exactly as in the
// editor, and every change (p, x, d, i, u, :s …) is refused with E21.
// Yanks go to the system clipboard (OSC 52 fallback) and copy what the
// reader shows. On top of the engine the reader keeps its host keys:
//
//	⏎ / gf    follow the link under the cursor (on a task: toggle it)
//	⌃w f / F  follow it in a vertical / horizontal split (also S / s)
//	gx        open an external link in the browser; inside a mermaid
//	          diagram, open the note in the Lumi app / web client
//	Tab ⇧Tab  jump to the next / previous link
//	K         link card
//	x         toggle the task under the cursor (a link: open it)
//	d u       half page down / up (less-style; the buffer is read-only)
//	i E       built-in editor / the other editor
//
// Mouse: a click places the cursor (on a link it follows the link;
// Ctrl-click opens it in a vertical split, Alt-click horizontal), the
// wheel scrolls and keeps the cursor in view.

// readerIndent / previewIndent are the left margins (columns).
const (
	readerIndent  = 2
	previewIndent = 1
)

// readerDoc is the rendered note plus its read-only engine.
type readerDoc struct {
	key   string
	lines []string // styled rendered lines
	plain [][]rune // the same lines without escapes (the engine text)
	pos   []linkPos
	links []noteLink
	// tasks maps a rendered line that starts a task item to the source
	// line of that task (-1: not a task line).
	tasks []int
	// mermaid holds the [first, last] rendered lines of each mermaid block.
	mermaid [][2]int
	eng     *vim.Engine
}

// readerDoc is the reader view's document at the pane's current size,
// and the body height. Rebuilt only when the note, the width or the
// theme changed (the cursor, search and registers survive).
func (m Model) readerDoc(p *notePane) (*readerDoc, int) {
	_, w, h := m.paneRect(p)
	p.rd = m.buildDoc(p, p.rd, readerWidth(w), true)
	return p.rd, max(1, h-1)
}

// previewDoc is the source+preview's rendered side.
func (m Model) previewDoc(p *notePane) (*readerDoc, int) {
	_, w, h := m.paneRect(p)
	rw := w - w/2 - 1
	p.pv = m.buildDoc(p, p.pv, max(10, rw-2), false)
	return p.pv, max(1, h-1)
}

func (m Model) buildDoc(p *notePane, old *readerDoc, width int, images bool) *readerDoc {
	t := theme.Current
	key := fmt.Sprintf("%s|%d|%d|%v|%s|%s", p.buf.path, p.buf.eng.Gen(), width, images, t.Background, t.Text)
	if old != nil && old.key == key {
		return old
	}
	body := p.buf.eng.Text()
	md, imgs := body, []string(nil)
	if images {
		md, imgs = imagePlaceholders(body, p.buf.path)
	}
	lines := strings.Split(strings.TrimRight(m.prose.render(md, width), "\n"), "\n")
	if len(imgs) > 0 {
		lines = expandImages(lines, imgs, width)
	}
	d := &readerDoc{key: key, lines: lines, plain: make([][]rune, len(lines))}
	texts := make([]string, len(lines))
	for i, l := range lines {
		// glamour pads lines to the wrap width: the padding is not text
		texts[i] = strings.TrimRight(stripAnsi(l), " ")
		d.plain[i] = []rune(texts[i])
	}
	d.links = findLinks(body)
	d.pos = linkPositions(d.plain, d.links)
	d.tasks = mapTasks(d.plain, body)
	d.mermaid = mermaidBlocks(d.plain)
	text := strings.Join(texts, "\n")
	if old != nil {
		// same engine: cursor, marks, search and registers survive
		cur, top := old.eng.Cursor(), old.eng.Top
		old.eng.SetText(text)
		old.eng.SetCursor(cur)
		old.eng.Top = top
		d.eng = old.eng
		return d
	}
	o := m.engineOptions()
	o.ReadOnly = true
	o.Wrap = false
	o.Number, o.RelativeNumber = false, false
	o.ClipboardUnnamed = true // y copies to the system clipboard
	e := vim.New(text, o)
	e.Clip = systemClipboard
	d.eng = e
	return d
}

// linkPositions finds where each source link landed in the rendered
// lines (display columns), in reading order.
func linkPositions(plain [][]rune, links []noteLink) []linkPos {
	pos := make([]linkPos, len(links))
	line, col := 0, 0
	for i, l := range links {
		pos[i] = linkPos{line: -1}
		needle := []rune(strings.TrimSpace(l.text))
		if len(needle) == 0 {
			continue
		}
		for ln := line; ln < len(plain); ln++ {
			start := 0
			if ln == line {
				start = col
			}
			if idx := runeIndex(plain[ln], needle, start); idx >= 0 {
				pos[i] = linkPos{line: ln, col: dispWidth(plain[ln][:idx]), w: dispWidth(needle)}
				line, col = ln, idx+len(needle)
				break
			}
		}
	}
	return pos
}

// reTask is a markdown task item ("- [ ] x", "1. [x] y", "> - [ ] z").
var reTask = regexp.MustCompile(`^((?:\s*>)*\s*(?:[-*+]|\d+[.)])\s+\[)([ xX])\]`)

// sourceTasks lists the source lines holding a task (fenced code skipped).
func sourceTasks(body string) []int {
	var out []int
	inFence := false
	for i, l := range strings.Split(body, "\n") {
		t := strings.TrimSpace(l)
		if strings.HasPrefix(t, "```") || strings.HasPrefix(t, "~~~") {
			inFence = !inFence
			continue
		}
		if !inFence && reTask.MatchString(l) {
			out = append(out, i)
		}
	}
	return out
}

// mapTasks pairs the rendered task lines (☐ / ☑ markers) with the
// source tasks in order.
func mapTasks(plain [][]rune, body string) []int {
	src := sourceTasks(body)
	out := make([]int, len(plain))
	k := 0
	for i, l := range plain {
		out[i] = -1
		t := strings.TrimLeft(string(l), " │▌┃>")
		if (strings.HasPrefix(t, "☐ ") || strings.HasPrefix(t, "☑ ")) && k < len(src) {
			out[i] = src[k]
			k++
		}
	}
	return out
}

func dispWidth(rs []rune) int {
	w := 0
	for _, r := range rs {
		w += runewidth.RuneWidth(r)
	}
	return w
}

// dispCol is the display column of rune column c (cutVisual's widths).
func dispCol(l []rune, c int) int { return dispWidth(l[:min(max(0, c), len(l))]) }

// colAt is the rune column at display column d.
func colAt(l []rune, d int) int {
	w := 0
	for i, r := range l {
		cw := runewidth.RuneWidth(r)
		if d < w+cw {
			return i
		}
		w += cw
	}
	return max(0, len(l)-1)
}

func cellWidth(l []rune, c int) int {
	if c < 0 || c >= len(l) {
		return 1
	}
	return max(1, runewidth.RuneWidth(l[c]))
}

// linkAtCursor is the index of the link under the doc's cursor.
func (d *readerDoc) linkAtCursor() int {
	c := d.eng.Cursor()
	if c.Line >= len(d.plain) {
		return -1
	}
	dc := dispCol(d.plain[c.Line], c.Col)
	return d.linkAt(c.Line, dc)
}

func (d *readerDoc) linkAt(line, dc int) int {
	for i, lp := range d.pos {
		if lp.line == line && dc >= lp.col && dc < lp.col+lp.w {
			return i
		}
	}
	return -1
}

// cursorTo puts the cursor on rendered line l, display column dc, and
// scrolls it into a body of h rows.
func (d *readerDoc) cursorTo(l, dc, h int) {
	if l < 0 || l >= len(d.plain) {
		return
	}
	d.eng.SetCursor(vim.Pos{Line: l, Col: colAt(d.plain[l], dc)})
	d.eng.Height = h
	if l < d.eng.Top || l >= d.eng.Top+h {
		d.eng.Top = clamp(l-h/3, 0, max(0, len(d.plain)-h))
	}
}

// scrollBy moves the view by n rows and keeps the cursor inside it.
func (d *readerDoc) scrollBy(n, h int) {
	e := d.eng
	e.Top = clamp(e.Top+n, 0, max(0, len(d.plain)-h))
	c := e.Cursor()
	if l := clamp(c.Line, e.Top, e.Top+h-1); l != c.Line {
		e.SetCursor(vim.Pos{Line: min(l, len(d.plain)-1), Col: c.Col})
	}
}

// --- keys ------------------------------------------------------------

func (m Model) updateReader(key string) (tea.Model, tea.Cmd) {
	p := m.ws.focus
	d, h := m.readerDoc(p)
	return m.updateProse(p, d, key, h)
}

// updatePreviewFocus: the split preview has the keyboard; it reads like
// the reader. Esc / q / ⌃h return to the editor side.
func (m Model) updatePreviewFocus(key string) (tea.Model, tea.Cmd) {
	p := m.ws.focus
	d, h := m.previewDoc(p)
	if d.eng.Mode() == vim.ModeNormal && d.eng.Pending() == "" && m.notePrefix == "" {
		switch key {
		case "q", "esc", "ctrl+h":
			p.previewFocus = false
			return m, nil
		}
	}
	return m.updateProse(p, d, key, h)
}

// updateProse runs one key in a read-only prose document.
func (m Model) updateProse(p *notePane, d *readerDoc, key string, h int) (tea.Model, tea.Cmd) {
	e := d.eng
	e.Height = h
	e.ClearMessage()
	idle := e.Mode() == vim.ModeNormal && e.Pending() == ""
	if idle {
		if mm, cmd, ok := m.proseHostKey(p, d, key, h); ok {
			return mm, cmd
		}
	}
	return m.feedProse(p, d, key)
}

// feedProse gives the key to the read-only engine and runs its effects.
func (m Model) feedProse(p *notePane, d *readerDoc, keys ...string) (Model, tea.Cmd) {
	e := d.eng
	var effs []vim.Effect
	for _, k := range keys {
		effs = append(effs, e.Feed(k)...)
	}
	var cmds []tea.Cmd
	if _, _, _, ok := e.Flash(time.Second); ok {
		if r := e.Register('"'); r.Text != "" {
			m.statusMsg = yankSummary(r)
		}
		cmds = append(cmds, tea.Tick(yankFlashFor+20*time.Millisecond, func(t time.Time) tea.Msg { return yankFlashMsg(t) }))
	}
	if p.view == viewReader {
		p.scroll = e.Top
	}
	mm := m
	for _, ef := range effs {
		var cmd tea.Cmd
		switch ef.Kind {
		case vim.EffQuit, vim.EffWriteQuit:
			if p.view == viewReader {
				mm.syncFocusOut()
				cmd = mm.closePane()
				break
			}
			mm, cmd = mm.runEffect(p, ef)
		default:
			mm, cmd = mm.runEffect(p, ef)
		}
		cmds = append(cmds, cmd)
	}
	return mm, tea.Batch(cmds...)
}

func yankSummary(r vim.Register) string {
	n := strings.Count(r.Text, "\n")
	if r.Linewise {
		if n <= 1 {
			return "1 linha copiada"
		}
		return fmt.Sprintf("%d linhas copiadas", n)
	}
	return fmt.Sprintf("%d caracteres copiados", len([]rune(r.Text)))
}

// proseHostKey handles the reader's own keys (links, tasks, panes,
// views). ok=false: the key goes to the engine.
func (m Model) proseHostKey(p *notePane, d *readerDoc, key string, h int) (Model, tea.Cmd, bool) {
	if pre := m.notePrefix; pre != "" {
		switch pre + key {
		case "gf":
			m.notePrefix = ""
			mm, cmd := m.followCursorLink(p, d, "replace")
			return mm, cmd, true
		case "gx":
			m.notePrefix = ""
			if i := d.linkAtCursor(); i >= 0 && d.links[i].external {
				openURLFn(d.links[i].target)
				m.statusMsg = "aberto no navegador: " + truncate(d.links[i].target, 40)
			} else if d.inMermaid(d.eng.Cursor().Line) {
				return m, m.openNoteInApp(p), true
			} else {
				m.statusMsg = "nenhum link externo no cursor"
			}
			return m, nil, true
		case "zv", "gs", "gb", "ga":
			mm, cmd, ok := m.commonNoteKey(key)
			return mm, cmd, ok
		}
		if pre == m.leaderKey() {
			mm, cmd, ok := m.commonNoteKey(key)
			return mm, cmd, ok
		}
		// gg, ge, g_, zz, zt, zb … are the engine's
		m.notePrefix = ""
		mm, cmd := m.feedProse(p, d, pre, key)
		return mm, cmd, true
	}
	switch key {
	case "enter":
		if i := d.linkAtCursor(); i >= 0 {
			mm, cmd := m.followLink(p, d.links[i], "replace")
			return mm, cmd, true
		}
		if mm, cmd, ok := m.toggleTaskAtCursor(p, d); ok {
			return mm, cmd, true
		}
		return m, nil, false // vim: next line
	case "x":
		if mm, cmd, ok := m.toggleTaskAtCursor(p, d); ok {
			return mm, cmd, true
		}
		if i := d.linkAtCursor(); i >= 0 && d.links[i].external {
			openURLFn(d.links[i].target)
			m.statusMsg = "aberto no navegador: " + truncate(d.links[i].target, 40)
			return m, nil, true
		}
		m.statusMsg = "x marca uma tarefa (somente leitura)"
		return m, nil, true
	case "s", "S":
		how := "hsplit"
		if key == "S" {
			how = "vsplit"
		}
		if i := d.linkAtCursor(); i >= 0 {
			mm, cmd := m.followLink(p, d.links[i], how)
			return mm, cmd, true
		}
		m.statusMsg = "nenhum link no cursor"
		return m, nil, true
	case "tab", "shift+tab":
		m.cycleLink(p, d, key == "tab", h)
		return m, nil, true
	case "K":
		if i := d.linkAtCursor(); i >= 0 {
			mm, cmd := m.openHover(p, d.links[i])
			return mm, cmd, true
		}
		m.statusMsg = "nenhum link no cursor (Tab vai ao próximo)"
		return m, nil, true
	case "d", "u":
		k := map[string]string{"d": "ctrl+d", "u": "ctrl+u"}[key]
		mm, cmd := m.feedProse(p, d, k)
		return mm, cmd, true
	case "esc":
		p.link = -1
		m.statusMsg = ""
		d.eng.FeedKeys(":noh<CR>")
		return m, nil, true
	case "e", "/", "n", "N", "*", "#":
		return m, nil, false // the engine's motion / search
	}
	mm, cmd, ok := m.commonNoteKey(key)
	return mm, cmd, ok
}

// followCursorLink follows the link under the cursor.
func (m Model) followCursorLink(p *notePane, d *readerDoc, how string) (Model, tea.Cmd) {
	if i := d.linkAtCursor(); i >= 0 {
		return m.followLink(p, d.links[i], how)
	}
	m.statusMsg = "nenhum link no cursor"
	return m, nil
}

// cycleLink moves the cursor to the next (or previous) link after it.
func (m *Model) cycleLink(p *notePane, d *readerDoc, fwd bool, h int) {
	var idx []int
	for i, lp := range d.pos {
		if lp.line >= 0 {
			idx = append(idx, i)
		}
	}
	if len(idx) == 0 {
		m.statusMsg = "sem links nesta nota"
		return
	}
	c := d.eng.Cursor()
	dc := 0
	if c.Line < len(d.plain) {
		dc = dispCol(d.plain[c.Line], c.Col)
	}
	before := func(i int) bool { // link i starts before the cursor
		lp := d.pos[i]
		return lp.line < c.Line || lp.line == c.Line && lp.col <= dc
	}
	pick := -1
	if fwd {
		for _, i := range idx {
			lp := d.pos[i]
			if lp.line > c.Line || lp.line == c.Line && lp.col > dc {
				pick = i
				break
			}
		}
		if pick < 0 {
			pick = idx[0]
		}
	} else {
		cur := d.linkAtCursor()
		for k := len(idx) - 1; k >= 0; k-- {
			if i := idx[k]; before(i) && i != cur {
				pick = i
				break
			}
		}
		if pick < 0 {
			pick = idx[len(idx)-1]
		}
	}
	lp := d.pos[pick]
	d.cursorTo(lp.line, lp.col, h)
	p.link = pick
	if p.view == viewReader {
		p.scroll = d.eng.Top
	}
	m.statusMsg = fmt.Sprintf("link %d/%d: %s", pick+1, len(d.links), truncate(d.links[pick].target, 50))
}

// toggleTaskAtCursor flips the task box on the cursor's line: one
// character of the source changes, saved through the normal save path
// (frontmatter kept, 3-way push).
func (m Model) toggleTaskAtCursor(p *notePane, d *readerDoc) (Model, tea.Cmd, bool) {
	c := d.eng.Cursor()
	if c.Line >= len(d.tasks) {
		return m, nil, false
	}
	src := d.tasks[c.Line]
	if src < 0 {
		return m, nil, false
	}
	cmd, ok := m.toggleTask(p.buf, src)
	if !ok {
		return m, nil, false
	}
	return m, cmd, true
}

// toggleTask flips "[ ]" ↔ "[x]" on source line l of b and saves.
func (m *Model) toggleTask(b *noteBuffer, l int) (tea.Cmd, bool) {
	lines := strings.Split(b.eng.Text(), "\n")
	if l < 0 || l >= len(lines) {
		return nil, false
	}
	loc := reTask.FindStringSubmatchIndex(lines[l])
	if loc == nil {
		return nil, false
	}
	at := loc[4] // the box character
	mark, done := "x", true
	if lines[l][at] != ' ' {
		mark, done = " ", false
	}
	lines[l] = lines[l][:at] + mark + lines[l][at+1:]
	cur := b.eng.Cursor()
	b.eng.SetText(strings.Join(lines, "\n"))
	b.eng.SetCursor(cur)
	cmd := m.saveBuffer(b)
	if done {
		m.statusMsg = "tarefa concluída"
	} else {
		m.statusMsg = "tarefa reaberta"
	}
	return cmd, true
}

// --- rendering -------------------------------------------------------

// renderProse draws h rows of d starting at the engine's top, with the
// cursor, the visual selection, the yank flash, search matches and the
// link under the cursor. indent is the left margin.
func (m Model) renderProse(d *readerDoc, h, indent int, focused bool) []string {
	t := theme.Current
	e := d.eng
	e.Height = h
	n := len(d.lines)
	cur := e.Cursor()
	top := e.Top
	if focused {
		if cur.Line < top {
			top = cur.Line
		}
		if cur.Line >= top+h {
			top = cur.Line - h + 1
		}
	}
	top = clamp(top, 0, max(0, n-h))
	e.Top = top
	selS, selE, selMode, toEOL, selOK := e.Selection()
	flS, flE, flMode, flOK := e.Flash(yankFlashFor)
	link := -1
	if focused {
		link = d.linkAtCursor()
	}
	selSt := lipgloss.NewStyle().Background(visualSelBg).Foreground(t.Text)
	flashSt := lipgloss.NewStyle().Background(yankFlashBg).Foreground(t.Text)
	matchSt := lipgloss.NewStyle().Background(t.Warning).Foreground(t.Background)
	linkSt := lipgloss.NewStyle().Foreground(t.Accent).Underline(true).Bold(true)
	curSt := lipgloss.NewStyle().Background(primaryColor).Foreground(t.Background)
	pad := strings.Repeat(" ", indent)
	out := make([]string, 0, h)
	for i := top; i < n && len(out) < h; i++ {
		l := d.lines[i]
		pl := d.plain[i]
		lw := dispWidth(pl)
		for _, mt := range e.MatchesInLine(i) {
			l = overlayCells(l, dispCol(pl, mt[0]), dispCol(pl, mt[1]), matchSt)
		}
		if link >= 0 && d.pos[link].line == i {
			lp := d.pos[link]
			l = overlayCells(l, lp.col, lp.col+lp.w, linkSt)
		}
		if flOK {
			if a, b, ok := selCells(pl, lw, i, flS, flE, flMode, false); ok {
				l = overlayCells(l, a, b, flashSt)
			}
		}
		if focused && selOK {
			if a, b, ok := selCells(pl, lw, i, selS, selE, selMode, toEOL); ok {
				l = overlayCells(l, a, b, selSt)
			}
		}
		if focused && i == cur.Line {
			a := dispCol(pl, cur.Col)
			l = overlayCells(l, a, a+cellWidth(pl, cur.Col), curSt)
		}
		out = append(out, pad+l)
	}
	return out
}

// selCells is the display range [a, b) a selection covers on line i.
func selCells(pl []rune, lw, i int, s, en vim.Pos, mode vim.Mode, toEOL bool) (int, int, bool) {
	if i < s.Line || i > en.Line {
		return 0, 0, false
	}
	full := max(1, lw)
	switch mode {
	case vim.ModeVisualLine:
		return 0, full, true
	case vim.ModeVisualBlock:
		a, b := dispCol(pl, s.Col), dispCol(pl, en.Col)
		if a > b {
			a, b = b, a
		}
		b += cellWidth(pl, colAt(pl, b))
		if toEOL {
			b = full
		}
		return a, b, true
	}
	a, b := 0, full
	if i == s.Line {
		a = dispCol(pl, s.Col)
	}
	if i == en.Line {
		b = dispCol(pl, en.Col) + cellWidth(pl, en.Col)
	}
	return a, max(a+1, b), true
}

// overlayCells restyles display columns [a, b) of a styled line with st
// (the text keeps its characters; cells past the end are blanks).
func overlayCells(l string, a, b int, st lipgloss.Style) string {
	if b <= a {
		return l
	}
	seg := stripAnsi(cutVisual(l, a, b))
	if w := runewidth.StringWidth(seg); w < b-a {
		seg += strings.Repeat(" ", b-a-w)
	}
	return cutVisual(l, 0, a) + "\x1b[0m" + st.Render(seg) + cutVisual(l, b, 1<<30)
}
