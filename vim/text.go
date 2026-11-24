// Package vim is lumi's terminal vim engine: a pure, UI-free model of a
// text buffer driven by key strings (Bubbletea's KeyMsg.String()
// spelling: "j", "esc", "enter", "ctrl+r", " ").
//
// It follows the semantics of the web (CodeMirror vim) and Apple engines
// as far as a terminal allows: modes, motions, operators, counts, text
// objects, registers (system clipboard included), `.` repeat, undo/redo,
// charwise/linewise/blockwise visual, search, `:s` with ranges, macros,
// marks, insert-mode escape sequences (jj) and a safe .vimrc subset.
//
// Positions are rune indices. Cursor motions step over grapheme
// clusters (a base letter plus combining accents, ZWJ emoji), so a
// decomposed "é" or a family emoji is one cursor cell, as in vim.
package vim

import (
	"strings"
	"unicode"

	"github.com/mattn/go-runewidth"
)

// Pos is a buffer position: 0-based line and rune column.
type Pos struct{ Line, Col int }

// Less reports whether p comes before q.
func (p Pos) Less(q Pos) bool { return p.Line < q.Line || (p.Line == q.Line && p.Col < q.Col) }

func order(a, b Pos) (Pos, Pos) {
	if b.Less(a) {
		return b, a
	}
	return a, b
}

const zwj = 0x200D

// isMark reports runes that never start a cluster (combining marks,
// variation selectors, the zero-width joiner).
func isMark(r rune) bool {
	return unicode.In(r, unicode.Mn, unicode.Me) || r == zwj || (r >= 0xFE00 && r <= 0xFE0F) ||
		(r >= 0x1F3FB && r <= 0x1F3FF) // skin tones
}

// clusterStart moves c back to the start of its grapheme cluster.
func clusterStart(l []rune, c int) int {
	if c >= len(l) {
		return c
	}
	for c > 0 && (isMark(l[c]) || l[c-1] == zwj) {
		c--
	}
	return c
}

// nextCluster returns the column just past the cluster at c.
func nextCluster(l []rune, c int) int {
	if c >= len(l) {
		return len(l)
	}
	c++
	for c < len(l) && (isMark(l[c]) || l[c-1] == zwj) {
		c++
	}
	return c
}

// prevCluster returns the start of the cluster before c.
func prevCluster(l []rune, c int) int {
	if c <= 0 {
		return 0
	}
	return clusterStart(l, c-1)
}

// lastCluster is the start of the last cluster (0 on empty lines).
func lastCluster(l []rune) int {
	if len(l) == 0 {
		return 0
	}
	return clusterStart(l, len(l)-1)
}

const (
	clsBlank = 0
	clsPunct = 1
	clsWord  = 2
)

func class(r rune, big bool) int {
	if unicode.IsSpace(r) {
		return clsBlank
	}
	if big {
		return clsWord
	}
	if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' || isMark(r) {
		return clsWord
	}
	return clsPunct
}

// RuneWidth is the terminal cell width of r (tabs are handled by callers).
func RuneWidth(r rune) int {
	if isMark(r) {
		return 0
	}
	return runewidth.RuneWidth(r)
}

// DisplayCol is the screen column where rune column c starts, with tabs
// expanded to ts.
func DisplayCol(l []rune, c, ts int) int {
	if ts < 1 {
		ts = 4
	}
	w := 0
	for i := 0; i < c && i < len(l); i++ {
		if l[i] == '\t' {
			w += ts - w%ts
		} else {
			w += RuneWidth(l[i])
		}
	}
	return w
}

// ColForDisplay is the rune column of the cluster covering display col d
// (clamped to the last cluster unless past is set).
func ColForDisplay(l []rune, d, ts int, past bool) int {
	if ts < 1 {
		ts = 4
	}
	w := 0
	for i := 0; i < len(l); i = nextCluster(l, i) {
		cw := 0
		if l[i] == '\t' {
			cw = ts - w%ts
		} else {
			for j := i; j < nextCluster(l, i); j++ {
				cw += RuneWidth(l[j])
			}
		}
		if d < w+cw || (cw == 0 && d == w) {
			return i
		}
		w += cw
	}
	if past {
		return len(l)
	}
	return lastCluster(l)
}

func firstNonBlank(l []rune) int {
	for i, r := range l {
		if r != ' ' && r != '\t' {
			return i
		}
	}
	if len(l) == 0 {
		return 0
	}
	return lastCluster(l)
}

func leadingWS(l []rune) []rune {
	i := 0
	for i < len(l) && (l[i] == ' ' || l[i] == '\t') {
		i++
	}
	return append([]rune(nil), l[:i]...)
}

func isBlankLine(l []rune) bool { return strings.TrimSpace(string(l)) == "" }

func cloneLines(ls [][]rune) [][]rune {
	out := make([][]rune, len(ls))
	for i, l := range ls {
		out[i] = append([]rune(nil), l...)
	}
	return out
}

func splitLines(s string) [][]rune {
	parts := strings.Split(s, "\n")
	out := make([][]rune, len(parts))
	for i, p := range parts {
		out[i] = []rune(p)
	}
	return out
}

func joinLines(ls [][]rune) string {
	var b strings.Builder
	for i, l := range ls {
		if i > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(string(l))
	}
	return b.String()
}
