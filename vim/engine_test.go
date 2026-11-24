package vim

import (
	"strings"
	"testing"
	"time"
)

// buf builds an engine from text with a `|` marking the cursor.
func buf(t *testing.T, s string) *Engine {
	t.Helper()
	i := strings.Index(s, "|")
	if i < 0 {
		t.Fatalf("no cursor in %q", s)
	}
	text := s[:i] + s[i+1:]
	e := New(text, DefaultOptions())
	e.Clip = &MemClipboard{}
	before := s[:i]
	line := strings.Count(before, "\n")
	col := len([]rune(before[strings.LastIndex(before, "\n")+1:]))
	e.SetCursor(Pos{line, col})
	return e
}

// show renders the buffer with the cursor marked.
func show(e *Engine) string {
	var b strings.Builder
	for i, l := range e.lines {
		if i > 0 {
			b.WriteByte('\n')
		}
		if i == e.cur.Line {
			c := min(e.cur.Col, len(l))
			b.WriteString(string(l[:c]) + "|" + string(l[c:]))
		} else {
			b.WriteString(string(l))
		}
	}
	return b.String()
}

type vcase struct {
	name, in, keys, want string
}

func runCases(t *testing.T, cases []vcase) {
	t.Helper()
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := buf(t, c.in)
			e.FeedKeys(c.keys)
			if got := show(e); got != c.want {
				t.Fatalf("keys %q\n in: %q\ngot: %q\nwant %q (mode %v, msg %q)", c.keys, c.in, got, c.want, e.mode, e.msg)
			}
		})
	}
}

func TestMotions(t *testing.T) {
	runCases(t, []vcase{
		{"h l", "a|bc", "lh", "a|bc"},
		{"l clamps", "ab|c", "l", "ab|c"},
		{"count l", "|abcdef", "3l", "abc|def"},
		{"w", "|foo bar.baz", "w", "foo |bar.baz"},
		{"w punct", "foo |bar.baz", "w", "foo bar|.baz"},
		{"W", "|foo bar.baz qux", "2W", "foo bar.baz |qux"},
		{"w crosses lines", "foo |bar\nbaz", "w", "foo bar\n|baz"},
		{"w stops on empty line", "|foo\n\nbar", "w", "foo\n|\nbar"},
		{"b", "foo bar |baz", "b", "foo |bar baz"},
		{"b across lines", "foo\n|bar", "b", "|foo\nbar"},
		{"e", "|foo bar", "e", "fo|o bar"},
		{"e next", "fo|o bar", "e", "foo ba|r"},
		{"ge", "foo b|ar", "ge", "fo|o bar"},
		{"0 ^ $", "  ab|cd", "0", "|  abcd"},
		{"^", "  ab|cd", "^", "  |abcd"},
		{"$", "|abcd", "$", "abc|d"},
		{"gg G", "a\nb\n|c", "gg", "|a\nb\nc"},
		{"G", "|a\nb\nc", "G", "a\nb\n|c"},
		{"3G", "|a\nb\nc\nd", "3G", "a\nb\n|c\nd"},
		{"j keeps column", "ab|cd\nx\nefgh", "jj", "abcd\nx\nef|gh"},
		{"j with $", "a|b\nabcdef", "$j", "ab\nabcde|f"},
		{"f", "|a,b,c", "f,", "a|,b,c"},
		{"2f", "|a,b,c", "2f,", "a,b|,c"},
		{"t", "|a,b,c", "t,", "|a,b,c"},
		{"t moves", "|ab,c", "t,", "a|b,c"},
		{"F", "a,b,|c", "F,", "a,b|,c"},
		{"; repeats", "|a,b,c", "f,;", "a,b|,c"},
		{", reverses", "|a,b,c", "f,;,", "a|,b,c"},
		{"%", "|(a [b] c)", "%", "(a [b] c|)"},
		{"% back", "(a [b] c|)", "%", "|(a [b] c)"},
		{"} {", "|a\nb\n\nc", "}", "a\nb\n|\nc"},
		{"{", "a\n\nb\n|c", "{", "a\n|\nb\nc"},
		{"search", "|foo bar foo", "/foo<CR>", "foo bar |foo"},
		{"search wraps", "foo bar |foo", "/foo<CR>", "|foo bar foo"},
		{"n N", "|x a x a x", "/x<CR>nN", "x a |x a x"},
		{"? back", "x a x |a", "?x<CR>", "x a |x a"},
		{"* word", "|foo bar foo", "*", "foo bar |foo"},
		{"# word", "foo bar |foo", "#", "|foo bar foo"},
		{"smartcase", "|Foo foo FOO", "/FOO<CR>", "Foo foo |FOO"},
		{"ignorecase", "|Foo foo FOO", "/foo<CR>n", "Foo foo |FOO"},
		{"marks", "a\n|b\nc", "majj'a", "a\n|b\nc"},
		{"backtick mark", "a\nb|cd\nc", "mxgg`x", "a\nb|cd\nc"},
		{"+ -", "a\n  |b\n  c", "+", "a\n  b\n  |c"},
		{"accents l", "|ação", "ll", "aç|ão"},
		{"decomposed accent is one cell", "|éx", "l", "é|x"},
		{"wide chars", "|日本語", "l", "日|本語"},
		{"word with accents", "|não sei", "w", "não |sei"},
	})
}

func TestOperators(t *testing.T) {
	runCases(t, []vcase{
		{"x", "a|bc", "x", "a|c"},
		{"3x", "|abcde", "3x", "|de"},
		{"X", "ab|c", "X", "a|c"},
		{"dw", "|foo bar", "dw", "|bar"},
		{"dw end of line", "foo |bar\nbaz", "dw", "foo| \nbaz"},
		{"d2w", "|a b c", "d2w", "|c"},
		{"2dw", "|a b c", "2dw", "|c"},
		{"de", "|foo bar", "de", "| bar"},
		{"db", "foo |bar", "db", "|bar"},
		{"d$", "a|bcd", "d$", "|a"},
		{"D", "a|bcd", "D", "|a"},
		{"d0", "abc|d", "d0", "|d"},
		{"dd", "a\n|b\nc", "dd", "a\n|c"},
		{"dd last", "a\nb\n|c", "dd", "a\n|b"},
		{"2dd", "|a\nb\nc", "2dd", "|c"},
		{"dj", "|a\nb\nc", "dj", "|c"},
		{"dk", "a\n|b\nc", "dk", "|c"},
		{"dG", "a\n|b\nc", "dG", "|a"},
		{"dgg", "a\n|b\nc", "dgg", "|c"},
		{"dd only line", "|abc", "dd", "|"},
		{"df", "|a,b,c", "df,", "|b,c"},
		{"dt", "|a,b,c", "dt,", "|,b,c"},
		{"d}", "|a\nb\n\nc", "d}", "|\nc"},
		{"diw", "foo b|ar baz", "diw", "foo | baz"},
		{"daw", "foo b|ar baz", "daw", "foo |baz"},
		{"daw end", "foo b|ar", "daw", "fo|o"},
		{"di\"", `say "hel|lo" now`, `di"`, `say "|" now`},
		{"da\"", `say "hel|lo" now`, `da"`, `say |now`},
		{"di(", "f(a, |b)", "di(", "f(|)"},
		{"da(", "f(a, |b)", "da(", "|f"},
		{"di( on paren", "f|(a)", "dib", "f(|)"},
		{"ci{ multiline", "x {\n  a\n  |b\n}", "ci{z<Esc>", "x {\n  |z\n}"},
		{"di[ nested", "[a [|b] c]", "di[", "[a [|] c]"},
		{"2di( outer", "(a (|b) c)", "2di(", "(|)"},
		{"dip", "a\n|b\n\nc", "dip", "|\nc"},
		{"dap", "a\n|b\n\nc", "dap", "|c"},
		{"cw", "|foo bar", "cwx<Esc>", "|x bar"},
		{"cw last char", "fo|o bar", "cwx<Esc>", "fo|x bar"},
		{"ciw", "a f|oo b", "ciwbar<Esc>", "a ba|r b"},
		{"cc keeps indent", "  |abc", "ccx<Esc>", "  |x"},
		{"C", "a|bc", "Cx<Esc>", "a|x"},
		{"s", "|abc", "sx<Esc>", "|xbc"},
		{"S", "a\n|bcd\ne", "Sx<Esc>", "a\n|x\ne"},
		{"c$", "a|bc", "c$Z<Esc>", "a|Z"},
		{"yy p", "|a\nb", "yyp", "a\n|a\nb"},
		{"yy P", "a\n|b", "yyP", "a\n|b\nb"},
		{"yw p", "|ab cd", "ywP", "ab| ab cd"},
		{"yiw P", "x |foo", "yiwP", "x fo|ofoo"},
		{"3p", "|a", "yl3p", "aaa|a"},
		{"xp swap", "|ab", "xp", "b|a"},
		{"ddp swap lines", "|a\nb", "ddp", "b\n|a"},
		{">>", "|a", ">>", "    |a"},
		{"<<", "    |a", "<<", "|a"},
		{">j", "|a\nb", ">j", "    |a\n    b"},
		{"g~w", "|foo bar", "g~w", "|FOO bar"},
		{"gUU", "|foo bar", "gUU", "|FOO BAR"},
		{"guiw", "FO|O bar", "guiw", "|foo bar"},
		{"~", "|ab", "~~", "A|B"},
		{"r", "|abc", "rx", "|xbc"},
		{"3r", "|abcd", "3rx", "xx|xd"},
		{"r enter", "a|bc", "r<CR>", "a\n|c"},
		{"J", "|a\n  b", "J", "a| b"},
		{"3J", "|a\nb\nc", "3J", "a b| c"},
		{"gJ", "|a\n  b", "gJ", "a|  b"},
		{"ctrl+a", "x |9 y", "<C-a>", "x 1|0 y"},
		{"ctrl+x negative", "|1", "3<C-x>", "-|2"},
		{"d/", "|foo bar baz", "d/baz<CR>", "|baz"},
		{"x accent", "|éa", "x", "|a"},
		{"x decomposed", "|éa", "x", "|a"},
		{"dw unicode", "|ação feita", "dw", "|feita"},
		{"x wide", "a|日b", "x", "a|b"},
	})
}

func TestInsert(t *testing.T) {
	runCases(t, []vcase{
		{"i", "a|b", "ix<Esc>", "a|xb"},
		{"a", "a|b", "ax<Esc>", "ab|x"},
		{"I", "  a|b", "Ix<Esc>", "  |xab"},
		{"A", "a|b", "Ax<Esc>", "ab|x"},
		{"o", "|a\nb", "ox<Esc>", "a\n|x\nb"},
		{"O", "a\n|b", "Ox<Esc>", "a\n|x\nb"},
		{"o indent", "  |a", "ox<Esc>", "  a\n  |x"},
		{"3i", "|", "3iab<Esc>", "ababa|b"},
		{"2o", "|a", "2ox<Esc>", "a\nx\n|x"},
		{"enter splits", "a|b", "i<CR><Esc>", "a\n|b"},
		{"backspace joins", "a\n|b", "i<BS><Esc>", "|ab"},
		{"ctrl-w", "foo bar|", "a<C-w><Esc>", "foo| "},
		{"accents typed", "|", "iação<Esc>", "açã|o"},
		{"tab expands", "|a", "i<Tab><Esc>", "   | a"},
		{"ctrl-r", "|ab", "yiwA <C-r>\"<Esc>", "ab a|b"},
		{"R", "|abc", "Rxy<Esc>", "x|yc"},
		{"R backspace restores", "|abc", "Rxy<BS><Esc>", "|xbc"},
		{"insert arrows", "|ab", "i<Right>x<Esc>", "a|xb"},
	})
}

func TestVisual(t *testing.T) {
	runCases(t, []vcase{
		{"vd", "|abcd", "vld", "|cd"},
		{"vy p", "|ab", "vly$p", "aba|b"},
		{"Vd", "a\n|b\nc", "Vd", "a\n|c"},
		{"Vjd", "|a\nb\nc", "Vjd", "|c"},
		{"v iw", "a f|oo b", "viwd", "a | b"},
		{"v o", "a|bcd", "vlohd", "|d"},
		{"vc", "|abc", "vlcX<Esc>", "|Xc"},
		{"v~", "|ab", "vl~", "|AB"},
		{"vU", "|ab", "vlU", "|AB"},
		{"v>", "|a\nb", "Vj>", "    |a\n    b"},
		{"vr", "|abc", "vlrx", "|xxc"},
		{"vJ", "|a\nb\nc", "VjjJ", "a b| c"},
		{"vp replaces", "|foo bar", "yiwwviwp", "foo fo|o"},
		{"vp unnamed gets deleted", "|foo bar", "yiwwviwp0P", "ba|rfoo foo"},
		{"block d", "|abc\ndef\nghi", "<C-v>jld", "|c\nf\nghi"},
		{"block I", "|abc\ndef", "<C-v>jI# <Esc>", "|# abc\n# def"},
		{"block A", "|abc\ndef", "<C-v>jA!<Esc>", "|a!bc\nd!ef"},
		{"block $A", "|ab\ndefg", "<C-v>j$A;<Esc>", "a|b;\ndefg;"},
		{"block c", "|abc\ndef", "<C-v>jlcX<Esc>", "|Xc\nXf"},
		{"block y p", "|ab\ncd", "<C-v>jy$p", "ab|a\ncdc"},
		{"block r", "|abc\ndef", "<C-v>jlrx", "|xxc\nxxf"},
		{"gv", "|abc", "vl<Esc>gvd", "|c"},
		{"v$ includes newline-free end", "a|bc\nd", "v$d", "|a\nd"},
	})
}

func TestUndoRedoDot(t *testing.T) {
	runCases(t, []vcase{
		{"u", "|abc", "xu", "|abc"},
		{"u insert is one change", "|", "iabc<CR>def<Esc>u", "|"},
		{"ctrl-r", "|abc", "xxuu<C-r>", "|bc"},
		{"dot x", "|abcd", "x..", "|d"},
		{"dot dw", "|a b c d", "dw.", "|c d"},
		{"dot count", "|a b c d e", "dw2.", "|d e"},
		{"dot insert", "|x", "Aab<Esc>.", "xaba|b"},
		{"dot ciw", "|foo foo", "ciwbar<Esc>w.", "bar ba|r"},
		{"dot o", "|a", "ob<Esc>.", "a\nb\n|b"},
		{"dot visual line", "|a\nb\nc\nd\ne", "Vjd.", "|e"},
		{"dot >>", "|a", ">>.", "        |a"},
		{"undo cursor", "a\n|b\nc", "ddu", "a\n|b\nc"},
		{"u after s", "|abc", "sxyz<Esc>u", "|abc"},
	})
}

func TestRegistersAndMacros(t *testing.T) {
	runCases(t, []vcase{
		{"named register", "|foo\nbar", "\"ayyj\"ap", "foo\nbar\n|foo"},
		{"append register", "|a\nb", "\"ayyj\"AyyG\"ap", "a\nb\n|a\nb"},
		{"blackhole keeps unnamed", "|a b", "yiww\"_dwP", "a|a "},
		{"0 register after delete", "|a\nb", "yyjdd\"0P", "|a\na"},
		{"numbered shift", "|a\nb\nc", "dddd\"2p", "c\n|a"},
		{"macro", "|1\n1\n1", "qa<C-a>jq2@a", "2\n2\n|2"},
		{"@@", "|a\na\na", "qaA!<Esc>jq@a@@", "a!\na!\na|!"},
	})
}

func TestEx(t *testing.T) {
	runCases(t, []vcase{
		{":s", "|foo foo", ":s/foo/bar/<CR>", "|bar foo"},
		{":s g", "|foo foo", ":s/foo/bar/g<CR>", "|bar bar"},
		{":%s", "|a\nb a\na", ":%s/a/x/g<CR>", "x\nb x\n|x"},
		{":2,3s", "|a\na\na", ":2,3s/a/b/<CR>", "a\nb\n|b"},
		{":s groups", "|john smith", `:s/\(\w\+\) \(\w\+\)/\2 \1/<CR>`, "|smith john"},
		{":s amp", "|ab", ":s/b/[&]/<CR>", "|a[b]"},
		{":s case", "|foo", `:s/foo/\u&/<CR>`, "|Foo"},
		{":s newline", "|a,b", `:s/,/\r/<CR>`, "a\n|b"},
		{":s other sep", "|a/b", ":s#/#-#<CR>", "|a-b"},
		{":'<,'>s", "|a\na\na", "Vj:s/a/z/<CR>", "z\n|z\na"},
		{":s ignore flag", "|Foo", ":s/foo/x/i<CR>", "|x"},
		{"& repeat", "|ab\nab", ":s/a/x/<CR>j&", "xb\n|xb"},
		{":N goto", "|a\nb\nc", ":3<CR>", "a\nb\n|c"},
		{":d range", "|a\nb\nc\nd", ":2,3d<CR>", "a\n|d"},
		{":.,$d", "a\n|b\nc", ":.,$d<CR>", "|a"},
		{":sort", "|c\na\nb", ":sort<CR>", "|a\nb\nc"},
		{":sort!", "|a\nc\nb", ":sort!<CR>", "|c\nb\na"},
		{":normal", "|a\nb", ":%norm A;<CR>", "a;\nb|;"},
		{":undo", "|ab", "x:undo<CR>", "|ab"},
		{":j", "|a\nb", ":j<CR>", "a| b"},
	})
}

func TestEffects(t *testing.T) {
	e := New("x", DefaultOptions())
	check := func(keys string, kind EffectKind, name string, bang bool) {
		t.Helper()
		effs := e.FeedKeys(keys)
		if len(effs) != 1 || effs[0].Kind != kind || effs[0].Name != name || effs[0].Bang != bang {
			t.Fatalf("%s: effects %+v", keys, effs)
		}
	}
	check(":w<CR>", EffWrite, "", false)
	check(":q!<CR>", EffQuit, "", true)
	check(":wq<CR>", EffWriteQuit, "", false)
	check(":x<CR>", EffWriteQuit, "", false)
	check("ZZ", EffWriteQuit, "", false)
	check(":edit!<CR>", EffEditExternal, "", false)
	check(":ext<CR>", EffEditExternal, "", false)
	check(":vsplit notes/a.md<CR>", EffEx, "vsplit", false)
	check("<C-w>v", EffWindow, "", false)
	check("K", EffHover, "", false)
	check("gf", EffFollowLink, "", false)
	if effs := e.FeedKeys(":vs a.md<CR>"); effs[0].Args != "a.md" {
		t.Fatalf("args %+v", effs)
	}
}

func TestEscapeSequences(t *testing.T) {
	opts := DefaultOptions()
	opts.EscapeSeqs = []string{"jj", "ff"}
	now := time.Unix(0, 0)
	e := New("", opts)
	e.Now = func() time.Time { return now }
	e.FeedKeys("iabjj")
	if e.Mode() != ModeNormal || e.Text() != "ab" {
		t.Fatalf("jj: mode %v text %q", e.Mode(), e.Text())
	}
	// slow typing inserts the letters
	e.FeedKeys("A")
	e.Feed("j")
	now = now.Add(time.Second)
	e.Feed("j")
	if e.Mode() != ModeInsert || e.Text() != "abjj" {
		t.Fatalf("slow jj: mode %v text %q", e.Mode(), e.Text())
	}
	e.FeedKeys("ff")
	if e.Mode() != ModeNormal || e.Text() != "abjj" {
		t.Fatalf("ff: mode %v text %q", e.Mode(), e.Text())
	}
	// dot repeat after a jj exit replays the insert without the jj
	e = New("x", opts)
	e.Now = func() time.Time { return now }
	e.FeedKeys("A!jj.")
	if e.Text() != "x!!" {
		t.Fatalf("dot after jj: %q", e.Text())
	}
}

func TestClipboardOSCAndRegisters(t *testing.T) {
	e := New("hello world", DefaultOptions())
	clip := &MemClipboard{}
	e.Clip = clip
	e.FeedKeys("yiw")
	if clip.Text != "hello" {
		t.Fatalf("unnamed → clipboard: %q", clip.Text)
	}
	e.FeedKeys("yy")
	if clip.Text != "hello world\n" {
		t.Fatalf("linewise clipboard: %q", clip.Text)
	}
	clip.Text = "outside"
	e.FeedKeys("$p")
	if e.Text() != "hello worldoutside" {
		t.Fatalf("paste from clipboard: %q", e.Text())
	}
	opts := DefaultOptions()
	opts.ClipboardUnnamed = false
	e = New("abc", opts)
	clip = &MemClipboard{}
	e.Clip = clip
	e.FeedKeys("yl")
	if clip.Text != "" {
		t.Fatalf("clipboard touched without unnamedplus: %q", clip.Text)
	}
	e.FeedKeys("\"+yiw")
	if clip.Text != "abc" {
		t.Fatalf("\"+ yank: %q", clip.Text)
	}
}

func TestYankFlash(t *testing.T) {
	now := time.Unix(100, 0)
	e := New("one two", DefaultOptions())
	e.Now = func() time.Time { return now }
	e.FeedKeys("yiw")
	s, en, mode, ok := e.Flash(200 * time.Millisecond)
	if !ok || s != (Pos{0, 0}) || en != (Pos{0, 2}) || mode != ModeVisual {
		t.Fatalf("flash %v %v %v %v", s, en, mode, ok)
	}
	now = now.Add(time.Second)
	if _, _, _, ok := e.Flash(200 * time.Millisecond); ok {
		t.Fatal("flash should expire")
	}
}

func TestMappingsAndLeader(t *testing.T) {
	rc := ParseVimrc(`let mapleader = " "
nnoremap <leader>w :w<CR>
inoremap jk <Esc>
nmap Q dd
noremap H ^
map :W :write
set tabstop=2 shiftwidth=2 relativenumber
`, DefaultOptions())
	if rc.ErrorCount() != 0 {
		t.Fatalf("diags %+v", rc.Diagnostics)
	}
	e := New("  a\nb", rc.Options)
	if effs := e.FeedKeys(" w"); len(effs) != 1 || effs[0].Kind != EffWrite {
		t.Fatalf("leader map: %+v", effs)
	}
	e.FeedKeys("Q")
	if e.Text() != "b" {
		t.Fatalf("Q: %q", e.Text())
	}
	e.FeedKeys("ixjk")
	if e.Mode() != ModeNormal || e.Text() != "xb" {
		t.Fatalf("jk: %v %q", e.Mode(), e.Text())
	}
	if effs := e.FeedKeys(":W<CR>"); len(effs) != 1 || effs[0].Kind != EffWrite {
		t.Fatalf("ex alias: %+v", effs)
	}
	if !e.Opts.RelativeNumber || e.Opts.TabStop != 2 || e.Opts.ShiftWidth != 2 {
		t.Fatalf("opts %+v", e.Opts)
	}
}

func TestHostMaps(t *testing.T) {
	opts := DefaultOptions()
	opts.HostMaps = map[string]string{"<leader>e": "explorer"}
	e := New("a", opts)
	effs := e.FeedKeys(`\e`)
	if len(effs) != 1 || effs[0].Kind != EffHostMap || effs[0].Arg != "explorer" {
		t.Fatalf("host map: %+v", effs)
	}
	// a prefix that matches nothing runs the keys normally
	e = New("ab", opts)
	e.FeedKeys(`\l`)
	if e.Cursor().Col != 1 {
		t.Fatalf("flushed keys: %+v", e.Cursor())
	}
}

func TestVimrcDiagnostics(t *testing.T) {
	rc := ParseVimrc(`" comment
source ~/.other
nnoremap <expr> x Foo()
autocmd BufRead * echo 1
set foo
set tabstop=99
let g:x = 1
syntax on
unmap zz
nnoremap <C-q> :q<CR>
`, DefaultOptions())
	sev := map[int]Severity{}
	for _, d := range rc.Diagnostics {
		sev[d.Line] = d.Severity
	}
	want := map[int]Severity{2: SevWarning, 3: SevError, 4: SevWarning, 5: SevWarning, 6: SevError, 7: SevWarning, 8: SevInfo, 9: SevWarning}
	for l, s := range want {
		if sev[l] != s {
			t.Errorf("line %d: got %v want %v (%+v)", l, sev[l], s, rc.Diagnostics)
		}
	}
	if len(rc.Maps) != 1 || rc.Maps[0].LHS[0] != "ctrl+q" {
		t.Fatalf("maps %+v", rc.Maps)
	}
}

func TestEscapeSequenceValidation(t *testing.T) {
	for in, ok := range map[string]bool{"jk": true, "jj": true, "j": false, "a b": false, "<Esc>": false, "abcde": false, "jjjj": true} {
		_, err := ValidateEscapeSequence(in)
		if (err == nil) != ok {
			t.Errorf("%q: err %v", in, err)
		}
	}
}

func TestShapes(t *testing.T) {
	e := New("x", DefaultOptions())
	if e.Shape(ShapeBlock) != ShapeBlock {
		t.Fatal("normal block")
	}
	e.Feed("i")
	if e.Shape(ShapeBlock) != ShapeBar || ShapeBar.DECSCUSR() != "\x1b[6 q" {
		t.Fatal("insert bar")
	}
	e.Feed("esc")
	e.Feed("R")
	if e.Shape(ShapeBlock) != ShapeUnderline {
		t.Fatal("replace underline")
	}
	e.Feed("esc")
	e.Feed("d")
	if e.Shape(ShapeBlock) != ShapeUnderline {
		t.Fatal("operator-pending underline")
	}
}

func TestScrollKeepsCursorVisible(t *testing.T) {
	var lines []string
	for i := 0; i < 100; i++ {
		lines = append(lines, "x")
	}
	e := New(strings.Join(lines, "\n"), DefaultOptions())
	e.Height = 10
	e.FeedKeys("50G")
	if e.Cursor().Line < e.Top || e.Cursor().Line >= e.Top+10 {
		t.Fatalf("cursor %d top %d", e.Cursor().Line, e.Top)
	}
	e.FeedKeys("zt")
	if e.Top != 47 {
		t.Fatalf("zt top %d", e.Top)
	}
	e.FeedKeys("gg<C-d>")
	if e.Cursor().Line != 5 {
		t.Fatalf("ctrl-d line %d", e.Cursor().Line)
	}
}
