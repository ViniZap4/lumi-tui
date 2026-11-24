package ui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/vinizap/lumi/tui-client/theme"
)

// Cards: the centred, bordered panels of the clone / link prompt, the
// sign-in form and the accounts & servers screen. Every action on a card
// is a button that is both a key and a mouse target: the card records
// hit regions (content coordinates) while it is built, and the mouse
// handler maps a click back to the key it presses.

// cardHit is a clickable span of a card line: [x0, x1) on content row y.
type cardHit struct {
	x0, x1, y int
	key       string
}

// cardBuilder accumulates card lines and their hit regions.
type cardBuilder struct {
	lines []string
	hits  []cardHit
	cur   strings.Builder
	x     int
}

// text appends styled text to the current line.
func (c *cardBuilder) text(s string) {
	c.cur.WriteString(s)
	c.x += lipgloss.Width(s)
}

// button appends "key label" as a clickable action pressing press.
func (c *cardBuilder) button(key, label, press string) {
	t := theme.Current
	k := lipgloss.NewStyle().Foreground(secondaryColor).Bold(true).Render(key)
	l := lipgloss.NewStyle().Foreground(t.Text).Render(label)
	s := k + " " + l
	w := lipgloss.Width(s)
	c.hits = append(c.hits, cardHit{x0: c.x, x1: c.x + w, y: len(c.lines), key: press})
	c.text(s)
}

// hit makes the whole current line (so far) a target pressing press.
func (c *cardBuilder) hitLine(width int, press string) {
	c.hits = append(c.hits, cardHit{x0: 0, x1: width, y: len(c.lines), key: press})
}

// gap appends n spaces.
func (c *cardBuilder) gap(n int) { c.text(strings.Repeat(" ", n)) }

// nl ends the current line.
func (c *cardBuilder) nl() {
	c.lines = append(c.lines, c.cur.String())
	c.cur.Reset()
	c.x = 0
}

// line appends a whole line.
func (c *cardBuilder) line(s string) {
	c.text(s)
	c.nl()
}

// card is a finished card: its box and where its content starts on
// screen.
type card struct {
	box    string
	ox, oy int // screen position of content (0,0)
	hits   []cardHit
}

const (
	cardPadX = 2
	cardPadY = 1
)

// buildCard boxes the lines at width (content width) and centres the box
// in the body area.
func (m Model) buildCard(c *cardBuilder, width int) card {
	if c.cur.Len() > 0 {
		c.nl()
	}
	lines := make([]string, len(c.lines))
	for i, l := range c.lines {
		lines[i] = padVisual(cutVisual(l, 0, width), width)
	}
	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(theme.Current.Accent).
		Background(theme.Current.Background).
		Padding(cardPadY, cardPadX).
		Render(strings.Join(lines, "\n"))
	bw, bh := lipgloss.Width(box), lipgloss.Height(box)
	bx := max(0, (m.width-bw)/2)
	by := max(0, (m.contentHeight()-bh)/2)
	return card{box: box, ox: bx + 1 + cardPadX, oy: by + 1 + cardPadY, hits: c.hits}
}

// cardWidth is a card's content width for the terminal (capped).
func (m Model) cardWidth(want int) int {
	return max(20, min(want, m.width-4-2*cardPadX))
}

// over draws the card over base.
func (cd card) over(m Model, base string) string {
	return overlayBox(base, cd.box, cd.ox-1-cardPadX, cd.oy-1-cardPadY, m.width)
}

// alone draws the card on the dimmed body background.
func (cd card) alone(m Model) string {
	bg := lipgloss.NewStyle().Background(theme.Current.OverlayBg)
	blank := make([]string, m.contentHeight())
	for i := range blank {
		blank[i] = bg.Render(strings.Repeat(" ", max(0, m.width)))
	}
	return cd.over(m, strings.Join(blank, "\n"))
}

// keyAt is the key of the button under screen (x, y), "" when none.
func (cd card) keyAt(x, y int) string {
	cx, cy := x-cd.ox, y-cd.oy
	for _, h := range cd.hits {
		if cy == h.y && cx >= h.x0 && cx < h.x1 {
			return h.key
		}
	}
	return ""
}

// contains reports whether (x, y) is inside the card's box.
func (cd card) contains(x, y int) bool {
	bw, bh := lipgloss.Width(cd.box), lipgloss.Height(cd.box)
	bx, by := cd.ox-1-cardPadX, cd.oy-1-cardPadY
	return x >= bx && x < bx+bw && y >= by && y < by+bh
}

// spinner is the alive motion glyph for work in progress (braille
// breathing; a static ellipsis with reduced motion).
func (m Model) spinner() string {
	g := m.mo.breatheGlyph()
	if g == "" {
		g = "…"
	}
	return lipgloss.NewStyle().Foreground(theme.Current.Info).Render(g)
}

// progressBar is a width-cell bar for done/total.
func progressBar(done, total, width int) string {
	if width < 1 {
		return ""
	}
	fill := 0
	if total > 0 {
		fill = min(width, done*width/total)
	}
	t := theme.Current
	return lipgloss.NewStyle().Foreground(t.Accent).Render(strings.Repeat("█", fill)) +
		lipgloss.NewStyle().Foreground(t.Separator).Render(strings.Repeat("░", width-fill))
}

// pressCardKey turns a button key into the key message the card's
// update handles.
func pressCardKey(k string) tea.KeyMsg { return keyFor(k) }
