package vim

import (
	"fmt"
	"strconv"
	"strings"
	"unicode"
)

type command struct {
	reg   rune
	count int // count1*count2; 0 = none given
	op    string
	name  string
	arg   string
}

const (
	pDone = iota
	pMore
	pBad
)

func readCount(keys []string, i int) (int, int) {
	n := 0
	for i < len(keys) {
		k := keys[i]
		if len(k) != 1 || k[0] < '0' || k[0] > '9' || (k == "0" && n == 0) {
			break
		}
		n = n*10 + int(k[0]-'0')
		if n > 99999 {
			n = 99999
		}
		i++
	}
	return n, i
}

var simpleMotions = map[string]bool{
	"h": true, "j": true, "k": true, "l": true, "w": true, "W": true, "b": true, "B": true, "e": true, "E": true,
	"0": true, "^": true, "$": true, "|": true, "G": true, "H": true, "M": true, "L": true, "n": true, "N": true,
	"*": true, "#": true, "%": true, "{": true, "}": true, ";": true, ",": true, "_": true, "+": true, "-": true,
	"enter": true, "backspace": true, " ": true, "left": true, "right": true, "up": true, "down": true,
	"home": true, "end": true, "ctrl+n": true, "ctrl+p": true, "ctrl+j": true, "ctrl+h": true,
}

var gMotions = map[string]bool{"g": true, "e": true, "E": true, "_": true, "j": true, "k": true, "0": true, "$": true}

// parseMotion reads a motion starting at keys[*i-1] (k). It does not
// consume anything when k does not start a motion.
func parseMotion(k string, keys []string, i *int) (name, arg string, st int, isMotion bool) {
	if simpleMotions[k] {
		return k, "", pDone, true
	}
	switch k {
	case "f", "F", "t", "T", "'", "`":
		if *i >= len(keys) {
			return "", "", pMore, true
		}
		a := keys[*i]
		*i++
		if a == "esc" {
			return "", "", pBad, true
		}
		return k, a, pDone, true
	case "g":
		if *i >= len(keys) {
			return "", "", pMore, true
		}
		if gMotions[keys[*i]] {
			n := "g" + keys[*i]
			*i++
			return n, "", pDone, true
		}
	}
	return "", "", pDone, false
}

var simpleCmds = map[string]bool{
	"i": true, "a": true, "I": true, "A": true, "o": true, "O": true, "s": true, "S": true, "x": true, "X": true,
	"D": true, "C": true, "Y": true, "p": true, "P": true, "J": true, "R": true, "u": true, "ctrl+r": true,
	".": true, "~": true, "v": true, "V": true, "ctrl+v": true, ":": true, "/": true, "?": true, "&": true,
	"ctrl+a": true, "ctrl+x": true, "ctrl+d": true, "ctrl+u": true, "ctrl+f": true, "ctrl+b": true,
	"ctrl+e": true, "ctrl+y": true, "K": true, "delete": true, "insert": true, "ctrl+g": true, "pgdown": true, "pgup": true,
}

func validReg(r string) (rune, bool) {
	rs := []rune(r)
	if len(rs) != 1 {
		return 0, false
	}
	c := rs[0]
	if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.ContainsRune(`"+*_-/:.`, c) {
		return c, true
	}
	return 0, false
}

func mulCount(a, b int) int {
	switch {
	case a == 0:
		return b
	case b == 0:
		return a
	}
	return a * b
}

// parseCmd parses a normal-mode command.
func parseCmd(keys []string, macroRec bool) (command, int) {
	var c command
	i := 0
	n := len(keys)
	if i < n && keys[i] == "\"" {
		if i+1 >= n {
			return c, pMore
		}
		r, ok := validReg(keys[i+1])
		if !ok {
			return c, pBad
		}
		c.reg = r
		i += 2
	}
	cnt1, j := readCount(keys, i)
	i = j
	if i >= n {
		return c, pMore
	}
	k := keys[i]
	i++
	op := ""
	switch k {
	case "d", "c", "y", ">", "<":
		op = k
	case "g":
		if i >= n {
			return c, pMore
		}
		switch keys[i] {
		case "~", "u", "U":
			op = "g" + keys[i]
			i++
		}
	}
	if op != "" {
		c.op = op
		cnt2, j := readCount(keys, i)
		i = j
		c.count = mulCount(cnt1, cnt2)
		if i >= n {
			return c, pMore
		}
		k = keys[i]
		i++
		last := op[len(op)-1:]
		if k == last {
			c.name = "line"
			return c, pDone
		}
		if len(op) == 2 && k == "g" {
			if i >= n {
				return c, pMore
			}
			if keys[i] == last {
				c.name = "line"
				return c, pDone
			}
		}
		if k == "i" || k == "a" {
			if i >= n {
				return c, pMore
			}
			c.name = k + keys[i]
			return c, pDone
		}
		if k == "/" || k == "?" {
			c.name = k
			return c, pDone
		}
		if k == "esc" {
			return c, pBad
		}
		name, arg, st, ok := parseMotion(k, keys, &i)
		if !ok {
			return c, pBad
		}
		if st != pDone {
			return c, st
		}
		c.name, c.arg = name, arg
		return c, pDone
	}
	c.count = cnt1
	name, arg, st, ok := parseMotion(k, keys, &i)
	if ok {
		if st != pDone {
			return c, st
		}
		c.name, c.arg = name, arg
		return c, pDone
	}
	if simpleCmds[k] {
		c.name = k
		return c, pDone
	}
	switch k {
	case "r", "m", "@":
		if i >= n {
			return c, pMore
		}
		c.name, c.arg = k, keys[i]
		if c.arg == "esc" {
			return c, pBad
		}
		return c, pDone
	case "q":
		if macroRec {
			c.name = "q"
			return c, pDone
		}
		if i >= n {
			return c, pMore
		}
		c.name, c.arg = "q", keys[i]
		return c, pDone
	case "z", "Z", "ctrl+w":
		if i >= n {
			return c, pMore
		}
		c.name, c.arg = k, keys[i]
		return c, pDone
	case "g":
		if i >= n {
			return c, pMore
		}
		switch keys[i] {
		case "J", "i", "v", "f", "x", "p", "P", "?":
			c.name = "g" + keys[i]
			return c, pDone
		}
		return c, pBad
	}
	return c, pBad
}

func isChange(c command) bool {
	if c.op != "" {
		return c.op != "y"
	}
	switch c.name {
	case "x", "X", "D", "C", "s", "S", "p", "P", "J", "gJ", "r", "~", "o", "O", "i", "a", "I", "A", "R",
		"ctrl+a", "ctrl+x", "&", "delete", "insert", "gp", "gP":
		return true
	}
	return false
}

// recordKeys rebuilds a command's keys without counts (dot repeat).
func recordKeys(c command) []string {
	var ks []string
	if c.reg != 0 && c.reg != '"' {
		ks = append(ks, "\"", string(c.reg))
	}
	if c.op != "" {
		ks = append(ks, splitOp(c.op)...)
		switch {
		case c.name == "line":
			ks = append(ks, splitOp(c.op)[len(splitOp(c.op))-1])
		default:
			ks = append(ks, splitName(c.name)...)
			if c.arg != "" {
				ks = append(ks, c.arg)
			}
		}
		return ks
	}
	ks = append(ks, splitName(c.name)...)
	if c.arg != "" {
		ks = append(ks, c.arg)
	}
	return ks
}

func splitOp(op string) []string {
	if len(op) == 2 {
		return []string{op[:1], op[1:]}
	}
	return []string{op}
}

func splitName(n string) []string {
	if len(n) == 2 && (n[0] == 'g' || n[0] == 'i' || n[0] == 'a') {
		return []string{n[:1], n[1:]}
	}
	return []string{n}
}

func (e *Engine) normalKey(key string) {
	if (key == "esc" || key == "ctrl+c") && len(e.pend) > 0 {
		e.pend = nil
		return
	}
	if key == "esc" {
		return
	}
	e.pend = append(e.pend, key)
	c, st := parseCmd(e.pend, e.macroReg != 0)
	switch st {
	case pMore:
		return
	case pBad:
		e.pend = nil
		return
	}
	e.pend = nil
	e.exec(c)
}

func (e *Engine) exec(c command) {
	if e.Opts.ReadOnly && (isChange(c) || c.name == "u" || c.name == "ctrl+r" || c.name == ".") {
		e.setErr(errReadOnly)
		return
	}
	if isChange(c) {
		e.lastChange = recordKeys(c)
		e.lastCount = c.count
	}
	if c.op != "" {
		e.execOperator(c)
		return
	}
	count := c.count
	if count < 1 {
		count = 1
	}
	if isMotionName(c.name) {
		e.moveBy(c)
		return
	}
	e.execSimple(c, count)
}

func isMotionName(n string) bool {
	switch n {
	case "f", "F", "t", "T", "'", "`":
		return true
	}
	return simpleMotions[n] || (len(n) == 2 && n[0] == 'g' && gMotions[n[1:]])
}

// moveBy runs a motion as a cursor move.
func (e *Engine) moveBy(c command) {
	t, kind, ok := e.motion(c.name, c.arg, c.count, false)
	if !ok {
		return
	}
	if isJump(c.name) {
		e.marks['\''] = e.cur
	}
	e.cur = t
	e.clampCursor(false)
	switch c.name {
	case "j", "k", "up", "down", "ctrl+n", "ctrl+p", "ctrl+j", "gj", "gk", "H", "M", "L":
	case "$", "end":
		e.want = -1
	default:
		e.want = e.dcol()
	}
	_ = kind
}

func isJump(n string) bool {
	switch n {
	case "G", "gg", "n", "N", "*", "#", "%", "{", "}", "'", "`", "H", "M", "L":
		return true
	}
	return false
}

func (e *Engine) execSimple(c command, count int) {
	l := e.line()
	switch c.name {
	case "i", "insert":
		e.checkpoint()
		e.startInsert("i", count)
	case "a":
		e.checkpoint()
		if len(l) > 0 {
			e.cur.Col = nextCluster(l, e.cur.Col)
		}
		e.startInsert("a", count)
	case "I":
		e.checkpoint()
		e.cur.Col = firstNonBlank(l)
		if isBlankLine(l) {
			e.cur.Col = len(l)
		}
		e.startInsert("I", count)
	case "A":
		e.checkpoint()
		e.cur.Col = len(l)
		e.startInsert("A", count)
	case "o", "O":
		e.checkpoint()
		e.openLine(c.name == "o")
		e.startInsert(c.name, count)
	case "gi":
		e.checkpoint()
		if p, ok := e.marks['^']; ok {
			e.cur = p
		}
		e.clampCursor(true)
		e.startInsert("i", count)
	case "x", "delete":
		if len(l) == 0 {
			return
		}
		e.execOperator(command{reg: c.reg, count: count, op: "d", name: "l"})
	case "X":
		if e.cur.Col == 0 {
			return
		}
		e.execOperator(command{reg: c.reg, count: count, op: "d", name: "h"})
	case "D":
		e.execOperator(command{reg: c.reg, count: count, op: "d", name: "$"})
	case "C":
		e.execOperator(command{reg: c.reg, count: count, op: "c", name: "$"})
	case "s":
		if len(l) == 0 {
			e.checkpoint()
			e.startInsert("i", 1)
			return
		}
		e.execOperator(command{reg: c.reg, count: count, op: "c", name: "l"})
	case "S":
		e.execOperator(command{reg: c.reg, count: count, op: "c", name: "line"})
	case "Y":
		e.execOperator(command{reg: c.reg, count: count, op: "y", name: "line"})
	case "p", "P", "gp", "gP":
		e.put(c.reg, c.name == "p" || c.name == "gp", count, c.name[0] == 'g')
	case "J", "gJ":
		e.join(e.cur.Line, max(2, count), c.name == "J")
	case "r":
		e.replaceChars(c.arg, count)
	case "R":
		e.checkpoint()
		e.mode = ModeReplace
		e.replOrig = nil
		e.insCmd, e.insCount, e.insKeys = "R", count, nil
		e.startRecordingInsert()
	case "u":
		n := 0
		for i := 0; i < count; i++ {
			if e.undoOnce() {
				n++
			}
		}
		if n == 0 {
			e.setMsg("Already at oldest change")
		} else {
			e.setMsg(fmt.Sprintf("%d change%s; before #%d", n, plural(n), len(e.undo)+1))
		}
	case "ctrl+r":
		n := 0
		for i := 0; i < count; i++ {
			if e.redoOnce() {
				n++
			}
		}
		if n == 0 {
			e.setMsg("Already at newest change")
		}
	case ".":
		e.repeatChange(c.count)
	case "~":
		e.checkpoint()
		for i := 0; i < count && e.cur.Col < len(e.line()); i++ {
			l := e.line()
			nx := nextCluster(l, e.cur.Col)
			for j := e.cur.Col; j < nx; j++ {
				l[j] = toggleCase(l[j])
			}
			e.gen++
			if nx >= len(l) {
				break
			}
			e.cur.Col = nx
		}
		e.want = e.dcol()
	case "v", "V", "ctrl+v":
		e.vStart = e.cur
		e.vToEOL = false
		e.mode = map[string]Mode{"v": ModeVisual, "V": ModeVisualLine, "ctrl+v": ModeVisualBlock}[c.name]
	case "gv":
		if e.lastVisual != nil {
			e.vStart, e.cur, e.mode, e.vToEOL = e.lastVisual.start, e.lastVisual.end, e.lastVisual.mode, e.lastVisual.toEOL
			e.clampCursor(false)
			e.vStart = clampPos(e.lines, e.vStart)
		}
	case ":":
		pre := ""
		if c.count > 1 {
			pre = fmt.Sprintf(".,.+%d", c.count-1)
		}
		e.openCmdline(':', pre)
	case "/", "?":
		e.openCmdline([]rune(c.name)[0], "")
	case "&":
		if e.lastSub.ok {
			e.substitute(e.cur.Line, e.cur.Line, e.lastSub.pat, e.lastSub.rep, "", false)
		}
	case "ctrl+a", "ctrl+x":
		d := count
		if c.name == "ctrl+x" {
			d = -count
		}
		e.increment(d)
	case "ctrl+d", "ctrl+u", "ctrl+f", "ctrl+b", "pgdown", "pgup":
		h := max(1, e.Height)
		step := h / 2
		if c.count > 0 && (c.name == "ctrl+d" || c.name == "ctrl+u") {
			step = c.count
		}
		if c.name == "ctrl+f" || c.name == "ctrl+b" || c.name == "pgdown" || c.name == "pgup" {
			step = max(1, h-2) * count
		}
		if c.name == "ctrl+u" || c.name == "ctrl+b" || c.name == "pgup" {
			step = -step
		}
		e.Top += step
		e.cur.Line += step
		e.clampCursor(false)
		e.cur.Col = firstNonBlank(e.line())
		if e.Top < 0 {
			e.Top = 0
		}
	case "ctrl+e":
		e.Top = min(e.Top+count, max(0, len(e.lines)-1))
		if e.cur.Line < e.Top {
			e.cur.Line = e.Top
			e.clampCursor(false)
		}
	case "ctrl+y":
		e.Top = max(0, e.Top-count)
		if e.cur.Line >= e.Top+e.Height {
			e.cur.Line = e.Top + e.Height - 1
			e.clampCursor(false)
		}
	case "z":
		switch c.arg {
		case "z", ".":
			e.Top = max(0, e.cur.Line-e.Height/2)
		case "t", "enter":
			e.Top = e.cur.Line
		case "b", "-":
			e.Top = max(0, e.cur.Line-e.Height+1)
		}
		if c.arg == "." || c.arg == "enter" || c.arg == "-" {
			e.cur.Col = firstNonBlank(e.line())
		}
	case "Z":
		switch c.arg {
		case "Z":
			e.emit(Effect{Kind: EffWriteQuit})
		case "Q":
			e.emit(Effect{Kind: EffQuit, Bang: true})
		}
	case "m":
		r := []rune(c.arg)
		if len(r) == 1 && (unicode.IsLetter(r[0]) || r[0] == '\'' || r[0] == '`') {
			e.marks[r[0]] = e.cur
		}
	case "q":
		if e.macroReg != 0 {
			keys := e.macroKeys
			if n := len(keys); n > 0 && keys[n-1] == "q" {
				keys = keys[:n-1]
			}
			e.store(e.macroReg, Register{Text: KeysToNotation(keys)}, 'm')
			e.macroReg, e.macroKeys = 0, nil
			return
		}
		r, ok := validReg(c.arg)
		if !ok || !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z') {
			return
		}
		e.macroReg = r
		e.macroKeys = nil
		if r >= 'A' && r <= 'Z' {
			if ks, err := ParseKeys(e.Register(r).Text, e.Opts.Leader); err == nil {
				e.macroKeys = ks
			}
		}
	case "@":
		reg := []rune(c.arg)[0]
		if reg == '@' {
			reg = e.lastMacro
		}
		if reg == ':' {
			if n := len(e.cmdHist); n > 0 {
				for i := 0; i < count; i++ {
					e.execEx(e.cmdHist[n-1])
				}
			}
			return
		}
		e.lastMacro = reg
		ks, err := ParseKeys(e.Register(reg).Text, e.Opts.Leader)
		if err != nil || e.depth > 20 {
			return
		}
		e.depth++
		for i := 0; i < count; i++ {
			for _, k := range ks {
				e.runKey(k)
			}
		}
		e.depth--
	case "K":
		e.emit(Effect{Kind: EffHover})
	case "gf":
		e.emit(Effect{Kind: EffFollowLink})
	case "gx":
		e.emit(Effect{Kind: EffOpenURL})
	case "ctrl+w":
		e.emit(Effect{Kind: EffWindow, Arg: c.arg})
	case "ctrl+g":
		e.setMsg(fmt.Sprintf("line %d of %d --%d%%--", e.cur.Line+1, len(e.lines), (e.cur.Line+1)*100/len(e.lines)))
	}
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

func clampPos(ls [][]rune, p Pos) Pos {
	if p.Line >= len(ls) {
		p.Line = len(ls) - 1
	}
	if p.Line < 0 {
		p.Line = 0
	}
	if p.Col > len(ls[p.Line]) {
		p.Col = len(ls[p.Line])
	}
	return p
}

func toggleCase(r rune) rune {
	if unicode.IsUpper(r) {
		return unicode.ToLower(r)
	}
	return unicode.ToUpper(r)
}

func (e *Engine) repeatChange(count int) {
	if len(e.lastChange) == 0 {
		return
	}
	keys := append([]string(nil), e.lastChange...)
	if count == 0 {
		count = e.lastCount
	}
	var full []string
	i := 0
	if len(keys) >= 2 && keys[0] == "\"" {
		full = append(full, keys[0], keys[1])
		i = 2
	}
	if count > 0 {
		for _, d := range strconv.Itoa(count) {
			full = append(full, string(d))
		}
	}
	full = append(full, keys[i:]...)
	saved := e.replaying
	e.replaying = true
	for _, k := range full {
		e.runKey(k)
	}
	// A replayed insert that was not closed (it always is) ends here.
	if e.mode == ModeInsert || e.mode == ModeReplace {
		e.leaveInsert()
	}
	e.replaying = saved
}

func (e *Engine) openLine(below bool) {
	ind := leadingWS(e.line())
	at := e.cur.Line
	if below {
		at++
	}
	e.lines = append(e.lines[:at], append([][]rune{ind}, e.lines[at:]...)...)
	e.cur = Pos{at, len(ind)}
	e.gen++
}

func (e *Engine) join(from, n int, spaces bool) {
	if from+1 >= len(e.lines) {
		return
	}
	e.checkpoint()
	to := min(len(e.lines)-1, from+n-1)
	cur := e.lines[from]
	col := 0
	for l := from + 1; l <= to; l++ {
		next := e.lines[l]
		if spaces {
			trim := []rune(strings.TrimLeft(string(next), " \t"))
			cur = []rune(strings.TrimRight(string(cur), " \t"))
			col = len(cur)
			if len(trim) > 0 && len(cur) > 0 && trim[0] != ')' {
				cur = append(cur, ' ')
			}
			cur = append(cur, trim...)
		} else {
			col = len(cur)
			cur = append(cur, next...)
		}
	}
	e.lines[from] = cur
	e.lines = append(e.lines[:from+1], e.lines[to+1:]...)
	e.cur = Pos{from, col}
	e.gen++
	e.clampCursor(false)
}

func (e *Engine) replaceChars(arg string, count int) {
	l := e.line()
	if len(l) == 0 {
		return
	}
	// count clusters must exist
	end := e.cur.Col
	for i := 0; i < count; i++ {
		if end >= len(l) {
			return
		}
		end = nextCluster(l, end)
	}
	e.checkpoint()
	if arg == "enter" {
		rest := append([]rune(nil), l[end:]...)
		e.lines[e.cur.Line] = append([]rune(nil), l[:e.cur.Col]...)
		e.lines = append(e.lines[:e.cur.Line+1], append([][]rune{rest}, e.lines[e.cur.Line+1:]...)...)
		e.cur = Pos{e.cur.Line + 1, 0}
		e.gen++
		return
	}
	rs := []rune(arg)
	if arg == "tab" {
		rs = []rune{'\t'}
	}
	if len(rs) != 1 {
		return
	}
	var repl []rune
	for i := 0; i < count; i++ {
		repl = append(repl, rs[0])
	}
	nl := append(append(append([]rune(nil), l[:e.cur.Col]...), repl...), l[end:]...)
	e.lines[e.cur.Line] = nl
	e.cur.Col += len(repl) - 1
	e.gen++
}

// increment adds d to the number at or after the cursor (Ctrl-a/x).
func (e *Engine) increment(d int) {
	l := e.line()
	i := e.cur.Col
	// find the digit run at/after the cursor
	for i < len(l) && !unicode.IsDigit(l[i]) {
		i++
	}
	if i >= len(l) {
		return
	}
	// back up to the start of the number under the cursor
	for i > 0 && unicode.IsDigit(l[i-1]) {
		i--
	}
	j := i
	for j < len(l) && l[j] >= '0' && l[j] <= '9' {
		j++
	}
	start := i
	if start > 0 && l[start-1] == '-' {
		start--
	}
	n, err := strconv.Atoi(string(l[start:j]))
	if err != nil {
		return
	}
	e.checkpoint()
	s := []rune(strconv.Itoa(n + d))
	e.lines[e.cur.Line] = append(append(append([]rune(nil), l[:start]...), s...), l[j:]...)
	e.cur.Col = start + len(s) - 1
	e.gen++
}

// put pastes a register (p/P); gp/gP leave the cursor after the text.
func (e *Engine) put(reg rune, after bool, count int, cursorAfter bool) {
	r := e.fetch(reg)
	if r.Text == "" && !r.Linewise {
		e.setErr("E353: Nothing in register " + regName(reg))
		return
	}
	e.checkpoint()
	e.gen++
	switch {
	case r.Linewise:
		var ins [][]rune
		for i := 0; i < count; i++ {
			ins = append(ins, splitLines(r.Text)...)
		}
		at := e.cur.Line
		if after {
			at++
		}
		e.lines = append(e.lines[:at], append(ins, e.lines[at:]...)...)
		if cursorAfter {
			e.cur = Pos{min(at+len(ins), len(e.lines)-1), 0}
		} else {
			e.cur = Pos{at, firstNonBlank(e.lines[at])}
		}
	case r.Blockwise:
		rows := strings.Split(r.Text, "\n")
		col := e.cur.Col
		if after && len(e.line()) > 0 {
			col = nextCluster(e.line(), col)
		}
		dc := DisplayCol(e.line(), col, e.Opts.TabStop)
		width := 0
		for _, row := range rows {
			width = max(width, displayWidth([]rune(row), e.Opts.TabStop))
		}
		for i, row := range rows {
			ln := e.cur.Line + i
			if ln >= len(e.lines) {
				e.lines = append(e.lines, []rune{})
			}
			l := e.lines[ln]
			for displayWidth(l, e.Opts.TabStop) < dc {
				l = append(l, ' ')
			}
			c := ColForDisplay(l, dc, e.Opts.TabStop, true)
			piece := []rune(strings.Repeat(row, count))
			if c < len(l) {
				for displayWidth(piece, e.Opts.TabStop) < width*count {
					piece = append(piece, ' ')
				}
			}
			e.lines[ln] = append(append(append([]rune(nil), l[:c]...), piece...), l[c:]...)
		}
		e.cur.Col = col
	default:
		text := strings.Repeat(r.Text, count)
		pos := e.cur
		if after && len(e.line()) > 0 {
			pos.Col = nextCluster(e.line(), pos.Col)
		}
		end := e.insertText(pos, text)
		switch {
		case cursorAfter:
			e.cur = end
		case strings.Contains(text, "\n"):
			e.cur = pos
		default:
			e.cur = Pos{end.Line, prevCluster(e.lines[end.Line], end.Col)}
		}
	}
	e.clampCursor(cursorAfter)
	e.want = e.dcol()
}

func displayWidth(l []rune, ts int) int { return DisplayCol(l, len(l), ts) }

func regName(r rune) string {
	if r == 0 {
		return `"`
	}
	return string(r)
}

// insertText inserts text at p and returns the position just after it.
func (e *Engine) insertText(p Pos, text string) Pos {
	parts := splitLines(text)
	l := e.lines[p.Line]
	head := append([]rune(nil), l[:p.Col]...)
	tail := append([]rune(nil), l[p.Col:]...)
	if len(parts) == 1 {
		e.lines[p.Line] = append(append(head, parts[0]...), tail...)
		return Pos{p.Line, p.Col + len(parts[0])}
	}
	first := append(head, parts[0]...)
	last := parts[len(parts)-1]
	endCol := len(last)
	lastLine := append(append([]rune(nil), last...), tail...)
	mid := append([][]rune{first}, parts[1:len(parts)-1]...)
	mid = append(mid, lastLine)
	e.lines = append(e.lines[:p.Line], append(mid, e.lines[p.Line+1:]...)...)
	return Pos{p.Line + len(parts) - 1, endCol}
}

// textRange returns the text in [s, en) (flat coordinates).
func (e *Engine) textRange(s, en Pos) string {
	if s.Line == en.Line {
		return string(e.lines[s.Line][s.Col:en.Col])
	}
	var b strings.Builder
	b.WriteString(string(e.lines[s.Line][s.Col:]))
	for l := s.Line + 1; l < en.Line; l++ {
		b.WriteByte('\n')
		b.WriteString(string(e.lines[l]))
	}
	b.WriteByte('\n')
	if en.Line < len(e.lines) {
		b.WriteString(string(e.lines[en.Line][:en.Col]))
	}
	return b.String()
}

func (e *Engine) deleteRange(s, en Pos) {
	if en.Line >= len(e.lines) {
		en = Pos{len(e.lines) - 1, len(e.lines[len(e.lines)-1])}
	}
	head := append([]rune(nil), e.lines[s.Line][:s.Col]...)
	merged := append(head, e.lines[en.Line][en.Col:]...)
	e.lines = append(e.lines[:s.Line], append([][]rune{merged}, e.lines[en.Line+1:]...)...)
	e.gen++
}
