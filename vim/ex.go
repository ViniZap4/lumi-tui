package vim

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode"
)

func (e *Engine) openCmdline(kind rune, pre string) {
	if e.mode != ModeCmdline {
		e.cmdPrev = e.mode
	}
	e.mode = ModeCmdline
	e.cmdKind = kind
	e.cmdline = []rune(pre)
	e.histIdx = len(e.cmdHist)
}

func (e *Engine) closeCmdline() {
	e.mode = ModeNormal
	if e.cmdFromVi {
		e.mode = e.viReturn
		e.cmdFromVi = false
	}
	e.cmdOp = nil
}

func (e *Engine) cmdlineKey(k string) {
	if len(e.pend) == 1 && e.pend[0] == "ctrl+r" {
		e.pend = nil
		if r, ok := validReg(k); ok {
			e.cmdline = append(e.cmdline, []rune(e.fetch(r).Text)...)
		}
		return
	}
	switch k {
	case "esc", "ctrl+c":
		e.closeCmdline()
	case "enter", "ctrl+m":
		line := string(e.cmdline)
		kind := e.cmdKind
		op := e.cmdOp
		fromVi := e.cmdFromVi
		e.closeCmdline()
		if kind == ':' {
			if strings.TrimSpace(line) != "" {
				e.pushHist(line)
			}
			e.execEx(line)
			return
		}
		e.finishSearch(line, kind == '/', op, fromVi)
	case "backspace", "ctrl+h":
		if len(e.cmdline) == 0 {
			e.closeCmdline()
			return
		}
		e.cmdline = e.cmdline[:len(e.cmdline)-1]
	case "ctrl+u":
		e.cmdline = nil
	case "ctrl+w":
		s, _ := editWord(string(e.cmdline))
		e.cmdline = []rune(s)
	case "ctrl+r":
		e.pend = []string{"ctrl+r"}
	case "up", "down":
		if e.cmdKind != ':' || len(e.cmdHist) == 0 {
			return
		}
		if k == "up" && e.histIdx > 0 {
			e.histIdx--
		} else if k == "down" && e.histIdx < len(e.cmdHist) {
			e.histIdx++
		}
		if e.histIdx < len(e.cmdHist) {
			e.cmdline = []rune(e.cmdHist[e.histIdx])
		} else {
			e.cmdline = nil
		}
	case "tab":
	default:
		if isPrintable(k) {
			e.cmdline = append(e.cmdline, []rune(k)...)
		}
	}
}

func editWord(s string) (string, bool) {
	r := []rune(strings.TrimRightFunc(s, unicode.IsSpace))
	i := len(r)
	for i > 0 && !unicode.IsSpace(r[i-1]) {
		i--
	}
	return string(r[:i]), true
}

func (e *Engine) pushHist(line string) {
	if n := len(e.cmdHist); n > 0 && e.cmdHist[n-1] == line {
		return
	}
	e.cmdHist = append(e.cmdHist, line)
	if len(e.cmdHist) > 100 {
		e.cmdHist = e.cmdHist[1:]
	}
}

func (e *Engine) finishSearch(pat string, fwd bool, op *command, fromVi bool) {
	if pat == "" {
		pat = e.searchPat
	}
	if pat == "" {
		e.setErr("E35: No previous regular expression")
		return
	}
	e.searchPat, e.searchFwd, e.hlsearch = pat, fwd, true
	t, ok := e.search(pat, e.cur, fwd, 1)
	if !ok {
		return
	}
	if op != nil {
		s, en := order(e.cur, t)
		e.applyOp(op.op, op.reg, opRange{s: s, en: en, kind: mExcl}, max(1, op.count))
		return
	}
	e.marks['\''] = e.cur
	e.cur = t
	e.want = e.dcol()
}

// --- ex commands -----------------------------------------------------

// parseAddr reads one address at s[i:]; ok=false when there is none.
func (e *Engine) parseAddr(s []rune, i int) (int, int, bool, error) {
	line := -1
	switch {
	case i < len(s) && s[i] == '.':
		line = e.cur.Line
		i++
	case i < len(s) && s[i] == '$':
		line = e.lastLine()
		i++
	case i < len(s) && s[i] >= '0' && s[i] <= '9':
		j := i
		for j < len(s) && s[j] >= '0' && s[j] <= '9' {
			j++
		}
		n, _ := strconv.Atoi(string(s[i:j]))
		line = n - 1
		i = j
	case i+1 < len(s) && s[i] == '\'':
		m := s[i+1]
		var p Pos
		ok := false
		if (m == '<' || m == '>') && e.lastVisual != nil {
			a, b := order(e.lastVisual.start, e.lastVisual.end)
			p, ok = a, true
			if m == '>' {
				p = b
			}
		} else {
			p, ok = e.marks[m]
		}
		if !ok {
			return 0, i, false, fmt.Errorf("E20: Mark not set")
		}
		line = p.Line
		i += 2
	}
	// offsets
	for i < len(s) && (s[i] == '+' || s[i] == '-') {
		sign := 1
		if s[i] == '-' {
			sign = -1
		}
		i++
		j := i
		for j < len(s) && s[j] >= '0' && s[j] <= '9' {
			j++
		}
		n := 1
		if j > i {
			n, _ = strconv.Atoi(string(s[i:j]))
		}
		if line < 0 {
			line = e.cur.Line
		}
		line += sign * n
		i = j
	}
	return line, i, line >= 0 || line < -1, nil
}

// execEx runs one ex command line.
func (e *Engine) execEx(line string) {
	s := []rune(strings.TrimSpace(strings.TrimLeft(line, ": \t")))
	i := 0
	l1, l2 := -1, -1
	hasRange := false
	if i < len(s) && s[i] == '%' {
		l1, l2, hasRange = 0, e.lastLine(), true
		i++
	} else {
		a, j, ok, err := e.parseAddr(s, i)
		if err != nil {
			e.setErr(err.Error())
			return
		}
		if ok {
			l1, l2, hasRange = a, a, true
			i = j
		}
		if i < len(s) && (s[i] == ',' || s[i] == ';') {
			i++
			b, j, ok, err := e.parseAddr(s, i)
			if err != nil {
				e.setErr(err.Error())
				return
			}
			if ok {
				l2 = b
				i = j
			}
			if l1 < 0 {
				l1 = e.cur.Line
			}
			hasRange = true
		}
	}
	if hasRange {
		if l1 > l2 {
			l1, l2 = l2, l1
		}
		if l1 < 0 || l2 > e.lastLine() {
			if l2 > e.lastLine() && l1 <= e.lastLine() && len(s[i:]) == 0 {
				l2 = e.lastLine()
				l1 = min(l1, l2)
			} else {
				e.setErr("E16: Invalid range")
				return
			}
		}
	} else {
		l1, l2 = e.cur.Line, e.cur.Line
	}
	rest := strings.TrimSpace(string(s[i:]))
	if rest == "" {
		if hasRange {
			e.marks['\''] = e.cur
			e.cur = Pos{l2, firstNonBlank(e.lines[l2])}
		}
		return
	}
	// command name: letters, or a single non-letter (s/…, &, <, >)
	j := 0
	rr := []rune(rest)
	for j < len(rr) && unicode.IsLetter(rr[j]) {
		j++
	}
	name := string(rr[:j])
	if j == 0 {
		name = string(rr[0])
		j = 1
	}
	bang := false
	if j < len(rr) && rr[j] == '!' {
		bang = true
		j++
	}
	args := strings.TrimSpace(string(rr[j:]))
	if alias, ok := e.Opts.ExAliases[name]; ok && e.depth < 10 {
		e.depth++
		e.execEx(alias + " " + args)
		e.depth--
		return
	}
	if e.Opts.ReadOnly && exChanges(name, rr) {
		e.setErr(errReadOnly)
		return
	}
	// `:s/a/b/` — the separator follows the name directly
	if rr[0] == 's' && (len(rr) == 1 || (!unicode.IsLetter(rr[1]) && !unicode.IsDigit(rr[1]) && rr[1] != ' ' && rr[1] != '!')) {
		e.exSubstitute(l1, l2, string(rr[1:]))
		return
	}
	switch name {
	case "w", "write", "up", "update":
		e.emit(Effect{Kind: EffWrite, Bang: bang, Args: args})
	case "q", "quit", "clo", "close":
		e.emit(Effect{Kind: EffQuit, Bang: bang})
	case "wq", "x", "xit", "exi", "exit":
		e.emit(Effect{Kind: EffWriteQuit, Bang: bang})
	case "wa", "wall":
		e.emit(Effect{Kind: EffWrite, Name: "all"})
	case "qa", "qall", "quita", "quitall":
		e.emit(Effect{Kind: EffQuit, Name: "all", Bang: bang})
	case "wqa", "wqall", "xa", "xall":
		e.emit(Effect{Kind: EffWriteQuit, Name: "all"})
	case "e", "edit":
		if bang {
			e.emit(Effect{Kind: EffEditExternal})
			return
		}
		e.emit(Effect{Kind: EffEx, Name: "edit", Args: args})
	case "ext", "external":
		e.emit(Effect{Kind: EffEditExternal})
	case "s", "substitute":
		e.exSubstitute(l1, l2, args)
	case "&", "&&":
		if !e.lastSub.ok {
			e.setErr("E35: No previous regular expression")
			return
		}
		flags := args
		if name == "&&" || strings.HasPrefix(args, "&") {
			flags = e.lastSub.flags + strings.TrimPrefix(args, "&")
		}
		e.substitute(l1, l2, e.lastSub.pat, e.lastSub.rep, flags, true)
	case "d", "de", "del", "delete":
		reg := rune(0)
		if args != "" {
			reg = []rune(args)[0]
		}
		e.applyOp("d", reg, opRange{s: Pos{l1, 0}, en: Pos{l2, 0}, kind: mLine}, 1)
	case "y", "ya", "yank":
		reg := rune(0)
		if args != "" {
			reg = []rune(args)[0]
		}
		save := e.cur
		e.applyOp("y", reg, opRange{s: Pos{l1, 0}, en: Pos{l2, 0}, kind: mLine, fromVisual: true}, 1)
		e.cur = save
	case ">", "<":
		e.applyOp(name, 0, opRange{s: Pos{l1, 0}, en: Pos{l2, 0}, kind: mLine, fromVisual: true}, 1+strings.Count(args, name))
	case "j", "join":
		n := l2 - l1 + 1
		if !hasRange || l1 == l2 {
			n = 2
		}
		e.join(l1, n, !bang)
	case "noh", "nohl", "nohlsearch":
		e.hlsearch = false
	case "u", "un", "undo":
		e.execSimple(command{name: "u"}, 1)
	case "red", "redo":
		e.execSimple(command{name: "ctrl+r"}, 1)
	case "sor", "sort":
		if !hasRange {
			l1, l2 = 0, e.lastLine()
		}
		e.sortLines(l1, l2, bang, args)
	case "set", "se", "setl", "setlocal":
		e.exSet(args)
	case "reg", "registers", "di", "display":
		var parts []string
		for _, r := range `"0123456789-abcdefghijklmnopqrstuvwxyz` {
			if t := e.regs[r].Text; t != "" {
				t = strings.ReplaceAll(t, "\n", "^J")
				if len([]rune(t)) > 30 {
					t = string([]rune(t)[:30]) + "…"
				}
				parts = append(parts, fmt.Sprintf("\"%c %s", r, t))
			}
		}
		if len(parts) == 0 {
			e.setMsg("registers are empty")
		} else {
			e.setMsg(strings.Join(parts, "  "))
		}
	case "norm", "normal":
		ks, err := ParseKeys(args, e.Opts.Leader)
		if err != nil || e.depth > 10 {
			return
		}
		e.depth++
		for l := l1; l <= l2; l++ {
			e.cur = Pos{l, 0}
			for _, k := range ks {
				e.runKey(k)
			}
			if e.mode == ModeInsert || e.mode == ModeReplace {
				e.leaveInsert()
			}
			e.mode = ModeNormal
		}
		e.depth--
	default:
		e.emit(Effect{Kind: EffEx, Name: name, Args: args, Bang: bang})
	}
}

func (e *Engine) exSet(args string) {
	for _, tok := range strings.Fields(args) {
		var diags []Diagnostic
		o := e.Opts
		applySetToken(tok, 0, &o, &diags)
		if len(diags) > 0 && diags[0].Severity == SevError {
			e.setErr(diags[0].Message)
			continue
		}
		e.Opts = o
		switch tok {
		case "hlsearch", "hls":
			e.hlsearch = true
		case "nohlsearch", "nohls":
			e.hlsearch = false
		}
	}
}

func (e *Engine) sortLines(l1, l2 int, reverse bool, args string) {
	e.checkpoint()
	rows := make([]string, 0, l2-l1+1)
	for l := l1; l <= l2; l++ {
		rows = append(rows, string(e.lines[l]))
	}
	ic := strings.Contains(args, "i")
	sort.SliceStable(rows, func(a, b int) bool {
		x, y := rows[a], rows[b]
		if ic {
			x, y = strings.ToLower(x), strings.ToLower(y)
		}
		if reverse {
			return x > y
		}
		return x < y
	})
	if strings.Contains(args, "u") {
		var out []string
		for i, r := range rows {
			if i == 0 || r != rows[i-1] {
				out = append(out, r)
			}
		}
		rows = out
	}
	var nl [][]rune
	for _, r := range rows {
		nl = append(nl, []rune(r))
	}
	e.lines = append(e.lines[:l1], append(nl, e.lines[l2+1:]...)...)
	e.gen++
	e.clampCursor(false)
}

// exSubstitute parses "/pat/rep/flags" (any non-alnum separator).
func (e *Engine) exSubstitute(l1, l2 int, spec string) {
	rs := []rune(strings.TrimLeft(spec, " "))
	if len(rs) == 0 {
		if !e.lastSub.ok {
			e.setErr("E35: No previous regular expression")
			return
		}
		e.substitute(l1, l2, e.lastSub.pat, e.lastSub.rep, "", true)
		return
	}
	sep := rs[0]
	if unicode.IsLetter(sep) || unicode.IsDigit(sep) || sep == '\\' || sep == '"' || sep == '|' {
		e.setErr("E146: Regular expressions can't be delimited by letters")
		return
	}
	var parts []string
	var cur strings.Builder
	for i := 1; i < len(rs); i++ {
		if rs[i] == '\\' && i+1 < len(rs) {
			if rs[i+1] == sep {
				cur.WriteRune(sep)
			} else {
				cur.WriteRune('\\')
				cur.WriteRune(rs[i+1])
			}
			i++
			continue
		}
		if rs[i] == sep && len(parts) < 2 {
			parts = append(parts, cur.String())
			cur.Reset()
			continue
		}
		cur.WriteRune(rs[i])
	}
	parts = append(parts, cur.String())
	pat := parts[0]
	rep := ""
	flags := ""
	if len(parts) > 1 {
		rep = parts[1]
	}
	if len(parts) > 2 {
		flags = strings.TrimSpace(parts[2])
	}
	if pat == "" {
		pat = e.searchPat
	}
	if rep == "~" {
		rep = e.lastSub.rep
	}
	e.substitute(l1, l2, pat, rep, flags, true)
}

// substitute runs :s over lines l1..l2.
func (e *Engine) substitute(l1, l2 int, pat, rep, flags string, report bool) {
	if pat == "" {
		e.setErr("E35: No previous regular expression")
		return
	}
	re, err := compileVim(pat, flags)
	if err != nil {
		e.setErr("E486: Pattern not found: " + pat)
		return
	}
	e.lastSub.pat, e.lastSub.rep, e.lastSub.flags, e.lastSub.ok = pat, rep, flags, true
	e.searchPat = pat
	global := strings.Contains(flags, "g")
	countOnly := strings.Contains(flags, "n")
	total, linesHit := 0, 0
	var out [][]rune
	lastHit := -1
	changed := false
	for l := 0; l < len(e.lines); l++ {
		if l < l1 || l > l2 {
			out = append(out, e.lines[l])
			continue
		}
		s := string(e.lines[l])
		n := 0
		res := replaceVim(re, s, rep, global, &n)
		if n > 0 {
			total += n
			linesHit++
			lastHit = len(out)
			if !countOnly {
				changed = true
				for _, p := range strings.Split(res, "\n") {
					out = append(out, []rune(p))
				}
				lastHit = len(out) - 1
				continue
			}
		}
		out = append(out, e.lines[l])
	}
	if total == 0 {
		if !strings.Contains(flags, "e") {
			e.setErr("E486: Pattern not found: " + pat)
		}
		return
	}
	if countOnly {
		e.setMsg(fmt.Sprintf("%d match%s on %d line%s", total, esPlural(total), linesHit, plural(linesHit)))
		return
	}
	if changed {
		e.checkpoint()
		e.lines = out
		e.gen++
		e.cur = Pos{lastHit, firstNonBlank(e.lines[lastHit])}
	}
	if report && (total > 2 || linesHit > 2) {
		e.setMsg(fmt.Sprintf("%d substitution%s on %d line%s", total, plural(total), linesHit, plural(linesHit)))
	}
}

func esPlural(n int) string {
	if n == 1 {
		return ""
	}
	return "es"
}

// replaceVim replaces matches of re in s using a vim replacement
// string (& \0-\9 \r \n \t \u \U \l \L \e \E).
func replaceVim(re *regexp.Regexp, s, rep string, global bool, n *int) string {
	var b strings.Builder
	last := 0
	for _, m := range re.FindAllStringSubmatchIndex(s, -1) {
		if m[1] == m[0] && m[0] < last {
			continue
		}
		b.WriteString(s[last:m[0]])
		b.WriteString(expandRep(s, m, rep))
		last = m[1]
		*n++
		if !global {
			break
		}
	}
	b.WriteString(s[last:])
	return b.String()
}

func expandRep(s string, m []int, rep string) string {
	var b strings.Builder
	group := func(g int) string {
		if 2*g+1 < len(m) && m[2*g] >= 0 {
			return s[m[2*g]:m[2*g+1]]
		}
		return ""
	}
	caseOne := 0 // 1 upper next, -1 lower next
	caseAll := 0
	write := func(t string) {
		for _, r := range t {
			switch {
			case caseOne == 1:
				r = unicode.ToUpper(r)
				caseOne = 0
			case caseOne == -1:
				r = unicode.ToLower(r)
				caseOne = 0
			case caseAll == 1:
				r = unicode.ToUpper(r)
			case caseAll == -1:
				r = unicode.ToLower(r)
			}
			b.WriteRune(r)
		}
	}
	rs := []rune(rep)
	for i := 0; i < len(rs); i++ {
		r := rs[i]
		if r == '&' {
			write(group(0))
			continue
		}
		if r == '\\' && i+1 < len(rs) {
			i++
			c := rs[i]
			switch {
			case c >= '0' && c <= '9':
				write(group(int(c - '0')))
			case c == 'r' || c == 'n':
				b.WriteByte('\n')
			case c == 't':
				b.WriteByte('\t')
			case c == 'u':
				caseOne = 1
			case c == 'l':
				caseOne = -1
			case c == 'U':
				caseAll = 1
			case c == 'L':
				caseAll = -1
			case c == 'e' || c == 'E':
				caseAll = 0
			default:
				write(string(c))
			}
			continue
		}
		write(string(r))
	}
	return b.String()
}

// exChanges: the ex command edits the buffer (refused when read-only).
func exChanges(name string, rr []rune) bool {
	if rr[0] == 's' && (len(rr) == 1 || (!unicode.IsLetter(rr[1]) && !unicode.IsDigit(rr[1]) && rr[1] != ' ' && rr[1] != '!')) {
		return true
	}
	switch name {
	case "s", "substitute", "&", "&&", "d", "de", "del", "delete", ">", "<", "j", "join",
		"u", "un", "undo", "red", "redo", "sor", "sort":
		return true
	}
	return false
}
