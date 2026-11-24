package vim

import (
	"fmt"
	"strings"
	"unicode"
)

// opRange is the text an operator works on. Charwise ranges are
// [s, en) in flat coordinates; linewise ranges cover lines
// s.Line..en.Line; blockwise ranges cover display columns dc1..dc2.
type opRange struct {
	s, en      Pos
	kind       int
	block      bool
	dc1, dc2   int
	toEOL      bool
	fromVisual bool
}

func (e *Engine) execOperator(c command) {
	count := max(1, c.count)
	var r opRange
	switch {
	case c.name == "line":
		last := e.cur.Line + count - 1
		if last > e.lastLine() {
			if count > 1 && e.cur.Line == e.lastLine() {
				return
			}
			last = e.lastLine()
		}
		r = opRange{s: Pos{e.cur.Line, 0}, en: Pos{last, 0}, kind: mLine}
	case c.name == "/" || c.name == "?":
		cc := c
		e.cmdOp = &cc
		e.openCmdline([]rune(c.name)[0], "")
		return
	case len(c.name) == 2 && (c.name[0] == 'i' || c.name[0] == 'a') && !isMotionName(c.name):
		s, en, lw, ok := e.textObject(c.name, c.count)
		if !ok {
			return
		}
		kind := mExcl
		if lw {
			kind = mLine
		}
		r = opRange{s: s, en: en, kind: kind}
	default:
		name := c.name
		// cw on a word behaves like ce (vim's special case)
		if c.op == "c" && (name == "w" || name == "W") {
			l := e.line()
			if e.cur.Col < len(l) && class(l[e.cur.Col], name == "W") != clsBlank {
				if name == "w" {
					name = "e"
				} else {
					name = "E"
				}
				// on the last char of a word, ce would jump to the next
				// word: change just this word
				nx := nextCluster(l, e.cur.Col)
				if count == 1 && (nx >= len(l) || class(l[nx], c.name == "W") != class(l[e.cur.Col], c.name == "W")) {
					r = opRange{s: e.cur, en: Pos{e.cur.Line, nx}, kind: mExcl}
					e.applyOp(c.op, c.reg, r, count)
					return
				}
			}
		}
		t, kind, ok := e.motion(name, c.arg, c.count, true)
		if !ok {
			return
		}
		s, en := order(e.cur, t)
		switch kind {
		case mIncl:
			if en.Col < len(e.lines[en.Line]) {
				en.Col = nextCluster(e.lines[en.Line], en.Col)
			}
		case mExcl:
			// exclusive motion ending in column 0 of a later line
			if en.Line > s.Line && en.Col == 0 && name != "/" {
				if s.Col <= firstNonBlank(e.lines[s.Line]) && name != "w" && name != "W" {
					kind = mLine
					en = Pos{en.Line - 1, 0}
				} else {
					en = Pos{en.Line - 1, len(e.lines[en.Line-1])}
				}
			}
		}
		if kind == mLine {
			s, en = Pos{s.Line, 0}, Pos{en.Line, 0}
		}
		r = opRange{s: s, en: en, kind: kind}
		if isJump(name) {
			e.marks['\''] = e.cur
		}
	}
	e.applyOp(c.op, c.reg, r, count)
}

// rangeText returns the text of r (and whether it is linewise).
func (e *Engine) rangeText(r opRange) Register {
	switch {
	case r.block:
		var rows []string
		for l := r.s.Line; l <= r.en.Line; l++ {
			a, b := e.blockCols(l, r)
			rows = append(rows, string(e.lines[l][a:b]))
		}
		return Register{Text: strings.Join(rows, "\n"), Blockwise: true}
	case r.kind == mLine:
		var rows []string
		for l := r.s.Line; l <= r.en.Line; l++ {
			rows = append(rows, string(e.lines[l]))
		}
		return Register{Text: strings.Join(rows, "\n"), Linewise: true}
	}
	return Register{Text: e.textRange(r.s, r.en)}
}

// blockCols is the rune span of line l inside a block selection.
func (e *Engine) blockCols(l int, r opRange) (int, int) {
	ln := e.lines[l]
	ts := e.Opts.TabStop
	if displayWidth(ln, ts) <= r.dc1 {
		return len(ln), len(ln)
	}
	a := ColForDisplay(ln, r.dc1, ts, true)
	if r.toEOL {
		return a, len(ln)
	}
	b := ColForDisplay(ln, r.dc2, ts, true)
	if b < len(ln) {
		b = nextCluster(ln, b)
	}
	return a, b
}

func (e *Engine) setFlash(r opRange) {
	mode := ModeVisual
	s, en := r.s, r.en
	switch {
	case r.block:
		mode = ModeVisualBlock
		s = Pos{r.s.Line, ColForDisplay(e.lines[r.s.Line], r.dc1, e.Opts.TabStop, true)}
		en = Pos{r.en.Line, ColForDisplay(e.lines[r.en.Line], r.dc2, e.Opts.TabStop, true)}
	case r.kind == mLine:
		mode = ModeVisualLine
	default:
		// stored inclusive
		if en.Col > 0 {
			en.Col--
		} else if en.Line > s.Line {
			en = Pos{en.Line - 1, len(e.lines[en.Line-1])}
		}
	}
	e.flash = &visualSel{start: s, end: en, mode: mode, toEOL: r.toEOL}
	e.flashAt = e.Now()
}

func (e *Engine) applyOp(op string, reg rune, r opRange, count int) {
	switch op {
	case "y":
		txt := e.rangeText(r)
		e.store(reg, txt, 'y')
		e.setFlash(r)
		if n := r.en.Line - r.s.Line + 1; r.kind == mLine && n > 2 {
			e.setMsg(fmt.Sprintf("%d lines yanked", n))
		} else if r.block && n > 2 {
			e.setMsg(fmt.Sprintf("block of %d lines yanked", n))
		}
		if r.kind == mLine && !r.fromVisual {
			if e.cur.Line != r.s.Line {
				e.cur.Line = r.s.Line
			}
		} else if r.block {
			e.cur = Pos{r.s.Line, ColForDisplay(e.lines[r.s.Line], r.dc1, e.Opts.TabStop, false)}
		} else if r.kind != mLine {
			e.cur = r.s
		} else {
			e.cur = Pos{r.s.Line, 0}
			if r.fromVisual {
				e.cur.Col = min(e.cur.Col, lastCluster(e.line()))
			}
		}
		e.clampCursor(false)
	case "d", "c":
		e.checkpoint()
		txt := e.rangeText(r)
		if op == "d" || !r.block {
			e.store(reg, txt, 'd')
		} else {
			e.store(reg, txt, 'd')
		}
		switch {
		case r.block:
			for l := r.s.Line; l <= r.en.Line; l++ {
				a, b := e.blockCols(l, r)
				ln := e.lines[l]
				e.lines[l] = append(append([]rune(nil), ln[:a]...), ln[b:]...)
			}
			e.gen++
			e.cur = Pos{r.s.Line, ColForDisplay(e.lines[r.s.Line], r.dc1, e.Opts.TabStop, true)}
			if op == "c" {
				e.block = &blockInsert{l1: r.s.Line, l2: r.en.Line, dcol: r.dc1, line: r.s.Line, col: e.cur.Col}
				e.startInsert("c", 1)
				return
			}
		case r.kind == mLine:
			n := r.en.Line - r.s.Line + 1
			if op == "c" {
				ind := leadingWS(e.lines[r.s.Line])
				e.lines = append(e.lines[:r.s.Line], append([][]rune{ind}, e.lines[r.en.Line+1:]...)...)
				e.gen++
				e.cur = Pos{r.s.Line, len(ind)}
				e.startInsert("c", 1)
				return
			}
			e.lines = append(e.lines[:r.s.Line], e.lines[r.en.Line+1:]...)
			if len(e.lines) == 0 {
				e.lines = [][]rune{{}}
			}
			e.gen++
			e.cur = Pos{min(r.s.Line, e.lastLine()), 0}
			e.cur.Col = firstNonBlank(e.line())
			if n > 2 {
				e.setMsg(fmt.Sprintf("%d fewer lines", n))
			}
		default:
			e.deleteRange(r.s, r.en)
			e.cur = r.s
			if op == "c" {
				e.startInsert("c", 1)
				return
			}
		}
		e.clampCursor(false)
		e.want = e.dcol()
	case ">", "<":
		e.checkpoint()
		times := 1
		if r.fromVisual {
			times = count
		}
		for l := r.s.Line; l <= r.en.Line; l++ {
			for i := 0; i < times; i++ {
				e.shiftLine(l, op == ">")
			}
		}
		e.gen++
		e.cur = Pos{r.s.Line, firstNonBlank(e.lines[r.s.Line])}
		if n := r.en.Line - r.s.Line + 1; n > 2 {
			e.setMsg(fmt.Sprintf("%d lines %sed %d time", n, op, times))
		}
	case "g~", "gu", "gU", "~", "u", "U":
		e.checkpoint()
		f := map[string]func(rune) rune{"g~": toggleCase, "~": toggleCase, "gu": unicode.ToLower, "u": unicode.ToLower,
			"gU": unicode.ToUpper, "U": unicode.ToUpper}[op]
		apply := func(l, a, b int) {
			ln := e.lines[l]
			for i := a; i < b && i < len(ln); i++ {
				ln[i] = f(ln[i])
			}
		}
		switch {
		case r.block:
			for l := r.s.Line; l <= r.en.Line; l++ {
				a, b := e.blockCols(l, r)
				apply(l, a, b)
			}
			e.cur = Pos{r.s.Line, ColForDisplay(e.lines[r.s.Line], r.dc1, e.Opts.TabStop, false)}
		case r.kind == mLine:
			for l := r.s.Line; l <= r.en.Line; l++ {
				apply(l, 0, len(e.lines[l]))
			}
			e.cur = Pos{r.s.Line, e.cur.Col}
			if r.fromVisual {
				e.cur.Col = 0
			}
		default:
			for l := r.s.Line; l <= r.en.Line; l++ {
				a, b := 0, len(e.lines[l])
				if l == r.s.Line {
					a = r.s.Col
				}
				if l == r.en.Line {
					b = r.en.Col
				}
				apply(l, a, b)
			}
			e.cur = r.s
		}
		e.gen++
		e.clampCursor(false)
	}
}

func (e *Engine) shiftLine(l int, right bool) {
	ln := e.lines[l]
	if len(ln) == 0 {
		return
	}
	ws := leadingWS(ln)
	w := displayWidth(ws, e.Opts.TabStop)
	sw := e.Opts.ShiftWidth
	if right {
		w = (w/sw + 1) * sw
	} else {
		w = max(0, (w+sw-1)/sw*sw-sw)
	}
	var ind []rune
	if e.Opts.ExpandTab {
		ind = []rune(strings.Repeat(" ", w))
	} else {
		ind = []rune(strings.Repeat("\t", w/e.Opts.TabStop) + strings.Repeat(" ", w%e.Opts.TabStop))
	}
	e.lines[l] = append(ind, ln[len(ws):]...)
}
