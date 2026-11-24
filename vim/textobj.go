package vim

// textObject returns the range [s, en) of a text object (iw, a", i(, ip…)
// and whether it is linewise.
func (e *Engine) textObject(name string, count int) (Pos, Pos, bool, bool) {
	if len(name) != 2 && !(len(name) > 2) {
		return Pos{}, Pos{}, false, false
	}
	inner := name[0] == 'i'
	obj := name[1:]
	switch obj {
	case "w", "W":
		s, en, ok := e.wordObject(inner, obj == "W", max(1, count))
		return s, en, false, ok
	case "\"", "'", "`":
		s, en, ok := e.quoteObject(inner, []rune(obj)[0])
		return s, en, false, ok
	case "(", ")", "b":
		return e.bracketObject(inner, '(', ')', max(1, count))
	case "[", "]":
		return e.bracketObject(inner, '[', ']', max(1, count))
	case "{", "}", "B":
		return e.bracketObject(inner, '{', '}', max(1, count))
	case "<", ">":
		return e.bracketObject(inner, '<', '>', max(1, count))
	case "p":
		s, en, ok := e.paraObject(inner, max(1, count))
		return s, en, true, ok
	}
	return Pos{}, Pos{}, false, false
}

func (e *Engine) wordObject(inner, big bool, count int) (Pos, Pos, bool) {
	l := e.line()
	if len(l) == 0 {
		return e.cur, e.cur, false
	}
	c := min(e.cur.Col, len(l)-1)
	runEnd := func(i int) int {
		k := class(l[i], big)
		for i < len(l) && class(l[i], big) == k {
			i++
		}
		return i
	}
	start := c
	k := class(l[c], big)
	for start > 0 && class(l[start-1], big) == k {
		start--
	}
	end := c
	for n := 0; n < count; n++ {
		if end >= len(l) {
			break
		}
		if inner {
			end = runEnd(end)
			continue
		}
		// aw: word + trailing blanks (or blanks + word when on blanks)
		if class(l[end], big) == clsBlank {
			end = runEnd(end)
			if end < len(l) {
				end = runEnd(end)
			}
		} else {
			end = runEnd(end)
			if end < len(l) && class(l[end], big) == clsBlank {
				end = runEnd(end)
			} else if n == 0 {
				// no trailing blanks: take leading ones
				for start > 0 && class(l[start-1], big) == clsBlank {
					start--
				}
			}
		}
	}
	return Pos{e.cur.Line, start}, Pos{e.cur.Line, end}, true
}

func (e *Engine) quoteObject(inner bool, q rune) (Pos, Pos, bool) {
	l := e.line()
	var qs []int
	for i := 0; i < len(l); i++ {
		if l[i] == '\\' {
			i++
			continue
		}
		if l[i] == q {
			qs = append(qs, i)
		}
	}
	c := e.cur.Col
	for i := 0; i+1 < len(qs); i += 2 {
		a, b := qs[i], qs[i+1]
		if c >= a && c <= b || c < a {
			if inner {
				return Pos{e.cur.Line, a + 1}, Pos{e.cur.Line, b}, true
			}
			end := b + 1
			start := a
			if end < len(l) && (l[end] == ' ' || l[end] == '\t') {
				for end < len(l) && (l[end] == ' ' || l[end] == '\t') {
					end++
				}
			} else {
				for start > 0 && (l[start-1] == ' ' || l[start-1] == '\t') {
					start--
				}
			}
			return Pos{e.cur.Line, start}, Pos{e.cur.Line, end}, true
		}
	}
	return e.cur, e.cur, false
}

func (e *Engine) bracketObject(inner bool, open, close rune, count int) (Pos, Pos, bool, bool) {
	// find the count-th enclosing open bracket
	p := e.cur
	l := e.line()
	var op Pos
	found := false
	depth := 0
	if p.Col < len(l) && l[p.Col] == open {
		op, found = p, true
		count--
	} else if p.Col < len(l) && l[p.Col] == close {
		depth = 1 // the close under the cursor belongs to the pair we want
		count = max(1, count)
	}
	q := p
	for count > 0 {
		var ok bool
		q, ok = e.back(q)
		if !ok {
			break
		}
		r := e.lines[q.Line]
		if q.Col >= len(r) {
			continue
		}
		switch r[q.Col] {
		case close:
			depth++
		case open:
			if depth > 0 {
				depth--
				continue
			}
			op, found = q, true
			count--
		}
	}
	if !found {
		return Pos{}, Pos{}, false, false
	}
	// matching close
	depth = 0
	q = op
	var cp Pos
	ok := false
	for {
		var more bool
		q, more = e.fwd(q)
		if !more {
			break
		}
		r := e.lines[q.Line]
		if q.Col >= len(r) {
			continue
		}
		if r[q.Col] == open {
			depth++
		} else if r[q.Col] == close {
			if depth == 0 {
				cp, ok = q, true
				break
			}
			depth--
		}
	}
	if !ok {
		return Pos{}, Pos{}, false, false
	}
	if !inner {
		return op, Pos{cp.Line, cp.Col + 1}, false, true
	}
	s := Pos{op.Line, op.Col + 1}
	en := cp
	// multi-line inner block whose brackets sit alone: linewise inside
	if op.Line < cp.Line && s.Col >= len(e.lines[op.Line]) && cp.Col <= firstNonBlank(e.lines[cp.Line]) {
		if cp.Line-op.Line < 2 {
			return Pos{op.Line, len(e.lines[op.Line])}, Pos{op.Line, len(e.lines[op.Line])}, false, true
		}
		return Pos{op.Line + 1, 0}, Pos{cp.Line - 1, 0}, true, true
	}
	if s.Col >= len(e.lines[s.Line]) && s.Line < en.Line {
		s = Pos{s.Line + 1, 0}
	}
	return s, en, false, true
}

func (e *Engine) paraObject(inner bool, count int) (Pos, Pos, bool) {
	ln := e.cur.Line
	blank := isBlankLine(e.lines[ln])
	s := ln
	for s > 0 && isBlankLine(e.lines[s-1]) == blank {
		s--
	}
	en := ln
	for n := 0; n < count; n++ {
		b := isBlankLine(e.lines[en])
		for en+1 < len(e.lines) && isBlankLine(e.lines[en+1]) == b {
			en++
		}
		if !inner && !b && en+1 < len(e.lines) {
			en++
			for en+1 < len(e.lines) && isBlankLine(e.lines[en+1]) {
				en++
			}
		}
		if n < count-1 && en+1 < len(e.lines) {
			en++
		}
	}
	return Pos{s, 0}, Pos{en, 0}, true
}
