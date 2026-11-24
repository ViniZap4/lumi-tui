package vim

import (
	"regexp"
	"strings"
	"time"
)

// Mode is the engine mode.
type Mode int

const (
	ModeNormal Mode = iota
	ModeInsert
	ModeReplace
	ModeVisual
	ModeVisualLine
	ModeVisualBlock
	ModeCmdline // ':' ex line or '/' '?' search prompt
)

// IsVisual reports the three visual modes.
func (m Mode) IsVisual() bool {
	return m == ModeVisual || m == ModeVisualLine || m == ModeVisualBlock
}

// String is the mode label shown in the status line.
func (m Mode) String() string {
	switch m {
	case ModeInsert:
		return "INSERT"
	case ModeReplace:
		return "REPLACE"
	case ModeVisual:
		return "VISUAL"
	case ModeVisualLine:
		return "V-LINE"
	case ModeVisualBlock:
		return "V-BLOCK"
	case ModeCmdline:
		return "COMMAND"
	}
	return "NORMAL"
}

// CursorShape is the terminal cursor shape for a mode.
type CursorShape int

const (
	ShapeBlock CursorShape = iota
	ShapeBar
	ShapeUnderline
)

// DECSCUSR returns the escape sequence that sets a steady cursor shape.
func (s CursorShape) DECSCUSR() string {
	switch s {
	case ShapeBar:
		return "\x1b[6 q"
	case ShapeUnderline:
		return "\x1b[4 q"
	}
	return "\x1b[2 q"
}

// ResetCursorShape restores the terminal's default cursor.
const ResetCursorShape = "\x1b[0 q"

// EffectKind is something the host must do (the engine never touches
// files, windows or the network).
type EffectKind int

const (
	EffWrite        EffectKind = iota + 1 // :w
	EffQuit                               // :q (Bang: :q!)
	EffWriteQuit                          // :wq, :x, ZZ
	EffEditExternal                       // :edit! — open in $EDITOR
	EffEx                                 // an ex command the engine does not know (Name, Args)
	EffWindow                             // Ctrl-w <Arg>
	EffFollowLink                         // gf / Enter on a link (host decides)
	EffHover                              // K
	EffOpenURL                            // gx
	EffHostMap                            // a host key binding (Arg = action name)
)

// Effect is a request to the host.
type Effect struct {
	Kind EffectKind
	Name string // ex command name (EffEx)
	Args string
	Arg  string
	Bang bool
}

// Map is one key mapping.
type Map struct {
	Mode    string // "normal", "insert", "visual", "all"
	LHS     []string
	RHS     []string
	Noremap bool
}

// Options configures an engine.
type Options struct {
	TabStop        int
	ShiftWidth     int
	ExpandTab      bool
	Number         bool
	RelativeNumber bool
	Wrap           bool
	// EscapeSeqs are insert-mode sequences that act as <Esc> ("jj", "jk").
	EscapeSeqs    []string
	EscapeTimeout time.Duration
	// ClipboardUnnamed mirrors unnamed yanks/deletes to the system
	// clipboard (`set clipboard=unnamedplus`).
	ClipboardUnnamed bool
	Maps             []Map
	// HostMaps are built-in normal-mode bindings the host handles (e.g.
	// "<leader>e" → "explorer"). User maps win.
	HostMaps map[string]string
	// ExAliases come from `map :W :write`.
	ExAliases map[string]string
	Leader    string
}

// DefaultOptions are the engine defaults (web: tab 4, wrap on).
func DefaultOptions() Options {
	return Options{TabStop: 4, ShiftWidth: 4, ExpandTab: true, Wrap: true, EscapeTimeout: 300 * time.Millisecond,
		ClipboardUnnamed: true, Leader: `\`}
}

// Register holds yanked/deleted text.
type Register struct {
	Text      string
	Linewise  bool
	Blockwise bool
}

// Clipboard is the system clipboard (injected; tests use MemClipboard).
type Clipboard interface {
	Read() (string, error)
	Write(string) error
}

// MemClipboard is an in-memory clipboard.
type MemClipboard struct{ Text string }

func (c *MemClipboard) Read() (string, error) { return c.Text, nil }
func (c *MemClipboard) Write(s string) error  { c.Text = s; return nil }

type snapshot struct {
	lines [][]rune
	cur   Pos
}

type visualSel struct {
	start, end Pos
	mode       Mode
	toEOL      bool
}

// Engine is one buffer plus its vim state. Not safe for concurrent use.
type Engine struct {
	lines [][]rune
	cur   Pos
	want  int // desired display column; -1 = end of line
	mode  Mode
	Opts  Options

	regs    map[rune]Register
	Clip    Clipboard
	undo    []snapshot
	redo    []snapshot
	gen     int
	pend    []string
	mapPend []string
	depth   int

	vStart     Pos
	vToEOL     bool
	lastVisual *visualSel
	marks      map[rune]Pos

	searchPat string
	searchFwd bool
	hlsearch  bool
	lastFind  struct {
		cmd string
		ch  string
	}
	lastSub struct {
		pat, rep, flags string
		ok              bool
	}

	cmdKind   rune
	cmdline   []rune
	cmdHist   []string
	cmdOp     *command // operator waiting for a search motion
	cmdFromVi bool
	viReturn  Mode
	cmdPrev   Mode
	histIdx   int

	msg    string
	errMsg bool

	// dot repeat
	recKeys    []string
	recording  bool
	lastChange []string
	lastCount  int
	replaying  bool

	// insert session
	insCmd    string
	insCount  int
	insKeys   []string
	insStart  Pos
	lastIns   string
	escBuf    []string
	escAt     time.Time
	replOrig  []rune
	replStart int
	block     *blockInsert

	// macros
	macroReg  rune
	macroKeys []string
	lastMacro rune

	// view (the host sets Height; the engine keeps Top so the cursor
	// stays visible).
	Top    int
	Height int

	flash   *visualSel
	flashAt time.Time

	effects []Effect
	Now     func() time.Time

	hlPat string
	hlRe  *regexp.Regexp
}

type blockInsert struct {
	l1, l2 int
	dcol   int
	toEOL  bool
	line   int
	col    int // rune col where typing started on the first line
}

// New builds an engine over text.
func New(text string, opts Options) *Engine {
	if opts.TabStop < 1 {
		opts.TabStop = 4
	}
	if opts.ShiftWidth < 1 {
		opts.ShiftWidth = opts.TabStop
	}
	if opts.EscapeTimeout <= 0 {
		opts.EscapeTimeout = 300 * time.Millisecond
	}
	if opts.Leader == "" {
		opts.Leader = `\`
	}
	return &Engine{lines: splitLines(text), Opts: opts, regs: map[rune]Register{}, marks: map[rune]Pos{},
		Clip: &MemClipboard{}, Now: time.Now, Height: 20, searchFwd: true}
}

// Text is the buffer content.
func (e *Engine) Text() string { return joinLines(e.lines) }

// Lines is the buffer (read-only view; do not modify).
func (e *Engine) Lines() [][]rune { return e.lines }

// LineCount is the number of lines.
func (e *Engine) LineCount() int { return len(e.lines) }

// Cursor is the cursor position.
func (e *Engine) Cursor() Pos { return e.cur }

// Mode is the current mode.
func (e *Engine) Mode() Mode { return e.mode }

// Gen increases on every buffer change.
func (e *Engine) Gen() int { return e.gen }

// Message is the last status message ("3 lines yanked", an error).
func (e *Engine) Message() (string, bool) { return e.msg, e.errMsg }

// ClearMessage drops the status message.
func (e *Engine) ClearMessage() { e.msg, e.errMsg = "", false }

// Pending is the showcmd text (typed but incomplete command).
func (e *Engine) Pending() string {
	return KeysToNotation(append(append([]string(nil), e.mapPend...), e.pend...))
}

// Recording reports the macro register being recorded (0 when idle).
func (e *Engine) Recording() rune { return e.macroReg }

// Cmdline returns the prompt kind (':', '/', '?') and its text.
func (e *Engine) Cmdline() (rune, string) { return e.cmdKind, string(e.cmdline) }

// Shape is the cursor shape for the current mode; normal is the shape
// used in normal/visual modes (user setting).
func (e *Engine) Shape(normal CursorShape) CursorShape {
	switch e.mode {
	case ModeInsert:
		return ShapeBar
	case ModeReplace:
		return ShapeUnderline
	case ModeCmdline:
		return ShapeBar
	}
	if len(e.pend) > 0 && isOperator(e.pend) {
		return ShapeUnderline
	}
	return normal
}

func isOperator(p []string) bool {
	for _, k := range p {
		switch k {
		case "d", "c", "y", ">", "<", "~", "u", "U":
			return true
		}
	}
	return false
}

// Register returns a register's content (clipboard registers read the
// clipboard).
func (e *Engine) Register(r rune) Register {
	switch r {
	case '+', '*':
		if s, err := e.Clip.Read(); err == nil {
			return fromClip(s)
		}
		return Register{}
	case '/':
		return Register{Text: e.searchPat}
	case ':':
		if n := len(e.cmdHist); n > 0 {
			return Register{Text: e.cmdHist[n-1]}
		}
		return Register{}
	case '.':
		return Register{Text: e.lastIns}
	}
	if r >= 'A' && r <= 'Z' {
		r = r - 'A' + 'a'
	}
	return e.regs[r]
}

// SetText replaces the buffer (an external reload). Undo history is
// kept as one change so `u` brings the old text back.
func (e *Engine) SetText(text string) {
	if text == e.Text() {
		return
	}
	e.checkpoint()
	e.lines = splitLines(text)
	e.gen++
	e.clampCursor(false)
}

// SetCursor moves the cursor (clamped).
func (e *Engine) SetCursor(p Pos) {
	e.cur = p
	e.clampCursor(e.mode == ModeInsert)
	e.want = e.dcol()
}

// SearchPattern is the active search (for highlighting) and whether
// hlsearch is on.
func (e *Engine) SearchPattern() (string, bool) {
	if e.mode == ModeCmdline && (e.cmdKind == '/' || e.cmdKind == '?') && len(e.cmdline) > 0 {
		return string(e.cmdline), true
	}
	return e.searchPat, e.hlsearch && e.searchPat != ""
}

// Selection returns the visual selection (ok=false outside visual mode).
// For blockwise selections, start/end are corners and toEOL marks `$`.
func (e *Engine) Selection() (start, end Pos, mode Mode, toEOL, ok bool) {
	if !e.mode.IsVisual() {
		return Pos{}, Pos{}, 0, false, false
	}
	s, en := order(e.vStart, e.cur)
	return s, en, e.mode, e.vToEOL, true
}

// Flash returns the yank highlight while it is fresh (d is its lifetime).
func (e *Engine) Flash(d time.Duration) (start, end Pos, mode Mode, ok bool) {
	if e.flash == nil || e.Now().Sub(e.flashAt) > d {
		return Pos{}, Pos{}, 0, false
	}
	return e.flash.start, e.flash.end, e.flash.mode, true
}

// MatchesInLine returns [start,end) rune ranges of the search pattern on
// line l (for highlighting).
func (e *Engine) MatchesInLine(l int) [][2]int {
	pat, on := e.SearchPattern()
	if !on || l < 0 || l >= len(e.lines) {
		return nil
	}
	if e.hlRe == nil || e.hlPat != pat {
		re, err := compilePattern(pat)
		if err != nil {
			return nil
		}
		e.hlRe, e.hlPat = re, pat
	}
	return runeMatches(e.hlRe, e.lines[l])
}

// Feed processes one key and returns the host effects it produced.
func (e *Engine) Feed(key string) []Effect {
	e.effects = nil
	e.feedMapped(key)
	e.ensureVisible()
	out := e.effects
	e.effects = nil
	return out
}

// FeedKeys feeds keys in vim notation (tests, mappings).
func (e *Engine) FeedKeys(notation string) []Effect {
	ks, err := ParseKeys(notation, e.Opts.Leader)
	if err != nil {
		e.setErr(err.Error())
		return nil
	}
	var out []Effect
	for _, k := range ks {
		out = append(out, e.Feed(k)...)
	}
	return out
}

// FlushPending resolves an ambiguous mapping prefix as typed keys (the
// host calls it after a timeout).
func (e *Engine) FlushPending() []Effect {
	if len(e.mapPend) == 0 {
		return nil
	}
	e.effects = nil
	keys := e.mapPend
	e.mapPend = nil
	e.runExact(keys)
	out := e.effects
	e.effects = nil
	return out
}

func (e *Engine) emit(ef Effect) { e.effects = append(e.effects, ef) }

func (e *Engine) setMsg(s string) { e.msg, e.errMsg = s, false }
func (e *Engine) setErr(s string) { e.msg, e.errMsg = s, true }

// --- mapping layer ---------------------------------------------------

func (e *Engine) mapMode() string {
	switch {
	case e.mode == ModeInsert || e.mode == ModeReplace:
		return "insert"
	case e.mode.IsVisual():
		return "visual"
	case e.mode == ModeCmdline:
		return "cmdline"
	}
	return "normal"
}

type mapHit struct {
	rhs     []string
	noremap bool
	host    string
}

// lookup returns the exact mapping for keys and whether a longer one
// starts with them.
func (e *Engine) lookup(keys []string) (exact *mapHit, prefix bool) {
	mode := e.mapMode()
	if mode == "cmdline" {
		return nil, false
	}
	// Mappings only apply at the start of a command in normal mode
	// (not to the char argument of f/r/m, not mid-operator for host keys).
	match := func(lhs []string) (bool, bool) {
		if len(lhs) < len(keys) {
			return false, false
		}
		for i := range keys {
			if lhs[i] != keys[i] {
				return false, false
			}
		}
		return len(lhs) == len(keys), len(lhs) > len(keys)
	}
	for i := len(e.Opts.Maps) - 1; i >= 0; i-- {
		m := e.Opts.Maps[i]
		if m.Mode != mode && !(m.Mode == "all" && mode != "insert") {
			continue
		}
		ex, pre := match(m.LHS)
		if ex && exact == nil {
			exact = &mapHit{rhs: m.RHS, noremap: m.Noremap}
		}
		if pre {
			prefix = true
		}
	}
	if mode == "normal" && exact == nil {
		for lhs, action := range e.Opts.HostMaps {
			ks, err := ParseKeys(lhs, e.Opts.Leader)
			if err != nil {
				continue
			}
			ex, pre := match(ks)
			if ex {
				exact = &mapHit{host: action}
			}
			if pre {
				prefix = true
			}
		}
	}
	return exact, prefix
}

func (e *Engine) mappingsActive() bool {
	if e.mode == ModeCmdline || e.replaying {
		return false
	}
	if e.mode == ModeNormal || e.mode.IsVisual() {
		// Only at a command boundary or after a register/count prefix.
		return len(e.pend) == 0
	}
	return true
}

func (e *Engine) feedMapped(key string) {
	if !e.mappingsActive() {
		e.runKey(key)
		return
	}
	if e.mode == ModeInsert || e.mode == ModeReplace {
		e.runKey(key)
		return
	}
	keys := append(append([]string(nil), e.mapPend...), key)
	exact, prefix := e.lookup(keys)
	switch {
	case prefix:
		e.mapPend = keys
	case exact != nil:
		e.mapPend = nil
		e.applyMap(exact)
	default:
		e.mapPend = nil
		e.runExact(keys)
	}
}

// runExact runs keys that matched no mapping: the first key goes
// straight to the engine, the rest go through the mapping layer again.
func (e *Engine) runExact(keys []string) {
	if len(keys) == 0 {
		return
	}
	if len(keys) > 1 {
		if ex, _ := e.lookup(keys); ex != nil {
			e.applyMap(ex)
			return
		}
	}
	e.runKey(keys[0])
	for _, k := range keys[1:] {
		e.feedMapped(k)
	}
}

func (e *Engine) applyMap(h *mapHit) {
	if h.host != "" {
		e.emit(Effect{Kind: EffHostMap, Arg: h.host})
		return
	}
	if e.depth > 20 {
		e.setErr("E223: recursive mapping")
		return
	}
	e.depth++
	defer func() { e.depth-- }()
	for _, k := range h.rhs {
		if h.noremap {
			e.runKey(k)
		} else {
			e.feedMapped(k)
		}
	}
}

// runKey dispatches one key by mode (no mappings).
func (e *Engine) runKey(key string) {
	if e.macroReg != 0 && !e.replaying {
		e.macroKeys = append(e.macroKeys, key)
	}
	if e.recording {
		e.recKeys = append(e.recKeys, key)
	}
	switch e.mode {
	case ModeInsert, ModeReplace:
		e.insertKey(key)
	case ModeCmdline:
		e.cmdlineKey(key)
	case ModeVisual, ModeVisualLine, ModeVisualBlock:
		e.visualKey(key)
	default:
		e.normalKey(key)
	}
}

// --- undo ------------------------------------------------------------

func (e *Engine) checkpoint() {
	e.undo = append(e.undo, snapshot{lines: cloneLines(e.lines), cur: e.cur})
	if len(e.undo) > 1000 {
		e.undo = e.undo[1:]
	}
	e.redo = nil
}

func sameLines(a, b [][]rune) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if string(a[i]) != string(b[i]) {
			return false
		}
	}
	return true
}

func (e *Engine) undoOnce() bool {
	for len(e.undo) > 0 {
		s := e.undo[len(e.undo)-1]
		e.undo = e.undo[:len(e.undo)-1]
		if sameLines(s.lines, e.lines) {
			continue
		}
		e.redo = append(e.redo, snapshot{lines: cloneLines(e.lines), cur: e.cur})
		e.lines = s.lines
		e.cur = s.cur
		e.gen++
		e.clampCursor(false)
		return true
	}
	return false
}

func (e *Engine) redoOnce() bool {
	if len(e.redo) == 0 {
		return false
	}
	s := e.redo[len(e.redo)-1]
	e.redo = e.redo[:len(e.redo)-1]
	e.undo = append(e.undo, snapshot{lines: cloneLines(e.lines), cur: e.cur})
	e.lines = s.lines
	e.cur = s.cur
	e.gen++
	e.clampCursor(false)
	return true
}

// --- registers -------------------------------------------------------

// store saves text into reg (0 = unnamed). kind: 'y' yank, 'd' delete.
func (e *Engine) store(reg rune, r Register, kind byte) {
	if reg == '_' {
		return
	}
	switch {
	case reg == '+' || reg == '*':
		_ = e.Clip.Write(clipText(r))
	case reg >= 'a' && reg <= 'z':
		e.regs[reg] = r
	case reg >= 'A' && reg <= 'Z':
		lr := reg - 'A' + 'a'
		old := e.regs[lr]
		if old.Linewise && !r.Linewise {
			r.Text = old.Text + r.Text
			r.Linewise = true
		} else if r.Linewise && old.Text != "" {
			r.Text = old.Text + "\n" + r.Text
		} else {
			r.Text = old.Text + r.Text
		}
		e.regs[lr] = r
		reg = lr
	case reg == 0 || reg == '"':
		if kind == 'y' {
			e.regs['0'] = r
		} else if r.Linewise || strings.Contains(r.Text, "\n") {
			for i := '9'; i > '1'; i-- {
				e.regs[i] = e.regs[i-1]
			}
			e.regs['1'] = r
		} else {
			e.regs['-'] = r
		}
		if e.Opts.ClipboardUnnamed {
			_ = e.Clip.Write(clipText(r))
		}
	}
	e.regs['"'] = r
}

func (e *Engine) fetch(reg rune) Register {
	if reg == 0 || reg == '"' {
		if e.Opts.ClipboardUnnamed {
			if s, err := e.Clip.Read(); err == nil && s != "" && s != clipText(e.regs['"']) {
				return fromClip(s)
			}
		}
		return e.regs['"']
	}
	return e.Register(reg)
}

// --- cursor ----------------------------------------------------------

func (e *Engine) line() []rune { return e.lines[e.cur.Line] }

func (e *Engine) dcol() int { return DisplayCol(e.line(), e.cur.Col, e.Opts.TabStop) }

// clampCursor keeps the cursor inside the buffer; insert allows the
// position just past the end of the line.
func (e *Engine) clampCursor(past bool) {
	if len(e.lines) == 0 {
		e.lines = [][]rune{{}}
	}
	if e.cur.Line < 0 {
		e.cur.Line = 0
	}
	if e.cur.Line >= len(e.lines) {
		e.cur.Line = len(e.lines) - 1
	}
	l := e.line()
	if e.cur.Col < 0 {
		e.cur.Col = 0
	}
	if past {
		if e.cur.Col > len(l) {
			e.cur.Col = len(l)
		}
		if e.cur.Col < len(l) {
			e.cur.Col = clusterStart(l, e.cur.Col)
		}
		return
	}
	if e.cur.Col >= len(l) {
		e.cur.Col = lastCluster(l)
	}
	e.cur.Col = clusterStart(l, e.cur.Col)
}

func (e *Engine) ensureVisible() {
	h := e.Height
	if h < 1 {
		h = 1
	}
	so := 2
	if h < 7 {
		so = 0
	}
	if e.cur.Line < e.Top+so {
		e.Top = e.cur.Line - so
	}
	if e.cur.Line >= e.Top+h-so {
		e.Top = e.cur.Line - h + so + 1
	}
	if m := len(e.lines) - h; e.Top > m {
		e.Top = m
	}
	if e.Top < 0 {
		e.Top = 0
	}
}

// clipText is a register as clipboard text (linewise ends in a newline).
func clipText(r Register) string {
	if r.Linewise {
		return r.Text + "\n"
	}
	return r.Text
}

func fromClip(s string) Register {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	if strings.HasSuffix(s, "\n") {
		return Register{Text: strings.TrimSuffix(s, "\n"), Linewise: true}
	}
	return Register{Text: s}
}
