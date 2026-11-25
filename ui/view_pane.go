package ui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/vinizap/lumi/tui-client/canvas"
	"github.com/vinizap/lumi/tui-client/image"
	"github.com/vinizap/lumi/tui-client/theme"
	"github.com/vinizap/lumi/tui-client/vim"
)

type rect struct{ x, y, w, h int }

// layoutRects places every pane in the note area.
func (m Model) layoutRects() map[*notePane]rect {
	out := map[*notePane]rect{}
	if m.ws == nil || m.ws.root == nil {
		return out
	}
	if z := m.zoomed(); z != nil {
		out[z] = rect{0, 0, m.width, m.contentHeight()}
		return out
	}
	var place func(n *layoutNode, r rect)
	place = func(n *layoutNode, r rect) {
		if n.dir == 0 {
			out[n.pane] = r
			return
		}
		for i, cr := range m.childRects(n, r) {
			place(n.kids[i], cr)
		}
	}
	place(m.ws.root, rect{0, 0, m.width, m.contentHeight()})
	return out
}

// childRects splits r between n's children: equal shares, except a
// pane that is sliding open (motion), which grows from zero to its
// share. Side by side children leave a 1-column separator.
func (m Model) childRects(n *layoutNode, r rect) []rect {
	k := len(n.kids)
	weights := make([]float64, k)
	total := 0.0
	for i, c := range n.kids {
		weights[i] = 1
		if a, ok := m.mo.get(animSplit); ok && a.active && c.dir == 0 && c.pane == m.mo.splitPane {
			weights[i] = a.value(0.02, 1)
		}
		total += weights[i]
	}
	out := make([]rect, k)
	if n.dir == 'v' {
		avail := r.w - (k - 1)
		x := r.x
		for i := range n.kids {
			w := int(float64(avail) * weights[i] / total)
			if i == k-1 {
				w = r.x + r.w - x
			}
			out[i] = rect{x, r.y, max(1, w), r.h}
			x += w + 1
		}
		return out
	}
	y := r.y
	for i := range n.kids {
		h := int(float64(r.h) * weights[i] / total)
		if i == k-1 {
			h = r.y + r.h - y
		}
		out[i] = rect{r.x, y, r.w, max(2, h)}
		y += h
	}
	return out
}

// paneRect is p's rectangle (and its width and height).
func (m Model) paneRect(p *notePane) (rect, int, int) {
	r, ok := m.layoutRects()[p]
	if !ok {
		r = rect{0, 0, m.width, m.contentHeight()}
	}
	return r, r.w, r.h
}

// renderWorkspace draws the pane tree.
func (m Model) renderWorkspace() string {
	if m.ws == nil || m.ws.root == nil {
		return ""
	}
	full := rect{0, 0, m.width, m.contentHeight()}
	if zp, zr, ok := m.zoomFrame(); ok {
		// the zoomed pane grows over (or shrinks back into) the layout
		z := m.ws.zoom
		m.ws.zoom = nil
		base := strings.Join(m.renderNode(m.ws.root, full), "\n")
		m.ws.zoom = z
		box := strings.Join(m.renderPane(zp, zr.w, zr.h, zr.x == 0 && zr.y == 0), "\n")
		return overlayBox(base, box, zr.x, zr.y, m.width)
	}
	if z := m.zoomed(); z != nil {
		return strings.Join(m.renderPane(z, full.w, full.h, true), "\n")
	}
	lines := m.renderNode(m.ws.root, full)
	return strings.Join(lines, "\n")
}

func (m Model) renderNode(n *layoutNode, r rect) []string {
	if n.dir == 0 {
		return m.renderPane(n.pane, r.w, r.h, r.x == 0 && r.y == 0)
	}
	rects := m.childRects(n, r)
	if n.dir == 'v' {
		out := make([]string, r.h)
		// Pane focus (DESIGN-LANGUAGE §8 row 7): the divider beside the
		// focused pane is its border, heavy and accent; every other
		// divider rests thin in the separator ink. Not colour alone: the
		// glyph changes too.
		sep := lipgloss.NewStyle().Foreground(theme.Current.Separator).Render("│")
		lit := lipgloss.NewStyle().Foreground(theme.Current.Accent).Render("┃")
		fr, hasFocus := m.layoutRects()[m.ws.focus]
		for i, c := range n.kids {
			block := m.renderNode(c, rects[i])
			sx := rects[i].x - 1
			beside := hasFocus && (fr.x+fr.w == sx || fr.x == sx+1)
			for row := 0; row < r.h; row++ {
				if i > 0 {
					if y := r.y + row; beside && y >= fr.y && y < fr.y+fr.h {
						out[row] += lit
					} else {
						out[row] += sep
					}
				}
				l := ""
				if row < len(block) {
					l = block[row]
				}
				out[row] += padVisual(l, rects[i].w)
			}
		}
		return out
	}
	var out []string
	for i, c := range n.kids {
		out = append(out, m.renderNode(c, rects[i])...)
	}
	return out
}

func fitBlock(lines []string, w, h int) []string {
	out := make([]string, h)
	for i := 0; i < h; i++ {
		l := ""
		if i < len(lines) {
			l = lines[i]
		}
		if lipgloss.Width(l) > w {
			l = cutVisual(l, 0, w)
		}
		out[i] = padVisual(l, w)
	}
	return out
}

// renderPane draws one pane: a header line and the view. The pane at
// the top-left corner carries the "≡ notes" explorer button.
func (m Model) renderPane(p *notePane, w, h int, origin bool) []string {
	t := theme.Current
	focused := m.ws.focus == p
	title := p.buf.note.Title
	if title == "" {
		title = filepath.Base(p.buf.path)
	}
	marks := ""
	if p.buf.dirty() {
		marks = " ●"
	}
	if p.buf.saveErr != "" {
		marks += " ⚠"
	}
	view := p.modeLabel()
	if p.view == viewSplit && p.previewFocus {
		view += " (prévia)"
	}
	right := lipgloss.NewStyle().Foreground(t.TextDim).Render(view + " ")
	if p.buf.note.HadFrontmatter && !p.buf.note.UpdatedAt.IsZero() && w > 60 {
		right = lipgloss.NewStyle().Foreground(t.TextDim).Render(noteDate(p.buf.note.UpdatedAt)+" · ") + right
	}
	ts := lipgloss.NewStyle().Bold(true).Foreground(t.TextDim)
	if focused {
		ts = ts.Foreground(t.Primary)
		if a, ok := m.mo.get(animTitle); ok && a.active && a.pos > 0.35 {
			ts = ts.Foreground(t.Accent) // just opened from the explorer
		}
	}
	k := kindOf(p.buf.path)
	left := kindStyle(k).Render(" "+k.glyph()) + ts.Render(" "+truncate(title, max(4, w-lipgloss.Width(right)-8-notesButtonWidth))) + lipgloss.NewStyle().Foreground(t.Warning).Render(marks)
	if len(p.buf.note.Tags) > 0 && w > 70 {
		var tags []string
		for _, tg := range p.buf.note.Tags {
			tags = append(tags, "#"+tg)
		}
		left += "  " + lipgloss.NewStyle().Foreground(t.Accent).Render(truncate(strings.Join(tags, " "), w/4))
	}
	if m.zoomed() == p {
		right = lipgloss.NewStyle().Foreground(t.Accent).Bold(true).Render("zoom ") + right
	}
	if m.zoomButtonShown(p) && w > 30 {
		right += m.renderZoomButton(p)
	}
	btn := ""
	if origin && w > notesButtonWidth+12 {
		btn = m.renderNotesButton()
	}
	gap := max(1, w-lipgloss.Width(btn)-lipgloss.Width(left)-lipgloss.Width(right))
	header := left + strings.Repeat(" ", gap) + right
	if focused && len(m.ws.leaves()) > 1 {
		header = lipgloss.NewStyle().Underline(true).Render(stripAnsi(header))
		header = lipgloss.NewStyle().Foreground(t.Accent).Render(header)
	}
	header = btn + header
	bodyH := max(1, h-1)
	var body []string
	switch p.view {
	case viewReader:
		body = m.renderReader(p, w, bodyH)
	case viewSource:
		body = m.renderEditor(p, w, bodyH, focused)
	case viewSplit:
		lw := w / 2
		rw := w - lw - 1
		left := fitBlock(m.renderEditor(p, lw, bodyH, focused && !p.previewFocus), lw, bodyH)
		right := fitBlock(m.renderPreview(p, rw, bodyH), rw, bodyH)
		sepStyle := lipgloss.NewStyle().Foreground(t.Separator)
		if p.previewFocus {
			sepStyle = sepStyle.Foreground(t.Accent)
		}
		for i := range left {
			body = append(body, left[i]+sepStyle.Render("│")+right[i])
		}
	case viewBoard:
		body = m.renderBoard(p, w, bodyH)
	case viewCanvas:
		body = m.renderCanvas(p, w, bodyH)
	}
	return fitBlock(append([]string{header}, body...), w, h)
}

// --- reader ----------------------------------------------------------

type linkPos struct{ line, col, w int }

func readerWidth(w int) int { return max(20, min(w-4, 100)) }

func runeIndex(hay, needle []rune, from int) int {
	for i := from; i+len(needle) <= len(hay); i++ {
		match := true
		for j := range needle {
			if hay[i+j] != needle[j] {
				match = false
				break
			}
		}
		if match {
			return i
		}
	}
	return -1
}

// renderReader draws the reader: the prose with its read-only cursor.
func (m Model) renderReader(p *notePane, w, h int) []string {
	d, _ := m.readerDoc(p)
	out := m.renderProse(d, h, readerIndent, m.ws.focus == p)
	p.scroll = d.eng.Top
	return out
}

// renderPreview is the rendered side of source+preview. Unfocused it
// follows the editor cursor (proportional scroll sync); focused it is
// a reader with its own cursor.
func (m Model) renderPreview(p *notePane, w, h int) []string {
	d, _ := m.previewDoc(p)
	if !p.previewFocus {
		e := p.buf.eng
		n := len(d.lines)
		top := clamp(e.Cursor().Line*n/max(1, e.LineCount())-h/3, 0, max(0, n-h))
		d.eng.Top = top
		if c := d.eng.Cursor(); c.Line < top || c.Line >= top+h {
			d.eng.SetCursor(vim.Pos{Line: top})
		}
	}
	return m.renderProse(d, h, previewIndent, m.ws.focus == p && p.previewFocus)
}

// --- editor ----------------------------------------------------------

// yankFlashFor is how long the yank highlight lasts.
const yankFlashFor = 200 * time.Millisecond

func (m Model) renderEditor(p *notePane, w, h int, focused bool) []string {
	e := p.buf.eng
	t := theme.Current
	cur := e.Cursor()
	top := e.Top
	if !focused {
		cur, top = p.cur, p.top
	}
	ls := e.Lines()
	strs := make([]string, len(ls))
	for i, l := range ls {
		strs[i] = string(l)
	}
	code := codeBlockLines(strs)
	gutter := 0
	if e.Opts.Number || e.Opts.RelativeNumber {
		gutter = len(fmt.Sprint(len(ls))) + 2
	}
	tw := max(4, w-gutter-1)
	ts := e.Opts.TabStop
	// rows each line takes (soft wrap)
	rowsOf := func(l []rune) int {
		if !e.Opts.Wrap {
			return 1
		}
		dw := vim.DisplayCol(l, len(l), ts) + 1
		return max(1, (dw+tw-1)/tw)
	}
	// keep the cursor's row visible with wrapped lines
	if top > cur.Line {
		top = cur.Line
	}
	for {
		rows := 0
		for l := top; l <= cur.Line && l < len(ls); l++ {
			rows += rowsOf(ls[l])
		}
		if rows <= h || top >= cur.Line {
			break
		}
		top++
	}
	if focused {
		e.Top = top
	} else {
		p.top = top
	}
	// the mode mark (DESIGN-LANGUAGE §8 row 16, "gutter colour = mode
	// colour"): in the focused editor the cursor line's left margin is a
	// bar in the mode colour, and so is its line number. Other rows and
	// unfocused editors keep a plain margin.
	edge, edgeColor := " ", t.Accent
	if focused {
		edgeColor = modeColor(e.Mode())
		edge = lipgloss.NewStyle().Foreground(edgeColor).Render("▎")
	}
	selS, selE, selMode, toEOL, selOK := vim.Pos{}, vim.Pos{}, vim.ModeNormal, false, false
	if focused {
		selS, selE, selMode, toEOL, selOK = e.Selection()
	}
	flS, flE, flMode, flOK := e.Flash(yankFlashFor)
	shape := e.Shape(m.normalShape())
	selBg := visualSelBg
	var out []string
	for l := top; l < len(ls) && len(out) < h; l++ {
		line := ls[l]
		str := strs[l]
		base := mdLineStyle(str, code[l])
		var cls []int
		if shouldClassifyInline(str, code[l]) {
			cls = classifyInline(str)
		}
		matches := e.MatchesInLine(l)
		inSel := func(c int, s, en vim.Pos, mode vim.Mode, eol bool) bool {
			if l < s.Line || l > en.Line {
				return false
			}
			switch mode {
			case vim.ModeVisualLine:
				return true
			case vim.ModeVisualBlock:
				a := vim.DisplayCol(ls[s.Line], s.Col, ts)
				b := vim.DisplayCol(ls[en.Line], en.Col, ts)
				if a > b {
					a, b = b, a
				}
				if mode == vim.ModeVisualBlock && !selOK {
					// flash of a block yank uses the stored corners
				}
				dc := vim.DisplayCol(line, c, ts)
				return dc >= a && (eol || dc <= b)
			}
			if l == s.Line && c < s.Col {
				return false
			}
			if l == en.Line && c > en.Col {
				return false
			}
			return true
		}
		type cell struct {
			text string
			st   lipgloss.Style
		}
		var cells []cell
		for c := 0; c <= len(line); c++ {
			atEnd := c == len(line)
			var ch string
			st := base
			if !atEnd {
				r := line[c]
				switch {
				case r == '\t':
					dc := vim.DisplayCol(line, c, ts)
					ch = strings.Repeat(" ", ts-dc%ts)
				case r < ' ':
					ch = "^" + string(rune(r+'@'))
				default:
					ch = string(r)
				}
				if cls != nil && c < len(cls) {
					st = resolveInlineStyle(cls[c], base)
				}
			}
			for _, mt := range matches {
				if c >= mt[0] && c < mt[1] {
					st = st.Background(t.Warning).Foreground(t.Background)
				}
			}
			if flOK && !atEnd && inSel(c, flS, flE, flMode, false) {
				st = st.Background(yankFlashBg)
			}
			if selOK && (!atEnd || (len(line) == 0 && selMode != vim.ModeVisualBlock)) && inSel(c, selS, selE, selMode, toEOL) {
				st = st.Background(selBg)
				if atEnd {
					ch = " "
				}
			}
			if l == cur.Line && c == cur.Col {
				switch {
				case !focused:
					st = st.Underline(true)
				case e.Mode() == vim.ModeCmdline:
					st = st.Reverse(true)
				case shape == vim.ShapeBar:
					if atEnd || ch == "" {
						ch = "▏"
						st = lipgloss.NewStyle().Foreground(t.Accent).Bold(true)
					} else {
						st = st.Background(blendHex(t.Accent, t.Background, 0.55)).Foreground(t.Text)
					}
				case shape == vim.ShapeUnderline:
					if atEnd || ch == "" {
						ch = " "
					}
					st = st.Underline(true).Foreground(t.Accent).Bold(true)
				default:
					if atEnd || ch == "" {
						ch = " "
					}
					st = lipgloss.NewStyle().Background(primaryColor).Foreground(t.Background)
				}
			}
			if atEnd && ch == "" {
				break
			}
			cells = append(cells, cell{ch, st})
		}
		// wrap cells into rows of tw columns
		var rows []string
		var row strings.Builder
		rw := 0
		flush := func() {
			rows = append(rows, row.String())
			row.Reset()
			rw = 0
		}
		for _, c := range cells {
			cw := lipgloss.Width(c.text)
			if e.Opts.Wrap && rw+cw > tw && rw > 0 {
				flush()
			}
			row.WriteString(c.st.Render(c.text))
			rw += cw
		}
		flush()
		if !e.Opts.Wrap && len(rows) > 0 {
			rows = rows[:1]
		}
		for ri, r := range rows {
			g := ""
			if gutter > 0 {
				num := ""
				if ri == 0 {
					n := l + 1
					if e.Opts.RelativeNumber && l != cur.Line {
						n = abs(l - cur.Line)
					}
					num = fmt.Sprint(n)
				}
				gs := lipgloss.NewStyle().Foreground(t.TextDim)
				if l == cur.Line {
					gs = gs.Foreground(edgeColor)
				}
				g = gs.Render(fmt.Sprintf("%*s ", gutter-1, num))
			}
			mark := " "
			if l == cur.Line {
				mark = edge
			}
			out = append(out, mark+g+r)
			if len(out) >= h {
				break
			}
		}
	}
	for len(out) < h {
		out = append(out, lipgloss.NewStyle().Foreground(t.TextDim).Render(" ~"))
	}
	return out
}

// --- board -----------------------------------------------------------

func (m Model) renderBoard(p *notePane, w, h int) []string {
	t := theme.Current
	b := p.board()
	if len(b.Columns) == 0 {
		return []string{"", "  " + lipgloss.NewStyle().Foreground(t.TextDim).Render("Quadro vazio — A cria uma coluna.")}
	}
	p.bcol = clamp(p.bcol, 0, len(b.Columns)-1)
	colW := max(22, min(40, (w-1)/len(b.Columns)))
	perView := max(1, w/(colW+1))
	start := 0
	if p.bcol >= perView {
		start = p.bcol - perView + 1
	}
	footer := ""
	switch {
	case p.binput != "":
		label := map[string]string{"card": "novo cartão", "cardafter": "novo cartão", "edit": "editar", "column": "nova coluna", "filter": "filtro"}[p.binput]
		footer = lipgloss.NewStyle().Foreground(t.Primary).Bold(true).Render(" "+label+" › ") + p.bvalue + lipgloss.NewStyle().Foreground(t.Accent).Render("▏")
	case p.bconfirm:
		footer = lipgloss.NewStyle().Foreground(t.Warning).Bold(true).Render(" apagar este cartão? y/n")
	case p.bfilter != "":
		footer = lipgloss.NewStyle().Foreground(t.Accent).Render(" filtro: " + p.bfilter + "  (Esc limpa)")
	case !p.bedit:
		footer = lipgloss.NewStyle().Foreground(t.TextDim).Render(" leitura · i edita o quadro · zv fonte")
	}
	bodyH := h
	if footer != "" {
		bodyH--
	}
	var cols [][]string
	for ci := start; ci < len(b.Columns) && ci < start+perView; ci++ {
		col := b.Columns[ci]
		vis := visibleCards(b, ci, p.bfilter)
		var lines []string
		hs := lipgloss.NewStyle().Bold(true).Foreground(t.Secondary)
		if ci == p.bcol {
			hs = hs.Foreground(t.Primary).Underline(true)
		}
		lines = append(lines, hs.Render(truncate(col.Title, colW-6))+lipgloss.NewStyle().Foreground(t.TextDim).Render(fmt.Sprintf(" %d", len(col.Cards))), "")
		focusRow := 0
		for vi, idx := range vis {
			cd := col.Cards[idx]
			box := "☐ "
			ts := lipgloss.NewStyle().Foreground(t.Text)
			if cd.Done {
				box = "☑ "
				ts = ts.Foreground(t.TextDim).Strikethrough(true)
			}
			sel := ci == p.bcol && vi == p.bcard
			text := m.prose.inline(cd.Title)
			wrapped := wrapWords(stripAnsi(text), colW-4)
			if len(wrapped) > 3 {
				wrapped = append(wrapped[:2], truncate(wrapped[2], colW-6)+"…")
			}
			if sel {
				focusRow = len(lines)
			}
			for li, wl := range wrapped {
				pre := "  "
				if li == 0 {
					pre = box
				}
				row := ts.Render(pre + wl)
				if sel {
					row = lipgloss.NewStyle().Background(t.SelectedBg).Foreground(t.Text).Bold(true).Render(padRight(pre+wl, colW-1))
				}
				lines = append(lines, row)
			}
			if len(cd.Body) > 0 {
				lines = append(lines, lipgloss.NewStyle().Foreground(t.TextDim).Render("  "+truncate(strings.TrimSpace(cd.Body[0]), colW-4)))
			}
			lines = append(lines, "")
		}
		if len(vis) == 0 {
			lines = append(lines, lipgloss.NewStyle().Foreground(t.TextDim).Render("  (vazio)"))
		}
		// scroll the column to keep the focused card visible
		if ci == p.bcol && focusRow >= bodyH-2 {
			lines = append(lines[:2], lines[min(len(lines), focusRow-bodyH/2):]...)
		}
		cols = append(cols, fitBlock(lines, colW, bodyH))
	}
	out := make([]string, bodyH)
	sep := lipgloss.NewStyle().Foreground(t.Separator).Render("│")
	for r := 0; r < bodyH; r++ {
		var row strings.Builder
		row.WriteString(" ")
		for i, c := range cols {
			if i > 0 {
				row.WriteString(sep)
			}
			row.WriteString(c[r])
		}
		out[r] = row.String()
	}
	if footer != "" {
		out = append(out, footer)
	}
	return out
}

// --- canvas ----------------------------------------------------------

func (m Model) renderCanvas(p *notePane, w, h int) []string {
	t := theme.Current
	rows, c, warn := m.canvasRows(p)
	var out []string
	if warn != "" {
		out = append(out, lipgloss.NewStyle().Foreground(t.Warning).Render(" ⚠ "+truncate(warn, w-4)))
	}
	if len(rows) == 0 {
		return append(out, "", "  "+lipgloss.NewStyle().Foreground(t.TextDim).Render("Canvas vazio. A edição gráfica fica no app/web; zv mostra o JSON."))
	}
	p.crow = clamp(p.crow, 0, len(rows)-1)
	lw := w
	if w >= 70 {
		lw = w * 55 / 100
	}
	listH := h - len(out)
	start := 0
	if p.crow >= listH {
		start = p.crow - listH + 1
	}
	glyph := map[canvas.RowKind]string{canvas.RowGroup: "▣ ", canvas.RowText: "¶ ", canvas.RowFile: "◇ ", canvas.RowLink: "↗ ", canvas.RowHeader: "", canvas.RowEdge: ""}
	var list []string
	for i := start; i < len(rows) && len(list) < listH; i++ {
		r := rows[i]
		st := lipgloss.NewStyle().Foreground(t.Text)
		switch r.Kind {
		case canvas.RowGroup:
			st = st.Bold(true).Foreground(t.Secondary)
		case canvas.RowFile:
			st = st.Foreground(t.Accent)
		case canvas.RowHeader:
			st = st.Bold(true).Foreground(t.Primary)
		case canvas.RowEdge:
			st = st.Foreground(t.TextDim)
		}
		text := strings.Repeat("  ", r.Depth) + glyph[r.Kind] + r.Label
		line := " " + st.Render(truncate(text, lw-3))
		if i == p.crow {
			line = " " + lipgloss.NewStyle().Background(t.SelectedBg).Foreground(t.Text).Bold(true).Render(padRight(truncate(text, lw-3), lw-3))
		}
		list = append(list, line)
	}
	if lw == w {
		return append(out, list...)
	}
	// preview of the focused node
	r := rows[p.crow]
	var prev []string
	pw := w - lw - 2
	switch {
	case r.File != "":
		abs := filepath.Join(m.rootDir, filepath.FromSlash(r.File))
		if pathIsInsideVault(m.rootDir, abs) {
			if body, err := readBody(abs); err == nil {
				prev = strings.Split(m.prose.render(body, pw), "\n")
			}
		}
		if prev == nil {
			prev = []string{lipgloss.NewStyle().Foreground(t.Error).Render("arquivo não encontrado")}
		}
	case r.URL != "":
		prev = []string{lipgloss.NewStyle().Foreground(t.Accent).Render(wrapHard(r.URL, pw)), "", "gx abre no navegador"}
	case r.Kind == canvas.RowText && c != nil:
		prev = strings.Split(m.prose.render(c.TextOf(r.NodeID), pw), "\n")
	}
	lb := fitBlock(list, lw, listH)
	rb := fitBlock(prev, pw, listH)
	sep := lipgloss.NewStyle().Foreground(t.Separator).Render("│ ")
	for i := range lb {
		out = append(out, lb[i]+sep+rb[i])
	}
	return out
}

// --- inline images (reader) --------------------------------------------

// imagePlaceholders swaps local image lines for tokens the prose
// renderer keeps intact; expandImages puts the rendered image there.
// Only ANSI-text renderers (timg/chafa/viu) are used inside panes;
// otherwise glamour shows the image as a link.
func imagePlaceholders(body, notePath string) (string, []string) {
	if !strings.Contains(body, "![") || !image.TextRenderer() {
		return body, nil
	}
	var imgs []string
	lines := strings.Split(body, "\n")
	for i, l := range lines {
		if !image.HasImage(l) {
			continue
		}
		src := image.ExtractImagePath(l)
		if image.IsEmbed(src) || image.IsVideo(src) || image.IsPdf(src) {
			continue
		}
		path := image.GetImagePath(l, notePath)
		if path == "" {
			continue
		}
		if _, err := os.Stat(path); err != nil {
			continue
		}
		lines[i] = fmt.Sprintf("LUMIIMG%dX", len(imgs))
		imgs = append(imgs, path)
	}
	return strings.Join(lines, "\n"), imgs
}

var imageRenders sync.Map // "path@width" → rendered lines

func expandImages(lines, imgs []string, w int) []string {
	var out []string
	for _, l := range lines {
		plain := stripAnsi(l)
		idx := -1
		if i := strings.Index(plain, "LUMIIMG"); i >= 0 {
			fmt.Sscanf(plain[i:], "LUMIIMG%dX", &idx)
		}
		if idx < 0 || idx >= len(imgs) {
			out = append(out, l)
			continue
		}
		key := fmt.Sprintf("%s@%d", imgs[idx], w)
		v, ok := imageRenders.Load(key)
		if !ok {
			v = strings.Split(strings.TrimRight(image.Render(imgs[idx], w), "\n"), "\n")
			imageRenders.Store(key, v)
		}
		out = append(out, v.([]string)...)
	}
	return out
}
