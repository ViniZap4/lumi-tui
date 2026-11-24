package vim

import (
	"strings"
	"unicode"
)

func (e *Engine) startInsert(cmd string, count int) {
	e.mode = ModeInsert
	e.insCmd, e.insCount, e.insKeys = cmd, max(1, count), nil
	e.insStart = e.cur
	e.escBuf = nil
	e.clampCursor(true)
	e.startRecordingInsert()
}

func (e *Engine) startRecordingInsert() {
	e.recording = true
	e.recKeys = append([]string(nil), e.lastChange...)
}

// insertSeq is an insert-mode key sequence typed as text (escape
// sequences like "jj", and `inoremap jk <Esc>` style maps).
type insertSeq struct {
	keys    []string
	rhs     []string
	noremap bool
}

func (e *Engine) insertSeqs() []insertSeq {
	var out []insertSeq
	for _, s := range e.Opts.EscapeSeqs {
		var ks []string
		for _, r := range s {
			ks = append(ks, string(r))
		}
		if len(ks) >= 2 {
			out = append(out, insertSeq{keys: ks, rhs: []string{"esc"}, noremap: true})
		}
	}
	for _, m := range e.Opts.Maps {
		if m.Mode != "insert" || len(m.LHS) == 0 {
			continue
		}
		printable := true
		for _, k := range m.LHS {
			if !isPrintable(k) || len([]rune(k)) != 1 {
				printable = false
			}
		}
		if printable {
			out = append(out, insertSeq{keys: m.LHS, rhs: m.RHS, noremap: m.Noremap})
		}
	}
	return out
}

// tryInsertSeq handles a printable key against the typed sequences.
// It returns true when the key completed a sequence (already handled).
func (e *Engine) tryInsertSeq(k string) bool {
	if e.replaying {
		return false
	}
	seqs := e.insertSeqs()
	if len(seqs) == 0 {
		return false
	}
	now := e.Now()
	if len(e.escBuf) > 0 && now.Sub(e.escAt) > e.Opts.EscapeTimeout {
		e.escBuf = nil
	}
	try := func(cand []string) (full *insertSeq, partial bool) {
		for i := range seqs {
			s := seqs[i]
			if len(cand) > len(s.keys) {
				continue
			}
			match := true
			for j := range cand {
				if s.keys[j] != cand[j] {
					match = false
					break
				}
			}
			if !match {
				continue
			}
			if len(cand) == len(s.keys) {
				full = &s
			} else {
				partial = true
			}
		}
		return
	}
	cand := append(append([]string(nil), e.escBuf...), k)
	for len(cand) > 0 {
		full, partial := try(cand)
		if full != nil && len(cand) > 1 {
			// retract the keys already inserted
			for i := 0; i < len(cand)-1; i++ {
				e.insertKey0("backspace")
				if n := len(e.insKeys); n > 0 {
					e.insKeys = e.insKeys[:n-1]
				}
				if n := len(e.recKeys); n > 0 && e.recording {
					e.recKeys = e.recKeys[:n-1]
				}
			}
			if n := len(e.recKeys); n > 0 && e.recording {
				e.recKeys = e.recKeys[:n-1] // the completing key itself
			}
			e.escBuf = nil
			for _, rk := range full.rhs {
				if full.noremap {
					e.runKey(rk)
				} else {
					e.feedMapped(rk)
				}
			}
			return true
		}
		if partial {
			e.escBuf = cand
			e.escAt = now
			return false
		}
		cand = cand[1:]
	}
	e.escBuf = nil
	return false
}

func (e *Engine) insertKey(k string) {
	if e.insRegPending(k) {
		return
	}
	if k == "ctrl+r" {
		e.pend = []string{"ctrl+r"}
		return
	}
	if isPrintable(k) && len([]rune(k)) == 1 {
		if e.tryInsertSeq(k) {
			return
		}
	} else {
		e.escBuf = nil
		// non-printable insert maps (e.g. <C-s>)
		for i := len(e.Opts.Maps) - 1; i >= 0 && !e.replaying; i-- {
			m := e.Opts.Maps[i]
			if m.Mode == "insert" && len(m.LHS) == 1 && m.LHS[0] == k {
				for _, rk := range m.RHS {
					if m.Noremap {
						e.runKey(rk)
					} else {
						e.feedMapped(rk)
					}
				}
				return
			}
		}
	}
	switch k {
	case "esc", "ctrl+c", "ctrl+[":
		e.leaveInsert()
		return
	}
	e.insertKey0(k)
	e.insKeys = append(e.insKeys, k)
}

// insRegPending completes Ctrl-r {reg} in insert mode.
func (e *Engine) insRegPending(k string) bool {
	if len(e.pend) != 1 || e.pend[0] != "ctrl+r" {
		return false
	}
	e.pend = nil
	if r, ok := validReg(k); ok {
		txt := e.fetch(r)
		if r != '"' {
			txt = e.Register(r)
		}
		e.typeRunes([]rune(txt.Text))
		e.insKeys = append(e.insKeys, txt.Text)
	}
	return true
}

func (e *Engine) insertKey0(k string) {
	l := e.line()
	switch k {
	case "enter", "ctrl+m", "ctrl+j":
		ind := leadingWS(l)
		if e.cur.Col < len(ind) {
			ind = ind[:e.cur.Col]
		}
		rest := append(append([]rune(nil), ind...), l[e.cur.Col:]...)
		e.lines[e.cur.Line] = append([]rune(nil), l[:e.cur.Col]...)
		e.lines = append(e.lines[:e.cur.Line+1], append([][]rune{rest}, e.lines[e.cur.Line+1:]...)...)
		e.cur = Pos{e.cur.Line + 1, len(ind)}
		e.gen++
	case "backspace", "ctrl+h":
		if e.mode == ModeReplace {
			if n := len(e.replOrig); n > 0 && e.cur.Col > 0 {
				orig := e.replOrig[n-1]
				e.replOrig = e.replOrig[:n-1]
				p := prevCluster(l, e.cur.Col)
				if orig == 0 {
					e.lines[e.cur.Line] = append(append([]rune(nil), l[:p]...), l[e.cur.Col:]...)
				} else {
					l[p] = orig
				}
				e.cur.Col = p
				e.gen++
			} else if e.cur.Col > 0 {
				e.cur.Col = prevCluster(l, e.cur.Col)
			}
			return
		}
		if e.cur.Col > 0 {
			p := prevCluster(l, e.cur.Col)
			e.lines[e.cur.Line] = append(append([]rune(nil), l[:p]...), l[e.cur.Col:]...)
			e.cur.Col = p
			e.gen++
		} else if e.cur.Line > 0 {
			prev := e.lines[e.cur.Line-1]
			col := len(prev)
			e.lines[e.cur.Line-1] = append(append([]rune(nil), prev...), l...)
			e.lines = append(e.lines[:e.cur.Line], e.lines[e.cur.Line+1:]...)
			e.cur = Pos{e.cur.Line - 1, col}
			e.gen++
		}
	case "delete":
		if e.cur.Col < len(l) {
			nx := nextCluster(l, e.cur.Col)
			e.lines[e.cur.Line] = append(append([]rune(nil), l[:e.cur.Col]...), l[nx:]...)
			e.gen++
		} else if e.cur.Line < e.lastLine() {
			e.lines[e.cur.Line] = append(append([]rune(nil), l...), e.lines[e.cur.Line+1]...)
			e.lines = append(e.lines[:e.cur.Line+1], e.lines[e.cur.Line+2:]...)
			e.gen++
		}
	case "tab":
		if e.Opts.ExpandTab {
			dc := DisplayCol(l, e.cur.Col, e.Opts.TabStop)
			n := e.Opts.ShiftWidth - dc%e.Opts.ShiftWidth
			e.typeRunes([]rune(strings.Repeat(" ", n)))
		} else {
			e.typeRunes([]rune{'\t'})
		}
	case "ctrl+w":
		c := e.cur.Col
		for c > 0 && unicode.IsSpace(l[c-1]) {
			c--
		}
		if c > 0 {
			k := class(l[c-1], false)
			for c > 0 && class(l[c-1], false) == k && !unicode.IsSpace(l[c-1]) {
				c--
			}
		}
		if c == e.cur.Col && e.cur.Col == 0 {
			e.insertKey0("backspace")
			return
		}
		e.lines[e.cur.Line] = append(append([]rune(nil), l[:c]...), l[e.cur.Col:]...)
		e.cur.Col = c
		e.gen++
	case "ctrl+u":
		c := 0
		if e.insStart.Line == e.cur.Line && e.insStart.Col < e.cur.Col {
			c = e.insStart.Col
		}
		e.lines[e.cur.Line] = append(append([]rune(nil), l[:c]...), l[e.cur.Col:]...)
		e.cur.Col = c
		e.gen++
	case "ctrl+t", "ctrl+d":
		before := len(l)
		e.shiftLine(e.cur.Line, k == "ctrl+t")
		e.cur.Col = max(0, e.cur.Col+len(e.line())-before)
		e.gen++
	case "left":
		if e.cur.Col > 0 {
			e.cur.Col = prevCluster(l, e.cur.Col)
		}
	case "right":
		if e.cur.Col < len(l) {
			e.cur.Col = nextCluster(l, e.cur.Col)
		}
	case "up", "down":
		dc := DisplayCol(l, e.cur.Col, e.Opts.TabStop)
		if k == "up" && e.cur.Line > 0 {
			e.cur.Line--
		} else if k == "down" && e.cur.Line < e.lastLine() {
			e.cur.Line++
		}
		e.cur.Col = ColForDisplay(e.line(), dc, e.Opts.TabStop, true)
	case "home":
		e.cur.Col = 0
	case "end":
		e.cur.Col = len(l)
	default:
		if isPrintable(k) {
			e.typeRunes([]rune(k))
		}
	}
}

func (e *Engine) typeRunes(rs []rune) {
	// pasted text may carry newlines
	for _, r := range rs {
		if r == '\n' || r == '\r' {
			e.insertKey0("enter")
			continue
		}
		l := e.line()
		if e.mode == ModeReplace && e.cur.Col < len(l) {
			e.replOrig = append(e.replOrig, l[e.cur.Col])
			l[e.cur.Col] = r
			e.cur.Col++
			e.gen++
			continue
		}
		if e.mode == ModeReplace {
			e.replOrig = append(e.replOrig, 0)
		}
		nl := make([]rune, 0, len(l)+1)
		nl = append(nl, l[:e.cur.Col]...)
		nl = append(nl, r)
		nl = append(nl, l[e.cur.Col:]...)
		e.lines[e.cur.Line] = nl
		e.cur.Col++
		e.gen++
	}
}

func (e *Engine) leaveInsert() {
	// count repeat (3ix<Esc>, 2o…)
	if e.insCount > 1 && e.block == nil {
		keys := e.insKeys
		for i := 1; i < e.insCount; i++ {
			if e.insCmd == "o" || e.insCmd == "O" {
				e.openLine(true)
			}
			for _, k := range keys {
				e.insertKey0(k)
			}
		}
	}
	var typed strings.Builder
	for _, k := range e.insKeys {
		if isPrintable(k) {
			typed.WriteString(k)
		} else if k == "enter" {
			typed.WriteByte('\n')
		}
	}
	e.lastIns = typed.String()
	if b := e.block; b != nil {
		e.block = nil
		l := e.lines[b.line]
		if e.cur.Line == b.line && e.cur.Col > b.col && b.col <= len(l) {
			text := append([]rune(nil), l[b.col:e.cur.Col]...)
			for ln := b.l1; ln <= b.l2; ln++ {
				if ln == b.line {
					continue
				}
				row := e.lines[ln]
				var c int
				if b.toEOL {
					c = len(row)
				} else {
					w := displayWidth(row, e.Opts.TabStop)
					if w < b.dcol {
						if e.insCmd != "A" {
							continue
						}
						row = append(row, []rune(strings.Repeat(" ", b.dcol-w))...)
					}
					c = ColForDisplay(row, b.dcol, e.Opts.TabStop, true)
				}
				e.lines[ln] = append(append(append([]rune(nil), row[:c]...), text...), row[c:]...)
			}
			e.gen++
		}
		e.cur = Pos{b.l1, b.col}
	}
	e.marks['^'] = e.cur
	e.mode = ModeNormal
	if e.cur.Col > 0 {
		e.cur.Col = prevCluster(e.line(), e.cur.Col)
	}
	e.clampCursor(false)
	e.want = e.dcol()
	e.escBuf = nil
	e.replOrig = nil
	if e.recording {
		e.recording = false
		if len(e.recKeys) == 0 || e.recKeys[len(e.recKeys)-1] != "esc" {
			e.recKeys = append(e.recKeys, "esc")
		}
		e.lastChange = e.recKeys
		e.recKeys = nil
	}
}
