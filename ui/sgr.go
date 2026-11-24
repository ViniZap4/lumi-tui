package ui

import (
	"io"
	"os"
	"strconv"
	"strings"
)

// Frame size (the scroll flicker, owner report 2026-11-24).
//
// Glamour pads every prose line to its wrap width with individually
// styled cells ("\x1b[38;2;…m \x1b[0m" per space) and fillBg re-emits
// the theme background after every reset, so one 120×40 reader frame
// was ~95 KB. Every scroll step repaints every row, and a frame that
// large reaches the terminal in many reads: the emulator draws the
// half-written frame in between (tearing, "piscando").
//
// compactSGR rewrites a frame with the same look but one SGR sequence
// per style run: it tracks the effective SGR state and only emits a
// sequence when the state of the next printed cell differs from the
// state already on screen. Non-SGR escapes (OSC, cursor moves) pass
// through untouched.

// sgrState is the effective graphic rendition.
type sgrState struct {
	attrs [10]bool // index = SGR code 1..9 (0 unused)
	fg    string   // "31", "38;5;9", "38;2;r;g;b" ("" default)
	bg    string
	ul    string // underline colour (58;…)
	extra string // codes this parser does not model, kept verbatim
}

func (s *sgrState) params() string {
	var b strings.Builder
	b.WriteString("0")
	for i := 1; i < len(s.attrs); i++ {
		if s.attrs[i] {
			b.WriteByte(';')
			b.WriteString(strconv.Itoa(i))
		}
	}
	for _, p := range []string{s.fg, s.bg, s.ul, s.extra} {
		if p != "" {
			b.WriteByte(';')
			b.WriteString(p)
		}
	}
	return b.String()
}

func (s *sgrState) isDefault() bool { return *s == sgrState{} }

// apply folds one SGR parameter list into s.
func (s *sgrState) apply(params string) {
	if params == "" {
		*s = sgrState{}
		return
	}
	ps := strings.Split(params, ";")
	for i := 0; i < len(ps); i++ {
		p := ps[i]
		if strings.Contains(p, ":") { // sub-parameters (4:3 curly underline)
			s.extra = joinParam(s.extra, p)
			continue
		}
		n, err := strconv.Atoi(p)
		if err != nil && p != "" {
			s.extra = joinParam(s.extra, p)
			continue
		}
		switch {
		case p == "" || n == 0:
			*s = sgrState{}
		case n >= 1 && n <= 9:
			s.attrs[n] = true
		case n == 21:
			s.attrs[1] = false
		case n == 22:
			s.attrs[1], s.attrs[2] = false, false
		case n == 23:
			s.attrs[3] = false
		case n == 24:
			s.attrs[4] = false
		case n == 25:
			s.attrs[5], s.attrs[6] = false, false
		case n == 27:
			s.attrs[7] = false
		case n == 28:
			s.attrs[8] = false
		case n == 29:
			s.attrs[9] = false
		case n >= 30 && n <= 37, n >= 90 && n <= 97:
			s.fg = p
		case n == 39:
			s.fg = ""
		case n >= 40 && n <= 47, n >= 100 && n <= 107:
			s.bg = p
		case n == 49:
			s.bg = ""
		case n == 59:
			s.ul = ""
		case n == 38 || n == 48 || n == 58:
			col, used := extColor(ps[i+1:])
			i += used
			c := p + col
			switch n {
			case 38:
				s.fg = c
			case 48:
				s.bg = c
			default:
				s.ul = c
			}
		default:
			s.extra = joinParam(s.extra, p)
		}
	}
}

// extColor reads ";5;n" or ";2;r;g;b" after 38/48/58.
func extColor(rest []string) (string, int) {
	if len(rest) >= 2 && rest[0] == "5" {
		return ";5;" + rest[1], 2
	}
	if len(rest) >= 4 && rest[0] == "2" {
		return ";2;" + rest[1] + ";" + rest[2] + ";" + rest[3], 4
	}
	return "", 0
}

func joinParam(a, b string) string {
	if a == "" {
		return b
	}
	return a + ";" + b
}

// compactSGR rewrites s (one frame, any number of lines) with minimal
// SGR transitions. The look is unchanged: each printed cell gets the
// same effective rendition as before. Every line ends reset, as the
// lines of the frame did.
func compactSGR(s string) string {
	var out strings.Builder
	out.Grow(len(s) / 3)
	var want, shown sgrState
	for i := 0; i < len(s); {
		c := s[i]
		if c == 0x1b && i+1 < len(s) && s[i+1] == '[' {
			// CSI: parameters then a final byte in 0x40–0x7e
			j := i + 2
			for j < len(s) && (s[j] < 0x40 || s[j] > 0x7e) {
				j++
			}
			if j >= len(s) {
				out.WriteString(s[i:])
				break
			}
			if s[j] == 'm' {
				params := s[i+2 : j]
				if strings.ContainsAny(params, "<=>?") {
					out.WriteString(s[i : j+1])
				} else {
					want.apply(params)
				}
			} else {
				out.WriteString(s[i : j+1])
			}
			i = j + 1
			continue
		}
		if c == 0x1b {
			// other escapes (OSC 8 links, OSC 52…): copy up to ST/BEL
			j := i + 1
			if j < len(s) && s[j] == ']' {
				for j < len(s) && s[j] != 0x07 && !(s[j] == 0x1b && j+1 < len(s) && s[j+1] == '\\') {
					j++
				}
				if j < len(s) && s[j] == 0x1b {
					j++
				}
			}
			end := min(len(s), j+1)
			out.WriteString(s[i:end])
			i = end
			continue
		}
		if c == '\n' {
			if !shown.isDefault() {
				out.WriteString("\x1b[m")
				shown = sgrState{}
			}
			out.WriteByte('\n')
			i++
			continue
		}
		if want != shown && !(c == ' ' && sameBlank(want, shown)) {
			if want.isDefault() {
				out.WriteString("\x1b[m")
			} else {
				out.WriteString("\x1b[")
				out.WriteString(want.params())
				out.WriteByte('m')
			}
			shown = want
		}
		out.WriteByte(c)
		i++
	}
	if !shown.isDefault() {
		out.WriteString("\x1b[m")
	}
	return out.String()
}

// sameBlank: a space looks the same under a and b (only the
// foreground or text weight differ, which a blank cell does not show).
func sameBlank(a, b sgrState) bool {
	if a.attrs[7] || b.attrs[7] || a.bg != b.bg || a.extra != b.extra {
		return false
	}
	for _, i := range []int{4, 5, 8, 9} {
		if a.attrs[i] != b.attrs[i] {
			return false
		}
	}
	if a.attrs[4] && (a.fg != b.fg || a.ul != b.ul) {
		return false
	}
	return true
}

// syncWriter wraps every write of the renderer in a synchronized
// update (DEC private mode 2026, "BSU/ESU"): terminals that support it
// (kitty, WezTerm, iTerm2, Ghostty, foot, Alacritty, Windows Terminal,
// tmux ≥ 3.4) draw the frame only once it is complete, so a repaint is
// never seen half done. Others ignore the unknown mode. Bubbletea's
// renderer writes one frame per Write. It stays an *os.File so
// Bubbletea still detects the terminal (size, raw mode).
type syncWriter struct{ *os.File }

const (
	syncBegin = "\x1b[?2026h"
	syncEnd   = "\x1b[?2026l"
)

func (w syncWriter) Write(p []byte) (int, error) {
	buf := make([]byte, 0, len(p)+len(syncBegin)+len(syncEnd))
	buf = append(buf, syncBegin...)
	buf = append(buf, p...)
	buf = append(buf, syncEnd...)
	if _, err := w.File.Write(buf); err != nil {
		return 0, err
	}
	return len(p), nil
}

var _ io.Writer = syncWriter{}
