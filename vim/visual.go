package vim

import "strconv"

var visualCmds = map[string]bool{
	"d": true, "x": true, "X": true, "D": true, "y": true, "Y": true, "c": true, "s": true, "C": true, "S": true,
	"R": true, "J": true, ">": true, "<": true, "~": true, "u": true, "U": true, "p": true, "P": true, "I": true,
	"A": true, "o": true, "O": true, "v": true, "V": true, "ctrl+v": true, ":": true, "esc": true, "ctrl+c": true,
	"/": true, "?": true, "delete": true, "backspace": false,
}

func (e *Engine) exitVisual() {
	e.lastVisual = &visualSel{start: e.vStart, end: e.cur, mode: e.mode, toEOL: e.vToEOL}
	s, en := order(e.vStart, e.cur)
	e.marks['<'], e.marks['>'] = s, en
	e.mode = ModeNormal
	e.clampCursor(false)
}

// visualRange is the selection as an operator range.
func (e *Engine) visualRange() opRange {
	s, en := order(e.vStart, e.cur)
	switch e.mode {
	case ModeVisualLine:
		return opRange{s: Pos{s.Line, 0}, en: Pos{en.Line, 0}, kind: mLine, fromVisual: true}
	case ModeVisualBlock:
		ts := e.Opts.TabStop
		d1 := DisplayCol(e.lines[e.vStart.Line], e.vStart.Col, ts)
		d2 := DisplayCol(e.lines[e.cur.Line], e.cur.Col, ts)
		if d1 > d2 {
			d1, d2 = d2, d1
		}
		// include the full width of a wide char at the right edge
		return opRange{s: Pos{s.Line, 0}, en: Pos{en.Line, 0}, block: true, dc1: d1, dc2: d2, toEOL: e.vToEOL, fromVisual: true}
	}
	l := e.lines[en.Line]
	if en.Col < len(l) {
		en.Col = nextCluster(l, en.Col)
	} else if en.Line < e.lastLine() {
		en = Pos{en.Line + 1, 0}
	}
	return opRange{s: s, en: en, kind: mExcl, fromVisual: true}
}

func (e *Engine) visualKey(key string) {
	if key == "esc" || key == "ctrl+c" {
		if len(e.pend) > 0 {
			e.pend = nil
			return
		}
		e.exitVisual()
		return
	}
	e.pend = append(e.pend, key)
	keys := e.pend
	i := 0
	var reg rune
	if keys[0] == "\"" {
		if len(keys) < 2 {
			return
		}
		r, ok := validReg(keys[1])
		if !ok {
			e.pend = nil
			return
		}
		reg = r
		i = 2
	}
	count, j := readCount(keys, i)
	i = j
	if i >= len(keys) {
		return
	}
	k := keys[i]
	i++
	need := func() bool { return i >= len(keys) }
	switch {
	case k == "i" || k == "a":
		if need() {
			return
		}
		e.pend = nil
		s, en, lw, ok := e.textObject(k+keys[i], count)
		if !ok {
			return
		}
		if lw {
			e.mode = ModeVisualLine
			e.vStart, e.cur = s, Pos{en.Line, 0}
		} else {
			if e.vStart != e.cur && e.mode == ModeVisual {
				// extend
				if s.Less(e.vStart) {
					e.vStart = s
				}
			} else {
				e.vStart = s
			}
			if en.Col > 0 {
				en.Col = prevCluster(e.lines[en.Line], en.Col)
			} else if en.Line > s.Line {
				en = Pos{en.Line - 1, len(e.lines[en.Line-1])}
			}
			e.cur = en
		}
		return
	case k == "r":
		if need() {
			return
		}
		e.pend = nil
		e.visualReplace(keys[i])
		return
	case k == "g":
		if need() {
			return
		}
		g := keys[i]
		switch g {
		case "~", "u", "U":
			e.pend = nil
			e.visualOp("g"+g, reg, count)
			return
		case "J":
			e.pend = nil
			r := e.visualRange()
			e.exitVisual()
			e.join(r.s.Line, max(2, r.en.Line-r.s.Line+1), false)
			return
		case "v":
			e.pend = nil
			if e.lastVisual != nil {
				lv := *e.lastVisual
				e.exitVisual()
				e.vStart, e.cur, e.mode, e.vToEOL = lv.start, lv.end, lv.mode, lv.toEOL
			}
			return
		}
	}
	name, arg, st, ok := parseMotion(k, keys, &i)
	if ok {
		if st == pMore {
			return
		}
		e.pend = nil
		if st == pBad {
			return
		}
		t, _, ok := e.motion(name, arg, count, false)
		if ok {
			e.cur = t
			e.clampCursor(false)
			if name == "$" || name == "end" {
				e.want = -1
				if e.mode == ModeVisualBlock {
					e.vToEOL = true
				}
			} else if name != "j" && name != "k" && name != "up" && name != "down" {
				e.want = e.dcol()
				e.vToEOL = false
			}
		}
		return
	}
	e.pend = nil
	if !visualCmds[k] {
		return
	}
	e.visualCommand(k, reg, count)
}

func (e *Engine) visualOp(op string, reg rune, count int) {
	r := e.visualRange()
	mode := e.mode
	e.exitVisual()
	// dot repeat: same amount of text from the cursor
	n := 0
	switch {
	case r.kind == mLine:
		n = r.en.Line - r.s.Line
		keys := splitOp(op)
		if n > 0 {
			keys = append(keys, strconv.Itoa(n), "j")
		} else {
			keys = append(keys, keys[len(keys)-1])
		}
		if op != "y" {
			e.lastChange, e.lastCount = keys, 0
		}
	case !r.block && r.s.Line == r.en.Line && op != "y":
		n = r.en.Col - r.s.Col
		e.lastChange, e.lastCount = append(splitOp(op), strconv.Itoa(n), "l"), 0
	}
	_ = mode
	if r.block {
		e.cur = Pos{r.s.Line, ColForDisplay(e.lines[r.s.Line], r.dc1, e.Opts.TabStop, false)}
	} else {
		e.cur = r.s
	}
	if op == "c" {
		if !r.block {
			e.startRecordingInsertAfter()
		}
	}
	e.applyOp(op, reg, r, count)
}

// startRecordingInsertAfter is a no-op hook: applyOp("c") starts the
// insert, which records from lastChange.
func (e *Engine) startRecordingInsertAfter() {}

// visualReadOnly are the visual commands an unmodifiable buffer keeps.
var visualReadOnly = map[string]bool{"y": true, "Y": true, "o": true, "O": true, "v": true, "V": true,
	"ctrl+v": true, ":": true, "/": true, "?": true, "esc": true, "ctrl+c": true}

func (e *Engine) visualCommand(k string, reg rune, count int) {
	if e.Opts.ReadOnly && !visualReadOnly[k] {
		e.setErr(errReadOnly)
		return
	}
	switch k {
	case "d", "x", "delete":
		e.visualOp("d", reg, count)
	case "X", "D":
		if e.mode == ModeVisualBlock && k == "D" {
			e.vToEOL = true
			e.visualOp("d", reg, count)
			return
		}
		e.mode = ModeVisualLine
		e.visualOp("d", reg, count)
	case "y":
		e.visualOp("y", reg, count)
	case "Y":
		if e.mode != ModeVisualBlock {
			e.mode = ModeVisualLine
		}
		e.visualOp("y", reg, count)
	case "c", "s":
		e.visualOp("c", reg, count)
	case "C", "S", "R":
		if e.mode == ModeVisualBlock && k == "C" {
			e.vToEOL = true
			e.visualOp("c", reg, count)
			return
		}
		e.mode = ModeVisualLine
		e.visualOp("c", reg, count)
	case ">", "<":
		e.visualOp(k, reg, max(1, count))
	case "~", "u", "U":
		e.visualOp(k, reg, count)
	case "J":
		r := e.visualRange()
		e.exitVisual()
		e.join(r.s.Line, max(2, r.en.Line-r.s.Line+1), true)
	case "p", "P":
		src := e.fetch(reg)
		r := e.visualRange()
		e.exitVisual()
		e.checkpoint()
		deleted := e.rangeText(r)
		if r.kind == mLine {
			e.lines = append(e.lines[:r.s.Line], e.lines[r.en.Line+1:]...)
			if len(e.lines) == 0 {
				e.lines = [][]rune{{}}
			}
			e.gen++
			if src.Linewise {
				at := r.s.Line
				ins := splitLines(src.Text)
				if at > len(e.lines) {
					at = len(e.lines)
				}
				e.lines = append(e.lines[:at], append(ins, e.lines[at:]...)...)
				e.cur = Pos{at, firstNonBlank(e.lines[at])}
			} else {
				at := min(r.s.Line, len(e.lines))
				e.lines = append(e.lines[:at], append([][]rune{{}}, e.lines[at:]...)...)
				end := e.insertText(Pos{at, 0}, src.Text)
				e.cur = Pos{end.Line, max(0, end.Col-1)}
			}
		} else {
			e.deleteRange(r.s, r.en)
			text := src.Text
			p := r.s
			if src.Linewise {
				// a linewise register goes on its own lines
				head := e.lines[p.Line][:p.Col]
				tail := append([]rune(nil), e.lines[p.Line][p.Col:]...)
				e.lines[p.Line] = append([]rune(nil), head...)
				ins := splitLines(text)
				ins = append(ins, tail)
				e.lines = append(e.lines[:p.Line+1], append(ins, e.lines[p.Line+1:]...)...)
				e.cur = Pos{p.Line + 1, 0}
			} else {
				end := e.insertText(p, text)
				e.cur = Pos{end.Line, prevCluster(e.lines[end.Line], end.Col)}
			}
		}
		e.gen++
		if k == "p" {
			e.store(0, deleted, 'd')
		}
		e.clampCursor(false)
	case "I", "A":
		if e.mode == ModeVisualBlock {
			r := e.visualRange()
			e.exitVisual()
			e.checkpoint()
			dcol := r.dc1
			if k == "A" {
				dcol = r.dc2 + 1
				// right edge after a wide char
				ln := e.lines[r.s.Line]
				c := ColForDisplay(ln, r.dc2, e.Opts.TabStop, true)
				if c < len(ln) {
					dcol = DisplayCol(ln, nextCluster(ln, c), e.Opts.TabStop)
				}
			}
			ln := e.lines[r.s.Line]
			var col int
			if k == "A" && r.toEOL {
				col = len(ln)
			} else {
				if w := displayWidth(ln, e.Opts.TabStop); w < dcol {
					e.lines[r.s.Line] = append(ln, []rune(repeatSpace(dcol-w))...)
				}
				col = ColForDisplay(e.lines[r.s.Line], dcol, e.Opts.TabStop, true)
			}
			e.cur = Pos{r.s.Line, col}
			e.block = &blockInsert{l1: r.s.Line, l2: r.en.Line, dcol: dcol, toEOL: k == "A" && r.toEOL, line: r.s.Line, col: col}
			e.lastChange = nil
			e.startInsert(k, 1)
			return
		}
		s, en := order(e.vStart, e.cur)
		wasLine := e.mode == ModeVisualLine
		e.exitVisual()
		e.checkpoint()
		if k == "I" {
			e.cur = s
			if wasLine {
				e.cur.Col = firstNonBlank(e.line())
			}
		} else {
			e.cur = en
			if l := e.line(); len(l) > 0 {
				e.cur.Col = nextCluster(l, e.cur.Col)
			}
		}
		e.startInsert("i", 1)
	case "o":
		e.vStart, e.cur = e.cur, e.vStart
		e.want = e.dcol()
	case "O":
		if e.mode == ModeVisualBlock {
			ts := e.Opts.TabStop
			d1 := DisplayCol(e.lines[e.vStart.Line], e.vStart.Col, ts)
			d2 := e.dcol()
			e.vStart.Col = ColForDisplay(e.lines[e.vStart.Line], d2, ts, false)
			e.cur.Col = ColForDisplay(e.line(), d1, ts, false)
		} else {
			e.vStart, e.cur = e.cur, e.vStart
		}
	case "v", "V", "ctrl+v":
		m := map[string]Mode{"v": ModeVisual, "V": ModeVisualLine, "ctrl+v": ModeVisualBlock}[k]
		if e.mode == m {
			e.exitVisual()
		} else {
			e.mode = m
		}
	case ":":
		e.exitVisual()
		e.openCmdline(':', "'<,'>")
	case "/", "?":
		e.cmdFromVi = true
		saved := e.mode
		e.openCmdline([]rune(k)[0], "")
		e.viReturn = saved
	}
}

func repeatSpace(n int) string {
	b := make([]rune, n)
	for i := range b {
		b[i] = ' '
	}
	return string(b)
}

func (e *Engine) visualReplace(ch string) {
	rs := []rune(ch)
	if ch == "tab" {
		rs = []rune{'\t'}
	}
	if len(rs) != 1 {
		e.exitVisual()
		return
	}
	r := e.visualRange()
	e.exitVisual()
	e.checkpoint()
	for l := r.s.Line; l <= r.en.Line; l++ {
		ln := e.lines[l]
		a, b := 0, len(ln)
		switch {
		case r.block:
			a, b = e.blockCols(l, r)
		case r.kind != mLine:
			if l == r.s.Line {
				a = r.s.Col
			}
			if l == r.en.Line {
				b = min(b, r.en.Col)
			}
		}
		var out []rune
		out = append(out, ln[:a]...)
		for i := a; i < b; i = nextCluster(ln, i) {
			out = append(out, rs[0])
		}
		out = append(out, ln[b:]...)
		e.lines[l] = out
	}
	e.gen++
	e.cur = r.s
	if r.block {
		e.cur.Col = ColForDisplay(e.lines[r.s.Line], r.dc1, e.Opts.TabStop, false)
	}
	e.clampCursor(false)
}
