package vim

import (
	"regexp"
	"strings"
)

const (
	mExcl = iota
	mIncl
	mLine
)

func (e *Engine) lastLine() int { return len(e.lines) - 1 }

// colAtWant places the cursor column on line l from the desired column.
func (e *Engine) colAtWant(l int) int {
	ln := e.lines[l]
	if e.want < 0 {
		return lastCluster(ln)
	}
	return ColForDisplay(ln, e.want, e.Opts.TabStop, false)
}

// motion computes a motion target. count 0 means "no count given".
func (e *Engine) motion(name, arg string, count int, forOp bool) (Pos, int, bool) {
	n := count
	if n < 1 {
		n = 1
	}
	p := e.cur
	l := e.line()
	switch name {
	case "h", "left", "ctrl+h":
		if p.Col == 0 {
			return p, mExcl, false
		}
		for i := 0; i < n && p.Col > 0; i++ {
			p.Col = prevCluster(l, p.Col)
		}
		return p, mExcl, true
	case "l", "right":
		if len(l) == 0 || (!forOp && nextCluster(l, p.Col) >= len(l)) {
			return p, mExcl, forOp && len(l) > 0
		}
		for i := 0; i < n && p.Col < len(l); i++ {
			p.Col = nextCluster(l, p.Col)
		}
		if !forOp && p.Col >= len(l) {
			p.Col = lastCluster(l)
		}
		return p, mExcl, true
	case " ", "backspace":
		for i := 0; i < n; i++ {
			if name == " " {
				ln := e.lines[p.Line]
				if nx := nextCluster(ln, p.Col); nx < len(ln) {
					p.Col = nx
				} else if p.Line < e.lastLine() {
					p = Pos{p.Line + 1, 0}
				} else {
					break
				}
			} else {
				if p.Col > 0 {
					p.Col = prevCluster(e.lines[p.Line], p.Col)
				} else if p.Line > 0 {
					p = Pos{p.Line - 1, lastCluster(e.lines[p.Line-1])}
				}
			}
		}
		return p, mExcl, p != e.cur
	case "j", "down", "ctrl+n", "ctrl+j", "gj":
		if p.Line >= e.lastLine() {
			return p, mLine, false
		}
		p.Line = min(e.lastLine(), p.Line+n)
		p.Col = e.colAtWant(p.Line)
		return p, mLine, true
	case "k", "up", "ctrl+p", "gk":
		if p.Line == 0 {
			return p, mLine, false
		}
		p.Line = max(0, p.Line-n)
		p.Col = e.colAtWant(p.Line)
		return p, mLine, true
	case "0", "home", "g0":
		return Pos{p.Line, 0}, mExcl, true
	case "^":
		return Pos{p.Line, firstNonBlank(l)}, mExcl, true
	case "$", "end", "g$":
		p.Line = min(e.lastLine(), p.Line+n-1)
		ln := e.lines[p.Line]
		if forOp {
			return Pos{p.Line, max(0, len(ln)-1)}, mIncl, len(ln) > 0 || p.Line != e.cur.Line
		}
		return Pos{p.Line, lastCluster(ln)}, mIncl, true
	case "g_":
		p.Line = min(e.lastLine(), p.Line+n-1)
		ln := []rune(strings.TrimRight(string(e.lines[p.Line]), " \t"))
		return Pos{p.Line, lastCluster(ln)}, mIncl, true
	case "|":
		return Pos{p.Line, ColForDisplay(l, n-1, e.Opts.TabStop, false)}, mExcl, true
	case "gg":
		t := 0
		if count > 0 {
			t = min(e.lastLine(), count-1)
		}
		return Pos{t, firstNonBlank(e.lines[t])}, mLine, true
	case "G":
		t := e.lastLine()
		if count > 0 {
			t = min(e.lastLine(), count-1)
		}
		return Pos{t, firstNonBlank(e.lines[t])}, mLine, true
	case "_":
		t := min(e.lastLine(), p.Line+n-1)
		return Pos{t, firstNonBlank(e.lines[t])}, mLine, true
	case "+", "enter":
		if p.Line >= e.lastLine() {
			return p, mLine, false
		}
		t := min(e.lastLine(), p.Line+n)
		return Pos{t, firstNonBlank(e.lines[t])}, mLine, true
	case "-":
		if p.Line == 0 {
			return p, mLine, false
		}
		t := max(0, p.Line-n)
		return Pos{t, firstNonBlank(e.lines[t])}, mLine, true
	case "H", "M", "L":
		h := max(1, e.Height)
		bottom := min(e.lastLine(), e.Top+h-1)
		var t int
		switch name {
		case "H":
			t = min(bottom, e.Top+n-1)
		case "L":
			t = max(e.Top, bottom-n+1)
		default:
			t = e.Top + (bottom-e.Top)/2
		}
		return Pos{t, firstNonBlank(e.lines[t])}, mLine, true
	case "w", "W", "b", "B", "e", "E", "ge", "gE":
		return e.wordMotion(name, n, forOp)
	case "f", "F", "t", "T":
		t, ok := e.findChar(name, arg, n)
		if ok {
			e.lastFind.cmd, e.lastFind.ch = name, arg
		}
		kind := mIncl
		if name == "F" || name == "T" {
			kind = mExcl
		}
		return t, kind, ok
	case ";", ",":
		if e.lastFind.cmd == "" {
			return p, mExcl, false
		}
		cmd := e.lastFind.cmd
		if name == "," {
			cmd = map[string]string{"f": "F", "F": "f", "t": "T", "T": "t"}[cmd]
		}
		// `;` after t/T skips the adjacent match (cpo-;).
		save := e.cur
		if cmd == "t" {
			e.cur.Col = nextCluster(l, e.cur.Col)
		} else if cmd == "T" && e.cur.Col > 0 {
			e.cur.Col = prevCluster(l, e.cur.Col)
		}
		t, ok := e.findChar(cmd, e.lastFind.ch, n)
		e.cur = save
		kind := mIncl
		if cmd == "F" || cmd == "T" {
			kind = mExcl
		}
		return t, kind, ok
	case "%":
		if count > 0 {
			t := min(e.lastLine(), (count*len(e.lines)+99)/100-1)
			return Pos{t, firstNonBlank(e.lines[t])}, mLine, true
		}
		t, ok := e.matchPair()
		return t, mIncl, ok
	case "{", "}":
		t := p.Line
		for i := 0; i < n; i++ {
			if name == "}" {
				t++
				for t < len(e.lines) && isBlankLine(e.lines[t]) {
					t++
				}
				for t < len(e.lines) && !isBlankLine(e.lines[t]) {
					t++
				}
				if t >= len(e.lines) {
					t = e.lastLine()
					ln := e.lines[t]
					if forOp {
						return Pos{t, len(ln)}, mExcl, true
					}
					return Pos{t, lastCluster(ln)}, mExcl, true
				}
			} else {
				t--
				for t > 0 && isBlankLine(e.lines[t]) {
					t--
				}
				for t > 0 && !isBlankLine(e.lines[t]) {
					t--
				}
				if t <= 0 {
					return Pos{0, 0}, mExcl, true
				}
			}
		}
		return Pos{t, 0}, mExcl, true
	case "n", "N", "*", "#":
		return e.searchMotion(name, n)
	case "'", "`":
		r := []rune(arg)
		if len(r) != 1 {
			return p, mExcl, false
		}
		var mp Pos
		var ok bool
		switch r[0] {
		case '<', '>':
			if e.lastVisual == nil {
				return p, mExcl, false
			}
			s, en := order(e.lastVisual.start, e.lastVisual.end)
			mp, ok = s, true
			if r[0] == '>' {
				mp = en
			}
		default:
			mp, ok = e.marks[r[0]]
		}
		if !ok {
			e.setErr("E20: Mark not set")
			return p, mExcl, false
		}
		mp = clampPos(e.lines, mp)
		if name == "'" {
			return Pos{mp.Line, firstNonBlank(e.lines[mp.Line])}, mLine, true
		}
		return mp, mExcl, true
	}
	return p, mExcl, false
}

func (e *Engine) findChar(cmd, ch string, n int) (Pos, bool) {
	l := e.line()
	target := []rune(ch)
	if ch == "tab" {
		target = []rune{'\t'}
	} else if ch == " " {
		target = []rune{' '}
	}
	if len(target) != 1 {
		return e.cur, false
	}
	c := e.cur.Col
	switch cmd {
	case "f", "t":
		for i := 0; i < n; i++ {
			j := nextCluster(l, c)
			for j < len(l) && l[j] != target[0] {
				j++
			}
			if j >= len(l) {
				return e.cur, false
			}
			c = j
			if cmd == "t" && i < n-1 {
				// keep going from the found char
			}
		}
		if cmd == "t" {
			c = prevCluster(l, c)
		}
	default:
		for i := 0; i < n; i++ {
			j := c - 1
			for j >= 0 && l[j] != target[0] {
				j--
			}
			if j < 0 {
				return e.cur, false
			}
			c = j
		}
		if cmd == "T" {
			c = nextCluster(l, c)
		}
	}
	return Pos{e.cur.Line, c}, true
}

func (e *Engine) matchPair() (Pos, bool) {
	l := e.line()
	pairs := map[rune]rune{'(': ')', '[': ']', '{': '}', ')': '(', ']': '[', '}': '{'}
	c := e.cur.Col
	for c < len(l) {
		if _, ok := pairs[l[c]]; ok {
			break
		}
		c++
	}
	if c >= len(l) {
		return e.cur, false
	}
	open := l[c]
	close := pairs[open]
	fwd := open == '(' || open == '[' || open == '{'
	depth := 0
	p := Pos{e.cur.Line, c}
	for {
		r := e.lines[p.Line]
		if p.Col < len(r) {
			switch r[p.Col] {
			case open:
				depth++
			case close:
				depth--
				if depth == 0 {
					return p, true
				}
			}
		}
		if fwd {
			p.Col++
			for p.Col >= len(e.lines[p.Line]) {
				if p.Line >= e.lastLine() {
					return e.cur, false
				}
				p = Pos{p.Line + 1, 0}
				if len(e.lines[p.Line]) > 0 {
					break
				}
			}
		} else {
			p.Col--
			for p.Col < 0 {
				if p.Line == 0 {
					return e.cur, false
				}
				p.Line--
				p.Col = len(e.lines[p.Line]) - 1
			}
		}
	}
}

// --- words -----------------------------------------------------------

// clsAt is the class at p; the end-of-line position is a blank, an
// empty line is its own "word" (vim treats it as a stop).
func (e *Engine) clsAt(p Pos, big bool) int {
	l := e.lines[p.Line]
	if p.Col >= len(l) {
		return clsBlank
	}
	return class(l[p.Col], big)
}

// fwd steps one rune forward across lines; ok=false at buffer end.
func (e *Engine) fwd(p Pos) (Pos, bool) {
	if p.Col < len(e.lines[p.Line]) {
		return Pos{p.Line, p.Col + 1}, true
	}
	if p.Line >= e.lastLine() {
		return p, false
	}
	return Pos{p.Line + 1, 0}, true
}

func (e *Engine) back(p Pos) (Pos, bool) {
	if p.Col > 0 {
		return Pos{p.Line, p.Col - 1}, true
	}
	if p.Line == 0 {
		return p, false
	}
	return Pos{p.Line - 1, len(e.lines[p.Line-1])}, true
}

func (e *Engine) wordStartFwd(p Pos, big bool) Pos {
	c := e.clsAt(p, big)
	startLine := p.Line
	if c != clsBlank {
		for {
			q, ok := e.fwd(p)
			if !ok {
				return Pos{p.Line, len(e.lines[p.Line])}
			}
			p = q
			if p.Line != startLine || e.clsAt(p, big) != c {
				break
			}
		}
	}
	for {
		l := e.lines[p.Line]
		if p.Col == 0 && len(l) == 0 && p.Line != startLine {
			return p // an empty line is a word
		}
		if p.Col < len(l) && class(l[p.Col], big) != clsBlank {
			return p
		}
		q, ok := e.fwd(p)
		if !ok {
			return Pos{p.Line, len(l)}
		}
		p = q
	}
}

func (e *Engine) wordEndFwd(p Pos, big bool) Pos {
	q, ok := e.fwd(p)
	if !ok {
		return p
	}
	p = q
	for e.clsAt(p, big) == clsBlank {
		q, ok := e.fwd(p)
		if !ok {
			return p
		}
		p = q
	}
	c := e.clsAt(p, big)
	for {
		q, ok := e.fwd(p)
		if !ok || q.Line != p.Line || e.clsAt(q, big) != c {
			return p
		}
		p = q
	}
}

func (e *Engine) wordStartBack(p Pos, big bool) Pos {
	q, ok := e.back(p)
	if !ok {
		return p
	}
	p = q
	for e.clsAt(p, big) == clsBlank {
		if len(e.lines[p.Line]) == 0 && p != e.cur {
			return p
		}
		q, ok := e.back(p)
		if !ok {
			return p
		}
		p = q
	}
	c := e.clsAt(p, big)
	for p.Col > 0 && e.clsAt(Pos{p.Line, p.Col - 1}, big) == c {
		p.Col--
	}
	return p
}

func (e *Engine) wordEndBack(p Pos, big bool) Pos {
	c := e.clsAt(p, big)
	// leave the current word
	for {
		q, ok := e.back(p)
		if !ok {
			return Pos{0, 0}
		}
		p = q
		if e.clsAt(p, big) != c || p.Col >= len(e.lines[p.Line]) {
			break
		}
	}
	for e.clsAt(p, big) == clsBlank {
		if len(e.lines[p.Line]) == 0 {
			return p
		}
		q, ok := e.back(p)
		if !ok {
			return p
		}
		p = q
	}
	return p
}

func (e *Engine) wordMotion(name string, n int, forOp bool) (Pos, int, bool) {
	big := name == "W" || name == "B" || name == "E" || name == "gE"
	p := e.cur
	switch name {
	case "w", "W":
		for i := 0; i < n; i++ {
			q := e.wordStartFwd(p, big)
			if forOp && i == n-1 && q.Line > p.Line {
				// `dw` on the last word of a line stops at the line end.
				ln := e.lines[p.Line]
				if p.Line == e.cur.Line || !isBlankLine(ln) {
					return Pos{p.Line, len(ln)}, mExcl, true
				}
			}
			p = q
		}
		if !forOp {
			p.Col = min(p.Col, lastCluster(e.lines[p.Line]))
		}
		return p, mExcl, p != e.cur || forOp
	case "b", "B":
		for i := 0; i < n; i++ {
			p = e.wordStartBack(p, big)
		}
		return p, mExcl, p != e.cur
	case "e", "E":
		for i := 0; i < n; i++ {
			p = e.wordEndFwd(p, big)
		}
		p.Col = clusterStart(e.lines[p.Line], p.Col)
		return p, mIncl, p != e.cur
	default: // ge gE
		for i := 0; i < n; i++ {
			p = e.wordEndBack(p, big)
		}
		return p, mIncl, p != e.cur
	}
}

// --- search ----------------------------------------------------------

// compilePattern translates a vim (magic) pattern into a Go regexp with
// smartcase: case-insensitive unless the pattern has an upper-case
// letter. `\v` switches to very-magic (Go syntax as is).
func compilePattern(pat string) (*regexp.Regexp, error) {
	return compileVim(pat, "")
}

func compileVim(pat, flags string) (*regexp.Regexp, error) {
	var b strings.Builder
	rs := []rune(pat)
	ignore := !hasUpper(pat)
	if strings.Contains(flags, "I") {
		ignore = false
	}
	if strings.Contains(flags, "i") {
		ignore = true
	}
	veryMagic := false
	for i := 0; i < len(rs); i++ {
		r := rs[i]
		if r == '\\' && i+1 < len(rs) {
			nx := rs[i+1]
			i++
			switch nx {
			case 'v':
				veryMagic = true
			case 'c':
				ignore = true
			case 'C':
				ignore = false
			case '<', '>':
				b.WriteString(`\b`)
			case '(', ')', '|', '+', '?', '{', '}':
				if veryMagic {
					b.WriteString(`\` + string(nx))
				} else {
					b.WriteRune(nx)
				}
			case '=':
				b.WriteRune('?')
			case 'n':
				b.WriteString(`\n`)
			case 't':
				b.WriteString(`\t`)
			case 's', 'S', 'd', 'D', 'w', 'W', 'b', 'B':
				b.WriteString(`\` + string(nx))
			case 'a':
				b.WriteString(`[A-Za-z]`)
			case 'l':
				b.WriteString(`[a-z]`)
			case 'u':
				b.WriteString(`[A-Z]`)
			default:
				b.WriteString(regexp.QuoteMeta(string(nx)))
			}
			continue
		}
		switch r {
		case '(', ')', '|', '+', '?', '{', '}':
			if veryMagic {
				b.WriteRune(r)
			} else {
				b.WriteString(`\` + string(r))
			}
		case '~':
			b.WriteString(`~`)
		default:
			b.WriteRune(r)
		}
	}
	src := b.String()
	if ignore {
		src = "(?i)" + src
	}
	re, err := regexp.Compile(src)
	if err != nil {
		// fall back to a literal search
		lit := regexp.QuoteMeta(pat)
		if ignore {
			lit = "(?i)" + lit
		}
		return regexp.Compile(lit)
	}
	return re, nil
}

func hasUpper(s string) bool {
	skip := false
	for _, r := range s {
		if skip {
			skip = false
			continue
		}
		if r == '\\' {
			skip = true
			continue
		}
		if r >= 'A' && r <= 'Z' || (r > 127 && strings.ToLower(string(r)) != string(r)) {
			return true
		}
	}
	return false
}

// runeMatches returns rune ranges of non-empty matches in l.
func runeMatches(re *regexp.Regexp, l []rune) [][2]int {
	s := string(l)
	idx := re.FindAllStringIndex(s, -1)
	if len(idx) == 0 {
		return nil
	}
	// byte → rune offsets
	b2r := make([]int, len(s)+1)
	ri := 0
	for bi := range s {
		b2r[bi] = ri
		ri++
	}
	b2r[len(s)] = ri
	var out [][2]int
	for _, m := range idx {
		if m[1] > m[0] {
			out = append(out, [2]int{b2r[m[0]], b2r[m[1]]})
		}
	}
	return out
}

// search finds the n-th match from `from` (exclusive) in direction fwd.
func (e *Engine) search(pat string, from Pos, fwd bool, n int) (Pos, bool) {
	re, err := compilePattern(pat)
	if err != nil {
		e.setErr("E486: Pattern not found: " + pat)
		return from, false
	}
	p := from
	wrapped := false
	for i := 0; i < n; i++ {
		q, w, ok := e.searchOnce(re, p, fwd)
		if !ok {
			e.setErr("E486: Pattern not found: " + pat)
			return from, false
		}
		wrapped = wrapped || w
		p = q
	}
	if wrapped {
		if fwd {
			e.setMsg("search hit BOTTOM, continuing at TOP")
		} else {
			e.setMsg("search hit TOP, continuing at BOTTOM")
		}
	} else {
		dir := "/"
		if !fwd {
			dir = "?"
		}
		e.setMsg(dir + pat)
	}
	return p, true
}

func (e *Engine) searchOnce(re *regexp.Regexp, from Pos, fwd bool) (Pos, bool, bool) {
	nl := len(e.lines)
	for i := 0; i <= nl; i++ {
		var li int
		if fwd {
			li = (from.Line + i) % nl
		} else {
			li = ((from.Line-i)%nl + nl) % nl
		}
		ms := runeMatches(re, e.lines[li])
		if fwd {
			for _, m := range ms {
				if i == 0 && m[0] <= from.Col {
					continue
				}
				if i == nl && m[0] > from.Col {
					continue
				}
				return Pos{li, m[0]}, from.Line+i >= nl, true
			}
		} else {
			for j := len(ms) - 1; j >= 0; j-- {
				m := ms[j]
				if i == 0 && m[0] >= from.Col {
					continue
				}
				return Pos{li, m[0]}, from.Line-i < 0, true
			}
		}
	}
	return from, false, false
}

func (e *Engine) searchMotion(name string, n int) (Pos, int, bool) {
	switch name {
	case "*", "#":
		w := e.wordUnderCursor()
		if w == "" {
			e.setErr("E348: No string under cursor")
			return e.cur, mExcl, false
		}
		e.searchPat = `\<` + regexp.QuoteMeta(w) + `\>`
		e.searchFwd = name == "*"
		e.hlsearch = true
		// start from the word start so `#` skips the current word
		l := e.line()
		s := e.cur.Col
		for s > 0 && class(l[s-1], false) == clsWord {
			s--
		}
		p, ok := e.search(e.searchPat, Pos{e.cur.Line, s}, e.searchFwd, n)
		return p, mExcl, ok
	}
	if e.searchPat == "" {
		e.setErr("E35: No previous regular expression")
		return e.cur, mExcl, false
	}
	fwd := e.searchFwd
	if name == "N" {
		fwd = !fwd
	}
	e.hlsearch = true
	p, ok := e.search(e.searchPat, e.cur, fwd, n)
	return p, mExcl, ok
}

func (e *Engine) wordUnderCursor() string {
	l := e.line()
	c := e.cur.Col
	for c < len(l) && class(l[c], false) != clsWord {
		c++
	}
	if c >= len(l) {
		return ""
	}
	s, en := c, c
	for s > 0 && class(l[s-1], false) == clsWord {
		s--
	}
	for en < len(l) && class(l[en], false) == clsWord {
		en++
	}
	return string(l[s:en])
}
