package mermaid

import (
	"strings"

	"github.com/mattn/go-runewidth"
)

// Class tells the host how to colour a run of the diagram.
type Class uint8

const (
	Plain Class = iota // spaces
	Box                // node / participant borders
	Edge               // connectors and lifelines
	Arrow              // arrowheads
	Text               // node and participant labels
	Label              // edge and message labels
	Note               // notes, block dividers, footnotes
)

// Span is a run of cells with one class.
type Span struct {
	Text  string
	Class Class
}

// Line is one rendered row.
type Line []Span

// String is the line without classes.
func (l Line) String() string {
	var b strings.Builder
	for _, s := range l {
		b.WriteString(s.Text)
	}
	return b.String()
}

// Width is the display width of the line.
func (l Line) Width() int { return runewidth.StringWidth(l.String()) }

// Line directions: a cell's mask says which neighbours it connects to.
const (
	dUp uint8 = 1 << iota
	dDown
	dLeft
	dRight
)

// Line styles.
const (
	solid = iota
	dotted
	thick
)

type cell struct {
	mask  uint8
	style uint8
	ch    rune // text (0: none); wideTail marks the 2nd half of a wide rune
	cls   Class
}

const wideTail = -1

// canvas is a growable grid where connectors merge into box-drawing
// junctions and text sits on top.
type canvas struct {
	rows [][]cell
}

func (c *canvas) at(x, y int) *cell {
	for len(c.rows) <= y {
		c.rows = append(c.rows, nil)
	}
	for len(c.rows[y]) <= x {
		c.rows[y] = append(c.rows[y], cell{})
	}
	return &c.rows[y][x]
}

func (c *canvas) mark(x, y int, m uint8, style int, cls Class) {
	if x < 0 || y < 0 {
		return
	}
	k := c.at(x, y)
	if k.mask == 0 {
		k.style = uint8(style)
		if k.cls == Plain {
			k.cls = cls
		}
	}
	k.mask |= m
}

// hline draws a connector between (x0,y) and (x1,y), ends included.
func (c *canvas) hline(x0, x1, y, style int, cls Class) {
	if x0 > x1 {
		x0, x1 = x1, x0
	}
	for x := x0; x <= x1; x++ {
		var m uint8
		if x > x0 {
			m |= dLeft
		}
		if x < x1 {
			m |= dRight
		}
		c.mark(x, y, m, style, cls)
	}
}

// vline draws a connector between (x,y0) and (x,y1), ends included.
func (c *canvas) vline(x, y0, y1, style int, cls Class) {
	if y0 > y1 {
		y0, y1 = y1, y0
	}
	for y := y0; y <= y1; y++ {
		var m uint8
		if y > y0 {
			m |= dUp
		}
		if y < y1 {
			m |= dDown
		}
		c.mark(x, y, m, style, cls)
	}
}

// put writes text at (x,y); it overrides connectors underneath.
func (c *canvas) put(x, y int, s string, cls Class) {
	for _, r := range s {
		w := runewidth.RuneWidth(r)
		if w == 0 {
			continue
		}
		k := c.at(x, y)
		k.ch, k.cls = r, cls
		if w == 2 {
			t := c.at(x+1, y)
			t.ch, t.cls = wideTail, cls
		}
		x += w
	}
}

// free reports whether cells [x0,x1] of row y hold nothing.
func (c *canvas) free(x0, x1, y int) bool {
	if x0 < 0 || y < 0 {
		return false
	}
	if y >= len(c.rows) {
		return true
	}
	for x := x0; x <= x1 && x < len(c.rows[y]); x++ {
		if k := c.rows[y][x]; k.mask != 0 || k.ch != 0 {
			return false
		}
	}
	return true
}

func (c *canvas) width() int {
	w := 0
	for _, r := range c.rows {
		for x := len(r) - 1; x >= 0; x-- {
			if r[x].mask != 0 || r[x].ch != 0 {
				w = max(w, x+1)
				break
			}
		}
	}
	return w
}

var junction = map[uint8]rune{
	dLeft | dRight:               '─',
	dLeft:                        '─',
	dRight:                       '─',
	dUp | dDown:                  '│',
	dUp:                          '│',
	dDown:                        '│',
	dDown | dRight:               '┌',
	dDown | dLeft:                '┐',
	dUp | dRight:                 '└',
	dUp | dLeft:                  '┘',
	dUp | dDown | dRight:         '├',
	dUp | dDown | dLeft:          '┤',
	dDown | dLeft | dRight:       '┬',
	dUp | dLeft | dRight:         '┴',
	dUp | dDown | dLeft | dRight: '┼',
}

func glyph(k cell) rune {
	horiz := k.mask&^(dLeft|dRight) == 0
	vert := k.mask&^(dUp|dDown) == 0
	switch {
	case k.style == dotted && horiz:
		return '┄'
	case k.style == dotted && vert:
		return '┆'
	case k.style == thick && horiz:
		return '━'
	case k.style == thick && vert:
		return '┃'
	}
	return junction[k.mask]
}

// lines renders the grid as classed spans, trailing blanks dropped.
func (c *canvas) lines() []Line {
	out := make([]Line, 0, len(c.rows))
	for _, r := range c.rows {
		end := len(r)
		for end > 0 && r[end-1].mask == 0 && r[end-1].ch == 0 {
			end--
		}
		var l Line
		var b strings.Builder
		cur := Plain
		flush := func() {
			if b.Len() > 0 {
				l = append(l, Span{Text: b.String(), Class: cur})
				b.Reset()
			}
		}
		for x := 0; x < end; x++ {
			k := r[x]
			var ch rune
			cls := k.cls
			switch {
			case k.ch == wideTail:
				continue
			case k.ch != 0:
				ch = k.ch
			case k.mask != 0:
				ch = glyph(k)
			default:
				ch, cls = ' ', Plain
			}
			if cls != cur {
				flush()
				cur = cls
			}
			b.WriteRune(ch)
		}
		flush()
		out = append(out, l)
	}
	return out
}

// textWidth is the display width of s.
func textWidth(s string) int { return runewidth.StringWidth(s) }

// clip shortens s to at most w columns with an ellipsis.
func clip(s string, w int) string {
	if textWidth(s) <= w {
		return s
	}
	return runewidth.Truncate(s, w, "…")
}
