// Package kanban reads and edits Obsidian Kanban boards (SPEC-PARTE2
// §5): a note whose frontmatter has `kanban-plugin: board` (or `basic`)
// is a board, each `## Heading` is a column and each `- [ ] text` /
// `- [x] text` list item under it is a card; indented lines below a
// card belong to it.
//
// Every operation returns minimal byte-range edits of the markdown, so
// unknown content (the `%% kanban:settings … %%` block, notes between
// cards, the frontmatter) is preserved byte for byte, and the edits map
// to ranged CRDT operations. A move is one delete plus one insert.
package kanban

import (
	"regexp"
	"sort"
	"strings"
)

// Card is one list item.
type Card struct {
	Done  bool
	Title string   // first-line text after the checkbox
	Body  []string // nested lines (as written)
	Start int      // byte offset of the line start
	End   int      // byte offset just past the last line (incl. its newline)
	// TextStart/TextEnd delimit Title in the source.
	TextStart, TextEnd int
	Box                int // byte offset of the checkbox mark (' ' or 'x')
}

// Column is one `## Heading` section.
type Column struct {
	Title       string
	HeadStart   int // start of the heading line
	HeadEnd     int // just past the heading line (incl. newline)
	End         int // end of the column region
	Cards       []Card
	lastContent int // end of the last non-blank line in the region
}

// Board is a parsed board.
type Board struct {
	Columns []Column
	// Settings is the byte offset where the `%% kanban:settings` block
	// starts (len(text) when absent).
	Settings int
	Text     string
}

// Edit replaces Text[Start:End] with New.
type Edit struct {
	Start, End int
	New        string
}

var (
	reFront   = regexp.MustCompile(`(?m)^kanban-plugin:\s*["']?(board|basic)["']?\s*$`)
	reCard    = regexp.MustCompile(`^(\s*)[-*+] \[([ xX])\] ?(.*)$`)
	reHeading = regexp.MustCompile(`^##\s+(.*?)\s*$`)
)

// IsBoard reports whether text is a kanban note (frontmatter flag).
func IsBoard(text string) bool {
	fm, _ := frontmatter(text)
	return fm != "" && reFront.MatchString(fm)
}

func frontmatter(text string) (string, int) {
	if !strings.HasPrefix(text, "---\n") && !strings.HasPrefix(text, "---\r\n") {
		return "", 0
	}
	i := strings.Index(text, "\n") + 1
	for i < len(text) {
		j := strings.Index(text[i:], "\n")
		line := text[i:]
		next := len(text)
		if j >= 0 {
			line = text[i : i+j]
			next = i + j + 1
		}
		if strings.TrimRight(line, "\r") == "---" {
			return text[:i], next
		}
		i = next
	}
	return "", 0
}

type lineInfo struct {
	start, end int // end excludes the newline
	next       int // start of the next line
	text       string
}

func lines(text string, from int) []lineInfo {
	var out []lineInfo
	i := from
	for i < len(text) {
		j := strings.IndexByte(text[i:], '\n')
		if j < 0 {
			out = append(out, lineInfo{i, len(text), len(text), text[i:]})
			break
		}
		out = append(out, lineInfo{i, i + j, i + j + 1, strings.TrimRight(text[i:i+j], "\r")})
		i += j + 1
	}
	return out
}

// Parse reads a board (works on any text; non-boards just have no
// frontmatter flag).
func Parse(text string) *Board {
	b := &Board{Text: text, Settings: len(text)}
	_, body := frontmatter(text)
	ls := lines(text, body)
	inFence := false
	var col *Column
	var card *Card
	closeCard := func() {
		if card != nil && col != nil {
			col.Cards = append(col.Cards, *card)
		}
		card = nil
	}
	closeCol := func(end int) {
		closeCard()
		if col != nil {
			col.End = end
			b.Columns = append(b.Columns, *col)
		}
		col = nil
	}
	for _, l := range ls {
		trim := strings.TrimSpace(l.text)
		if strings.HasPrefix(trim, "%% kanban:settings") {
			closeCol(l.start)
			b.Settings = l.start
			break
		}
		if strings.HasPrefix(trim, "```") || strings.HasPrefix(trim, "~~~") {
			inFence = !inFence
		}
		if !inFence {
			if m := reHeading.FindStringSubmatch(l.text); m != nil {
				closeCol(l.start)
				col = &Column{Title: m[1], HeadStart: l.start, HeadEnd: l.next, lastContent: l.next}
				continue
			}
		}
		if col == nil {
			continue
		}
		if trim != "" {
			col.lastContent = l.next
		}
		if !inFence {
			if m := reCard.FindStringSubmatchIndex(l.text); m != nil && m[3]-m[2] == 0 {
				closeCard()
				off := l.start
				card = &Card{Done: l.text[m[4]] != ' ', Title: l.text[m[6]:m[7]], Start: l.start, End: l.next,
					TextStart: off + m[6], TextEnd: off + m[7], Box: off + m[4]}
				continue
			}
		}
		if card != nil && trim != "" && (strings.HasPrefix(l.text, " ") || strings.HasPrefix(l.text, "\t")) {
			card.Body = append(card.Body, l.text)
			card.End = l.next
			continue
		}
		if trim != "" {
			closeCard()
		}
	}
	closeCol(b.Settings)
	return b
}

// Apply applies non-overlapping edits.
func Apply(text string, edits []Edit) string {
	es := append([]Edit(nil), edits...)
	sort.SliceStable(es, func(i, j int) bool { return es[i].Start > es[j].Start })
	for _, e := range es {
		text = text[:e.Start] + e.New + text[e.End:]
	}
	return text
}

func (b *Board) card(c, i int) (*Card, bool) {
	if c < 0 || c >= len(b.Columns) || i < 0 || i >= len(b.Columns[c].Cards) {
		return nil, false
	}
	return &b.Columns[c].Cards[i], true
}

// Toggle flips a card's done state (one-byte edit).
func (b *Board) Toggle(c, i int) []Edit {
	cd, ok := b.card(c, i)
	if !ok {
		return nil
	}
	mark := "x"
	if cd.Done {
		mark = " "
	}
	return []Edit{{cd.Box, cd.Box + 1, mark}}
}

// SetText replaces a card's first-line text.
func (b *Board) SetText(c, i int, text string) []Edit {
	cd, ok := b.card(c, i)
	if !ok {
		return nil
	}
	text = strings.ReplaceAll(text, "\n", " ")
	if text == cd.Title {
		return nil
	}
	return []Edit{{cd.TextStart, cd.TextEnd, text}}
}

// Delete removes a card (with its nested lines).
func (b *Board) Delete(c, i int) []Edit {
	cd, ok := b.card(c, i)
	if !ok {
		return nil
	}
	return []Edit{{cd.Start, cd.End, ""}}
}

// cardSource is a card's full source, ending in a newline.
func (b *Board) cardSource(cd *Card) string {
	s := b.Text[cd.Start:cd.End]
	if !strings.HasSuffix(s, "\n") {
		s += "\n"
	}
	return s
}

// insertAt is the offset where a card lands at index i of column c, and
// a prefix needed to start a new line there.
func (b *Board) insertAt(c, i int) (int, string) {
	col := &b.Columns[c]
	if i < len(col.Cards) {
		return col.Cards[i].Start, ""
	}
	pos := col.HeadEnd
	if n := len(col.Cards); n > 0 {
		pos = col.Cards[n-1].End
	} else {
		// after the heading and one blank line, when present
		ls := lines(b.Text, col.HeadEnd)
		if len(ls) > 0 && strings.TrimSpace(ls[0].text) == "" && ls[0].start < col.End {
			pos = ls[0].next
		}
		// Indented lines with no card above them would become the new
		// card's body; land after them (same fix as the web planner).
		for _, l := range ls {
			if l.start >= col.End {
				break
			}
			if l.next <= pos || strings.TrimSpace(l.text) == "" {
				continue
			}
			if l.text[0] != ' ' && l.text[0] != '\t' {
				break
			}
			pos = l.next
		}
	}
	prefix := ""
	if pos > 0 && b.Text[pos-1] != '\n' {
		prefix = "\n"
	}
	return pos, prefix
}

// Insert adds a new card at index i of column c.
func (b *Board) Insert(c, i int, text string) []Edit {
	if c < 0 || c >= len(b.Columns) {
		return nil
	}
	i = max(0, min(i, len(b.Columns[c].Cards)))
	pos, pre := b.insertAt(c, i)
	return []Edit{{pos, pos, pre + "- [ ] " + strings.ReplaceAll(text, "\n", " ") + "\n"}}
}

// Move moves card (c, i) to index ti of column tc (ti is the index in the
// target column after removal). It is one delete plus one insert.
func (b *Board) Move(c, i, tc, ti int) []Edit {
	cd, ok := b.card(c, i)
	if !ok || tc < 0 || tc >= len(b.Columns) {
		return nil
	}
	if c == tc {
		if ti == i {
			return nil
		}
		if ti > i {
			ti++ // index in the column before removal
		}
	}
	ti = max(0, min(ti, len(b.Columns[tc].Cards)))
	pos, pre := b.insertAt(tc, ti)
	src := b.cardSource(cd)
	if pos == cd.Start || pos == cd.End && c == tc && ti == i+1 {
		return nil
	}
	return []Edit{{cd.Start, cd.End, ""}, {pos, pos, pre + src}}
}

// AddColumn appends a column before the settings block.
func (b *Board) AddColumn(title string) []Edit {
	pos := b.Settings
	pre := ""
	if pos > 0 && b.Text[pos-1] != '\n' {
		pre = "\n"
	}
	if pos > 1 && b.Text[pos-1] == '\n' && b.Text[pos-2] != '\n' {
		pre = "\n"
	} else if pos > 0 && b.Text[pos-1] != '\n' {
		pre = "\n\n"
	}
	return []Edit{{pos, pos, pre + "## " + strings.TrimSpace(title) + "\n\n"}}
}
