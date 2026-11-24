package vim

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// A safe .vimrc subset, the same grammar as the web client
// (web-client/src/lib/vimrc.ts). The text is PARSED, never evaluated:
// mappings, the leader and a few `set` options become data; everything
// else is reported per line and ignored. No line can run a command, load
// a file or reach the network.

// Severity of a vimrc diagnostic.
type Severity int

const (
	SevInfo Severity = iota
	SevWarning
	SevError
)

func (s Severity) String() string {
	return [...]string{"info", "warning", "error"}[s]
}

// Diagnostic is one per-line vimrc message.
type Diagnostic struct {
	Line     int
	Severity Severity
	Message  string
}

// Limits (same as the web client).
const (
	MaxVimrcBytes = 16 * 1024
	MaxVimrcMaps  = 200
	MaxLHSLen     = 48
	MaxRHSLen     = 256
)

var mapCommands = map[string]struct {
	mode    string
	noremap bool
}{
	"map": {"all", false}, "nmap": {"normal", false}, "nm": {"normal", false},
	"imap": {"insert", false}, "im": {"insert", false},
	"vmap": {"visual", false}, "vm": {"visual", false}, "xmap": {"visual", false}, "xm": {"visual", false},
	"noremap": {"all", true}, "no": {"all", true},
	"nnoremap": {"normal", true}, "nn": {"normal", true},
	"inoremap": {"insert", true}, "ino": {"insert", true},
	"vnoremap": {"visual", true}, "vn": {"visual", true}, "xnoremap": {"visual", true}, "xn": {"visual", true},
}

var unmapCommands = map[string]string{
	"unmap": "all", "unm": "all", "nunmap": "normal", "nun": "normal", "iunmap": "insert", "iu": "insert",
	"vunmap": "visual", "vu": "visual", "xunmap": "visual", "xu": "visual",
}

var ignoredCommands = map[string]bool{"syntax": true, "syn": true, "filetype": true, "filet": true, "colorscheme": true, "colo": true}

var scriptCommands = map[string]bool{
	"source": true, "so": true, "runtime": true, "ru": true, "function": true, "fu": true, "function!": true,
	"endfunction": true, "endf": true, "call": true, "cal": true, "execute": true, "exe": true, "autocmd": true,
	"au": true, "augroup": true, "aug": true, "command": true, "command!": true, "com": true, "if": true,
	"endif": true, "else": true, "for": true, "endfor": true, "while": true, "endwhile": true, "lua": true,
	"python": true, "py": true, "python3": true, "py3": true, "ruby": true, "perl": true, "normal": true,
	"norm": true, "normal!": true, "norm!": true, "silent": true, "sil": true, "silent!": true, "packadd": true,
	"Plug": true,
}

var mapFlags = map[string]bool{"<silent>": true, "<nowait>": true, "<unique>": true, "<buffer>": true, "<special>": true, "<script>": true}

var boolOpts = map[string]string{
	"number": "number", "nu": "number", "relativenumber": "relativenumber", "rnu": "relativenumber",
	"wrap": "wrap", "expandtab": "expandtab", "et": "expandtab",
	"ignorecase": "ignorecase", "ic": "ignorecase", "smartcase": "smartcase", "scs": "smartcase",
	"hlsearch": "hlsearch", "hls": "hlsearch", "incsearch": "incsearch", "is": "incsearch",
}
var alwaysOn = map[string]bool{"ignorecase": true, "smartcase": true, "hlsearch": true, "incsearch": true}
var numOpts = map[string]string{"tabstop": "tabstop", "ts": "tabstop", "shiftwidth": "shiftwidth", "sw": "shiftwidth",
	"softtabstop": "softtabstop", "sts": "softtabstop"}
var ignoredOpts = map[string]bool{"nocompatible": true, "compatible": true, "cp": true, "nocp": true, "encoding": true,
	"enc": true, "fileencoding": true, "fenc": true, "syntax": true, "background": true, "bg": true,
	"termguicolors": true, "tgc": true, "mouse": true}

var (
	reCmd       = regexp.MustCompile(`^(\S+)\s*(.*)$`)
	reFlag      = regexp.MustCompile(`(?i)^(<[a-z]+>)\s*`)
	reMapParts  = regexp.MustCompile(`^(\S+)\s+(.+)$`)
	reLet       = regexp.MustCompile(`^(g:)?(mapleader|maplocalleader)\s*=\s*(.+)$`)
	reSetEq     = regexp.MustCompile(`^([a-z]+)\s*[=:]\s*(.*)$`)
	reCtrlChars = regexp.MustCompile("[\x00-\x08\x0b-\x1f\x7f]")
	reLeaderPfx = regexp.MustCompile(`(?i)^<leader>`)
)

// Vimrc is a parsed vimrc.
type Vimrc struct {
	Maps        []Map
	ExAliases   map[string]string
	Leader      string // notation: `\`, `,`, `<Space>`
	Options     Options
	Set         map[string]bool // options the vimrc set explicitly
	Diagnostics []Diagnostic
}

// ParseVimrc parses vimrc text over base options.
func ParseVimrc(text string, base Options) Vimrc {
	out := Vimrc{Leader: `\`, Options: base, Set: map[string]bool{}, ExAliases: map[string]string{}}
	diag := func(n int, s Severity, m string) {
		out.Diagnostics = append(out.Diagnostics, Diagnostic{Line: n, Severity: s, Message: m})
	}
	if len(text) > MaxVimrcBytes {
		diag(1, SevError, fmt.Sprintf("vimrc is larger than %d KB; only the first %d KB are read", MaxVimrcBytes/1024, MaxVimrcBytes/1024))
		text = text[:MaxVimrcBytes]
	}
	type rawMap struct {
		mode, lhs, rhs  string
		noremap, leader bool
		leaderAt        string
	}
	var maps []rawMap
	for idx, raw := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		n := idx + 1
		line := strings.TrimSpace(strings.ReplaceAll(raw, "\t", " "))
		if line == "" || strings.HasPrefix(line, `"`) {
			continue
		}
		if reCtrlChars.MatchString(line) {
			diag(n, SevError, "control characters are not allowed")
			continue
		}
		line = strings.TrimSpace(strings.TrimLeft(line, ":"))
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, `\`) {
			diag(n, SevError, `line continuations (\) are not supported`)
			continue
		}
		m := reCmd.FindStringSubmatch(line)
		cmd, rest := m[1], m[2]
		if mc, ok := mapCommands[cmd]; ok {
			if len(maps) >= MaxVimrcMaps {
				diag(n, SevError, fmt.Sprintf("more than %d mappings; ignored", MaxVimrcMaps))
				continue
			}
			args := rest
			bad := false
			for {
				f := reFlag.FindStringSubmatch(args)
				if f == nil {
					break
				}
				fl := strings.ToLower(f[1])
				if fl == "<expr>" {
					diag(n, SevError, "<expr> mappings evaluate code and are not supported")
					bad = true
					break
				}
				if !mapFlags[fl] {
					break
				}
				args = args[len(f[0]):]
			}
			if bad {
				continue
			}
			parts := reMapParts.FindStringSubmatch(args)
			if parts == nil {
				if strings.TrimSpace(args) != "" {
					diag(n, SevError, cmd+": missing right-hand side")
				} else {
					diag(n, SevError, cmd+": listing mappings is not supported here")
				}
				continue
			}
			lhs, rhs := parts[1], strings.TrimSpace(parts[2])
			leaderPfx := reLeaderPfx.MatchString(lhs) && mc.mode != "insert"
			if leaderPfx {
				lhs = lhs[8:]
			}
			if _, err := ParseKeys(lhs, out.Leader); err != nil {
				diag(n, SevError, err.Error())
				continue
			}
			if _, err := ParseKeys(rhs, out.Leader); err != nil {
				diag(n, SevError, err.Error())
				continue
			}
			if (lhs == "" && !leaderPfx) || rhs == "" {
				diag(n, SevError, cmd+": empty key")
				continue
			}
			if len(lhs) > MaxLHSLen || len(rhs) > MaxRHSLen {
				diag(n, SevError, fmt.Sprintf("mapping too long (max %d keys left, %d right)", MaxLHSLen, MaxRHSLen))
				continue
			}
			if strings.HasPrefix(lhs, ":") && lhs != ":" {
				if mc.mode != "all" {
					diag(n, SevError, "ex-command aliases (:Name) need plain map/noremap, not a mode-specific map")
					continue
				}
				if !regexp.MustCompile(`^:[A-Za-z][A-Za-z0-9]*$`).MatchString(lhs) {
					diag(n, SevError, "ex-command alias must be a word, e.g. map :W :write")
					continue
				}
				target := strings.TrimSuffix(strings.TrimSuffix(strings.TrimPrefix(rhs, ":"), "<CR>"), "<cr>")
				out.ExAliases[lhs[1:]] = target
				continue
			}
			kept := maps[:0]
			for _, x := range maps {
				if !(x.lhs == lhs && x.mode == mc.mode && x.leader == leaderPfx) {
					kept = append(kept, x)
				}
			}
			maps = append(kept, rawMap{mode: mc.mode, lhs: lhs, rhs: rhs, noremap: mc.noremap, leader: leaderPfx, leaderAt: out.Leader})
			continue
		}
		if mode, ok := unmapCommands[cmd]; ok {
			arg := strings.TrimSpace(regexp.MustCompile(`(?i)^(<(silent|buffer|nowait)>\s*)+`).ReplaceAllString(rest, ""))
			if arg == "" {
				diag(n, SevError, cmd+": missing key")
				continue
			}
			leaderPfx := reLeaderPfx.MatchString(arg)
			if leaderPfx {
				arg = arg[8:]
			}
			before := len(maps)
			kept := maps[:0]
			for _, x := range maps {
				if !(x.lhs == arg && x.leader == leaderPfx && (mode == "all" || x.mode == mode || x.mode == "all")) {
					kept = append(kept, x)
				}
			}
			maps = kept
			if len(maps) == before {
				diag(n, SevWarning, "no mapping of "+arg+" above; built-in keys cannot be removed")
			}
			continue
		}
		if cmd == "let" {
			lm := reLet.FindStringSubmatch(strings.TrimSpace(stripComment(rest)))
			if lm == nil {
				diag(n, SevWarning, "only `let mapleader` / `let maplocalleader` are supported (variables are never evaluated)")
				continue
			}
			v, ok := parseLeader(lm[3])
			if !ok {
				diag(n, SevError, lm[2]+` must be one quoted key, e.g. " " or ","`)
				continue
			}
			if lm[2] == "mapleader" {
				out.Leader = v
			}
			continue
		}
		if cmd == "set" || cmd == "se" || cmd == "setlocal" || cmd == "setl" || cmd == "setglobal" {
			body := strings.TrimSpace(stripComment(rest))
			if body == "" {
				diag(n, SevError, "set: missing option")
				continue
			}
			for _, tok := range strings.Fields(body) {
				var ds []Diagnostic
				applySetToken(tok, n, &out.Options, &ds)
				out.Diagnostics = append(out.Diagnostics, ds...)
				if len(ds) == 0 || ds[0].Severity != SevError {
					out.Set[strings.SplitN(strings.TrimPrefix(tok, "no"), "=", 2)[0]] = true
				}
			}
			continue
		}
		if ignoredCommands[cmd] || line == "set nocompatible" {
			diag(n, SevInfo, "ignored: lumi handles syntax and colours itself")
			continue
		}
		if scriptCommands[cmd] || strings.HasPrefix(cmd, "call") || (cmd[0] >= 'A' && cmd[0] <= 'Z') {
			diag(n, SevWarning, "`"+cmd+"` is not supported: this vimrc never runs scripts or plugins")
			continue
		}
		diag(n, SevError, "unknown command `"+cmd+"`")
	}
	for _, rm := range maps {
		lhsNot := rm.lhs
		if rm.leader {
			lhsNot = "<leader>" + rm.lhs
		}
		lk, err1 := ParseKeys(lhsNot, rm.leaderAt)
		rk, err2 := ParseKeys(rm.rhs, rm.leaderAt)
		if err1 != nil || err2 != nil || len(lk) == 0 {
			continue
		}
		out.Maps = append(out.Maps, Map{Mode: rm.mode, LHS: lk, RHS: rk, Noremap: rm.noremap})
	}
	out.Options.Maps = out.Maps
	out.Options.ExAliases = out.ExAliases
	out.Options.Leader = out.Leader
	return out
}

// stripComment drops a trailing `" comment` on set/let lines.
func stripComment(s string) string {
	inS, inD := false, false
	for i, c := range s {
		switch {
		case c == '\'' && !inD:
			inS = !inS
		case c == '"' && !inS:
			prev := s[:i]
			if strings.TrimRight(prev, " ") != prev && !strings.HasSuffix(strings.TrimRight(prev, " "), "=") && !inD {
				return strings.TrimRight(prev, " ")
			}
			inD = !inD
		}
	}
	return s
}

func parseLeader(raw string) (string, bool) {
	v := strings.TrimSpace(raw)
	var s string
	switch {
	case len(v) >= 2 && v[0] == '"' && v[len(v)-1] == '"':
		s = v[1 : len(v)-1]
		s = regexp.MustCompile(`(?i)\\<space>`).ReplaceAllString(s, " ")
		s = regexp.MustCompile(`\\<([^>]+)>`).ReplaceAllString(s, "<$1>")
		s = regexp.MustCompile(`\\(.)`).ReplaceAllString(s, "$1")
	case len(v) >= 2 && v[0] == '\'' && v[len(v)-1] == '\'':
		s = strings.ReplaceAll(v[1:len(v)-1], "''", "'")
	default:
		return "", false
	}
	if s == " " {
		return "<Space>", true
	}
	if strings.HasPrefix(s, "<") && strings.HasSuffix(s, ">") {
		if _, err := ParseKeys(s, `\`); err != nil {
			return "", false
		}
		return s, true
	}
	if len([]rune(s)) != 1 || reCtrlChars.MatchString(s) {
		return "", false
	}
	return s, true
}

func applySetToken(tok string, n int, o *Options, ds *[]Diagnostic) {
	d := func(s Severity, m string) { *ds = append(*ds, Diagnostic{Line: n, Severity: s, Message: m}) }
	if strings.HasPrefix(tok, "clipboard") {
		v := strings.TrimLeft(strings.TrimPrefix(tok, "clipboard"), "=:")
		o.ClipboardUnnamed = strings.Contains(v, "unnamed")
		return
	}
	if m := reSetEq.FindStringSubmatch(tok); m != nil {
		key, ok := numOpts[m[1]]
		if !ok {
			if ignoredOpts[m[1]] {
				d(SevInfo, "option `"+m[1]+"` is not supported")
			} else {
				d(SevWarning, "option `"+m[1]+"` is not supported")
			}
			return
		}
		v, err := strconv.Atoi(m[2])
		if err != nil || v < 1 || v > 16 {
			d(SevError, m[1]+" must be a number from 1 to 16")
			return
		}
		switch key {
		case "softtabstop":
			d(SevInfo, "softtabstop accepted (indent follows shiftwidth)")
		case "tabstop":
			o.TabStop = v
		case "shiftwidth":
			o.ShiftWidth = v
		}
		return
	}
	if regexp.MustCompile(`[+^-]=`).MatchString(tok) {
		d(SevWarning, "`"+tok+"`: += / -= / ^= are not supported")
		return
	}
	if strings.HasSuffix(tok, "!") || strings.HasPrefix(tok, "inv") {
		d(SevWarning, "`"+tok+"`: toggling (! / inv) is not supported; use the plain or no… form")
		return
	}
	if strings.HasSuffix(tok, "?") || strings.HasSuffix(tok, "&") {
		d(SevInfo, "`"+tok+"` ignored")
		return
	}
	name, value := tok, true
	opt, ok := boolOpts[name]
	if !ok && strings.HasPrefix(name, "no") {
		if opt, ok = boolOpts[name[2:]]; ok {
			value = false
		}
	}
	if !ok {
		if _, isNum := numOpts[name]; isNum {
			d(SevError, name+" needs a value, e.g. "+name+"=4")
		} else if ignoredOpts[tok] {
			d(SevInfo, "option `"+tok+"` is not supported")
		} else {
			d(SevWarning, "option `"+tok+"` is not supported")
		}
		return
	}
	if alwaysOn[opt] {
		if value {
			d(SevInfo, opt+" is always on in this editor")
		} else {
			d(SevWarning, opt+" cannot be turned off in this editor")
		}
		return
	}
	switch opt {
	case "number":
		o.Number = value
	case "relativenumber":
		o.RelativeNumber = value
	case "wrap":
		o.Wrap = value
	case "expandtab":
		o.ExpandTab = value
	}
}

// ErrorCount is the number of error diagnostics.
func (v Vimrc) ErrorCount() int {
	n := 0
	for _, d := range v.Diagnostics {
		if d.Severity == SevError {
			n++
		}
	}
	return n
}

// ValidateEscapeSequence checks one escape sequence (2–4 printable
// characters, no spaces, no key notation) — the web rules.
func ValidateEscapeSequence(raw string) (string, error) {
	v := strings.TrimSpace(raw)
	rs := []rune(v)
	switch {
	case len(rs) < 2:
		return "", fmt.Errorf("use at least 2 characters (e.g. jk)")
	case len(rs) > 4:
		return "", fmt.Errorf("use at most 4 characters")
	case strings.ContainsAny(v, " \t"):
		return "", fmt.Errorf("spaces are not allowed")
	case strings.Contains(v, "<"):
		return "", fmt.Errorf("key notation (<…>) is not allowed here")
	case reCtrlChars.MatchString(v):
		return "", fmt.Errorf("only printable characters")
	}
	return v, nil
}
