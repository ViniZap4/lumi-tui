package ui

import (
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/vinizap/lumi/tui-client/canvas"
	"github.com/vinizap/lumi/tui-client/filesystem"
	"github.com/vinizap/lumi/tui-client/kanban"
)

// --- kanban board (SPEC-PARTE2 §5) -------------------------------------

func (p *notePane) board() *kanban.Board { return kanban.Parse(p.buf.eng.Text()) }

// visibleCards are the card indexes of column c that match the filter.
func visibleCards(b *kanban.Board, c int, filter string) []int {
	if c < 0 || c >= len(b.Columns) {
		return nil
	}
	var out []int
	f := strings.ToLower(filter)
	for i, cd := range b.Columns[c].Cards {
		if f == "" || strings.Contains(strings.ToLower(cd.Title+" "+strings.Join(cd.Body, " ")), f) {
			out = append(out, i)
		}
	}
	return out
}

// cardIndex is the real index of the focused card (-1: none).
func (p *notePane) cardIndex(b *kanban.Board) int {
	vis := visibleCards(b, p.bcol, p.bfilter)
	if p.bcard < 0 || p.bcard >= len(vis) {
		return -1
	}
	return vis[p.bcard]
}

func (m *Model) applyBoard(p *notePane, edits []kanban.Edit) tea.Cmd {
	if len(edits) == 0 {
		return nil
	}
	body := kanban.Apply(p.buf.eng.Text(), edits)
	p.buf.eng.SetText(body)
	return m.saveBuffer(p.buf)
}

func (m Model) updateBoard(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	p := m.ws.focus
	key := msg.String()
	b := p.board()
	if !p.bedit && p.binput != "filter" {
		return m.updateBoardReading(p, b, key)
	}
	if p.binput != "" {
		switch key {
		case "esc":
			if p.binput == "filter" {
				p.bfilter = ""
			}
			p.binput, p.bvalue = "", ""
			return m, nil
		case "enter":
			kind, val := p.binput, strings.TrimSpace(p.bvalue)
			p.binput, p.bvalue = "", ""
			switch kind {
			case "filter":
				p.bfilter = val
				p.bcard = 0
			case "card", "cardafter":
				if val == "" {
					return m, nil
				}
				at := len(b.Columns[p.bcol].Cards)
				if kind == "cardafter" {
					if ci := p.cardIndex(b); ci >= 0 {
						at = ci + 1
					}
				}
				cmd := m.applyBoard(p, b.Insert(p.bcol, at, val))
				p.bfilter = ""
				p.bcard = at
				return m, cmd
			case "edit":
				if ci := p.cardIndex(b); ci >= 0 && val != "" {
					return m, m.applyBoard(p, b.SetText(p.bcol, ci, val))
				}
			case "column":
				if val != "" {
					cmd := m.applyBoard(p, b.AddColumn(val))
					p.bcol, p.bcard = len(b.Columns), 0
					return m, cmd
				}
			}
			return m, nil
		}
		if v, ok := editLine(p.bvalue, msg); ok {
			p.bvalue = v
			if p.binput == "filter" {
				p.bfilter = v
				p.bcard = 0
			}
		}
		return m, nil
	}
	if p.bconfirm {
		p.bconfirm = false
		if key == "y" || key == "enter" {
			if ci := p.cardIndex(b); ci >= 0 {
				cmd := m.applyBoard(p, b.Delete(p.bcol, ci))
				p.bcard = max(0, p.bcard-1)
				return m, cmd
			}
		}
		m.statusMsg = "cancelado"
		return m, nil
	}
	if p.bpending == "d" {
		p.bpending = ""
		if key == "d" && p.cardIndex(b) >= 0 {
			p.bconfirm = true
			return m, nil
		}
	}
	ncol := len(b.Columns)
	vis := visibleCards(b, p.bcol, p.bfilter)
	switch key {
	case "h", "left":
		if p.bcol > 0 {
			p.bcol--
			p.bcard = clamp(p.bcard, 0, max(0, len(visibleCards(b, p.bcol, p.bfilter))-1))
		}
		return m, nil
	case "l", "right":
		if p.bcol < ncol-1 {
			p.bcol++
			p.bcard = clamp(p.bcard, 0, max(0, len(visibleCards(b, p.bcol, p.bfilter))-1))
		}
		return m, nil
	case "j", "down":
		if p.bcard < len(vis)-1 {
			p.bcard++
		}
		return m, nil
	case "k", "up":
		if p.bcard > 0 {
			p.bcard--
		}
		return m, nil
	case "H", "L":
		ci := p.cardIndex(b)
		tc := p.bcol - 1
		if key == "L" {
			tc = p.bcol + 1
		}
		if ci < 0 || tc < 0 || tc >= ncol {
			return m, nil
		}
		ti := min(ci, len(b.Columns[tc].Cards))
		cmd := m.applyBoard(p, b.Move(p.bcol, ci, tc, ti))
		p.bcol = tc
		p.bfilter = ""
		p.bcard = ti
		return m, cmd
	case "J", "K":
		ci := p.cardIndex(b)
		if ci < 0 {
			return m, nil
		}
		ti := ci + 1
		if key == "K" {
			ti = ci - 1
		}
		if ti < 0 || ti >= len(b.Columns[p.bcol].Cards) {
			return m, nil
		}
		cmd := m.applyBoard(p, b.Move(p.bcol, ci, p.bcol, ti))
		p.bfilter = ""
		p.bcard = ti
		return m, cmd
	case "a", "o":
		if ncol == 0 {
			m.statusMsg = "crie uma coluna com A"
			return m, nil
		}
		p.binput, p.bvalue = "card", ""
		if key == "o" {
			p.binput = "cardafter"
		}
		return m, nil
	case "A":
		p.binput, p.bvalue = "column", ""
		return m, nil
	case "enter", "i":
		if ci := p.cardIndex(b); ci >= 0 {
			p.binput, p.bvalue = "edit", b.Columns[p.bcol].Cards[ci].Title
		}
		return m, nil
	case "x", " ":
		if key == " " {
			break
		}
		if ci := p.cardIndex(b); ci >= 0 {
			return m, m.applyBoard(p, b.Toggle(p.bcol, ci))
		}
		return m, nil
	case "d":
		p.bpending = "d"
		return m, nil
	case "/":
		p.binput, p.bvalue = "filter", p.bfilter
		return m, nil
	case "esc", "q":
		if key == "esc" && p.bfilter != "" {
			p.bfilter = ""
			return m, nil
		}
		// Esc / q with nothing pending: back to Reading
		p.bedit = false
		m.statusMsg = "vista: " + p.modeLabel()
		return m, nil
	case "u":
		// undo the last board change (the buffer keeps the history)
		before := p.buf.eng.Gen()
		p.buf.eng.Feed("u")
		if p.buf.eng.Gen() != before {
			return m, m.saveBuffer(p.buf)
		}
		return m, nil
	}
	if mm, cmd, ok := m.commonNoteKey(key); ok {
		return mm, cmd
	}
	return m, nil
}

// --- board Reading (SPEC-PARTE2 §5a) ------------------------------------

// modeLabel is the pane header's mode: leitura / fonte / fonte+prévia,
// "quadro · leitura" / "quadro · edição", "canvas · leitura".
func (p *notePane) modeLabel() string {
	switch p.view {
	case viewBoard:
		if p.bedit {
			return "quadro · edição"
		}
		return "quadro · leitura"
	case viewCanvas:
		return "canvas · leitura"
	}
	return p.view.String()
}

// cardLinks are the links in the focused card (title and nested lines).
func (p *notePane) cardLinks(b *kanban.Board) []noteLink {
	ci := p.cardIndex(b)
	if ci < 0 {
		return nil
	}
	cd := b.Columns[p.bcol].Cards[ci]
	return findLinks(cd.Title + "\n" + strings.Join(cd.Body, "\n"))
}

// readOnlyHint is shown when a key would change a board in Reading.
const readOnlyHint = "leitura: nada muda aqui — i edita o quadro"

// updateBoardReading: the board in Reading — navigate, filter, follow
// links, K cards; no moves, edits, deletes or toggles.
func (m Model) updateBoardReading(p *notePane, b *kanban.Board, key string) (tea.Model, tea.Cmd) {
	if m.notePrefix == "g" && (key == "f" || key == "x") {
		m.notePrefix = ""
		ls := p.cardLinks(b)
		if len(ls) == 0 {
			m.statusMsg = "nenhum link neste cartão"
			return m, nil
		}
		if key == "x" {
			for _, l := range ls {
				if l.external {
					openURLFn(l.target)
					return m, nil
				}
			}
			return m, nil
		}
		return m.followLink(p, ls[0], "replace")
	}
	vis := visibleCards(b, p.bcol, p.bfilter)
	ncol := len(b.Columns)
	switch key {
	case "h", "left", "l", "right":
		d := 1
		if key == "h" || key == "left" {
			d = -1
		}
		if c := p.bcol + d; c >= 0 && c < ncol {
			p.bcol = c
			p.bcard = clamp(p.bcard, 0, max(0, len(visibleCards(b, p.bcol, p.bfilter))-1))
		}
		return m, nil
	case "j", "down":
		if p.bcard < len(vis)-1 {
			p.bcard++
		}
		return m, nil
	case "k", "up":
		if p.bcard > 0 {
			p.bcard--
		}
		return m, nil
	case "tab", "shift+tab":
		// next / previous card across columns
		d := 1
		if key == "shift+tab" {
			d = -1
		}
		c, i := p.bcol, p.bcard+d
		for n := 0; n <= ncol; n++ {
			cv := visibleCards(b, c, p.bfilter)
			if i >= 0 && i < len(cv) {
				p.bcol, p.bcard = c, i
				return m, nil
			}
			c = (c + d + ncol) % max(1, ncol)
			if d > 0 {
				i = 0
			} else {
				i = len(visibleCards(b, c, p.bfilter)) - 1
			}
		}
		return m, nil
	case "G":
		p.bcard = max(0, len(vis)-1)
		return m, nil
	case "enter":
		if ls := p.cardLinks(b); len(ls) > 0 {
			return m.followLink(p, ls[0], "replace")
		}
		m.statusMsg = "nenhum link neste cartão — i edita o quadro"
		return m, nil
	case "K":
		if ls := p.cardLinks(b); len(ls) > 0 {
			return m.openHover(p, ls[0])
		}
		m.statusMsg = "nenhum link neste cartão"
		return m, nil
	case "i", "e":
		p.bedit = true
		m.statusMsg = "vista: " + p.modeLabel() + " (Esc volta à leitura)"
		return m, nil
	case "/":
		p.binput, p.bvalue = "filter", p.bfilter
		return m, nil
	case "esc":
		if p.bfilter != "" {
			p.bfilter = ""
			return m, nil
		}
	case "x", " ", "H", "L", "J", "a", "o", "A", "d", "u", "D":
		m.statusMsg = readOnlyHint
		return m, nil
	}
	if mm, cmd, ok := m.commonNoteKey(key); ok {
		return mm, cmd
	}
	return m, nil
}

// --- canvas outline (SPEC-PARTE2 §6, TUI part) --------------------------

// canvasRows parses the buffer; JSON may be invalid mid-edit (server
// contract): the last good parse is kept.
func (m Model) canvasRows(p *notePane) ([]canvas.Row, *canvas.Canvas, string) {
	c, err := canvas.Parse([]byte(p.buf.eng.Text()))
	if err != nil {
		if p.buf.lastCanvas != nil {
			return p.buf.lastCanvas.Outline(), p.buf.lastCanvas, "JSON inválido — mostrando a última versão boa"
		}
		return nil, nil, err.Error()
	}
	p.buf.lastCanvas = c
	return c.Outline(), c, ""
}

func (m Model) updateCanvas(key string) (tea.Model, tea.Cmd) {
	p := m.ws.focus
	rows, _, _ := m.canvasRows(p)
	selectable := func(i int) bool { return i >= 0 && i < len(rows) && rows[i].Kind != canvas.RowHeader }
	step := func(d int) {
		for i := p.crow + d; i >= 0 && i < len(rows); i += d {
			if selectable(i) {
				p.crow = i
				return
			}
		}
	}
	if m.notePrefix == "g" {
		switch key {
		case "g":
			m.notePrefix = ""
			p.crow = 0
			return m, nil
		case "f":
			m.notePrefix = ""
			return m.openCanvasRow(p, "replace")
		case "x":
			m.notePrefix = ""
			if p.crow < len(rows) && rows[p.crow].URL != "" {
				openURLFn(rows[p.crow].URL)
			}
			return m, nil
		}
	}
	switch key {
	case "j", "down", "tab":
		step(1)
		return m, nil
	case "k", "up", "shift+tab":
		step(-1)
		return m, nil
	case "G":
		p.crow = max(0, len(rows)-1)
		return m, nil
	case "enter", "l":
		return m.openCanvasRow(p, "replace")
	case "i", "e":
		// Edit is the app / web canvas; the TUI has Reading and Source
		m.statusMsg = "editar o canvas: no app ou na web · zv / :source mostra o JSON"
		return m, nil
	}
	if mm, cmd, ok := m.commonNoteKey(key); ok {
		return mm, cmd
	}
	return m, nil
}

// openCanvasRow opens the focused file node's note (or link URL).
func (m Model) openCanvasRow(p *notePane, how string) (Model, tea.Cmd) {
	rows, _, _ := m.canvasRows(p)
	if p.crow < 0 || p.crow >= len(rows) {
		return m, nil
	}
	r := rows[p.crow]
	switch {
	case r.File != "":
		abs := filepath.Join(m.rootDir, filepath.FromSlash(r.File))
		if !pathIsInsideVault(m.rootDir, abs) {
			m.statusMsg = "arquivo fora do livro: " + r.File
			return m, nil
		}
		n, err := filesystem.ReadNote(abs)
		if err != nil {
			m.statusMsg = "arquivo não encontrado: " + r.File
			return m, nil
		}
		m.syncFocusOut()
		return m, m.openNoteIn(n, how)
	case r.URL != "":
		openURLFn(r.URL)
		m.statusMsg = "aberto no navegador"
	default:
		m.statusMsg = "Enter abre nós de arquivo"
	}
	return m, nil
}
