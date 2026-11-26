package kanban

import (
	"sort"
	"strings"
	"testing"
)

// Fuzz targets for the board parser and its edit planners (Phase 3
// quality harness, docs/phase3/research/09 Q6). Run locally with
//
//	go test ./kanban -run '^$' -fuzz FuzzParse -fuzztime 2m
//
// The nightly CI job runs each target for longer; any crasher lands in
// testdata/fuzz/<Target>/ and becomes a permanent regression input.

func seedBoards(f *testing.F) {
	f.Add(sample)
	f.Add("")
	f.Add("---\nkanban-plugin: board\n---\n\n## A\n\n- [ ] one\n")
	f.Add("## A\r\n- [x] crlf\r\n\t body\r\n## B\r\n")
	f.Add("## A\n```\n- [ ] fenced\n```\n- [ ] real\n%% kanban:settings\n%%")
	f.Add("## A\n- [ ] no newline at end")
	f.Add("## Só\n- [ ] ação 😀\n  - [ ] nested is body\n")
	f.Add("---\n---\n## \n- [ ]\n")
}

// checkBoard asserts the parser's structural invariants: every offset is
// in range and ordered, and each recorded span really holds what the
// struct says it holds (offsets are what the edit planners trust).
func checkBoard(t *testing.T, text string, b *Board) {
	t.Helper()
	n := len(text)
	if b.Settings < 0 || b.Settings > n {
		t.Fatalf("settings offset %d out of [0,%d]", b.Settings, n)
	}
	prevEnd := 0
	for ci, c := range b.Columns {
		if !(prevEnd <= c.HeadStart && c.HeadStart < c.HeadEnd && c.HeadEnd <= c.End && c.End <= n) {
			t.Fatalf("column %d spans head [%d,%d) end %d after %d (len %d)", ci, c.HeadStart, c.HeadEnd, c.End, prevEnd, n)
		}
		if c.End > b.Settings {
			t.Fatalf("column %d ends at %d past settings %d", ci, c.End, b.Settings)
		}
		cardPrev := c.HeadEnd
		for ki, k := range c.Cards {
			if !(cardPrev <= k.Start && k.Start <= k.Box && k.Box < k.TextStart && k.TextStart <= k.TextEnd && k.TextEnd <= k.End && k.End <= c.End) {
				t.Fatalf("column %d card %d offsets out of order: %+v (prev %d, colEnd %d)", ci, ki, k, cardPrev, c.End)
			}
			if got := text[k.TextStart:k.TextEnd]; got != k.Title {
				t.Fatalf("card title %q but span holds %q", k.Title, got)
			}
			switch text[k.Box] {
			case ' ', 'x', 'X':
			default:
				t.Fatalf("box offset %d holds %q", k.Box, text[k.Box])
			}
			if k.Done != (text[k.Box] != ' ') {
				t.Fatalf("done %v but box %q", k.Done, text[k.Box])
			}
			cardPrev = k.End
		}
		prevEnd = c.End
	}
}

func FuzzParse(f *testing.F) {
	seedBoards(f)
	f.Fuzz(func(t *testing.T, text string) {
		_ = IsBoard(text)
		checkBoard(t, text, Parse(text))
	})
}

// checkEdits asserts that planned edits are in range and do not overlap
// (Apply's contract), then applies them.
func checkEdits(t *testing.T, text string, es []Edit) string {
	t.Helper()
	s := append([]Edit(nil), es...)
	sort.SliceStable(s, func(i, j int) bool { return s[i].Start < s[j].Start })
	prev := 0
	for _, e := range s {
		if e.Start < prev || e.Start > e.End || e.End > len(text) {
			t.Fatalf("edit %+v out of range or overlapping (prev end %d, len %d)", e, prev, len(text))
		}
		prev = e.End
	}
	return Apply(text, es)
}

func titles(b *Board) []string {
	var out []string
	for _, c := range b.Columns {
		for _, k := range c.Cards {
			out = append(out, k.Title)
		}
	}
	sort.Strings(out)
	return out
}

func cardCount(b *Board) int {
	n := 0
	for _, c := range b.Columns {
		n += len(c.Cards)
	}
	return n
}

// FuzzEdits drives every planner with fuzzed indices and checks the
// minimal-edit contract: edits are in range, toggle is one byte, and a
// move or a toggle never changes which cards exist.
func FuzzEdits(f *testing.F) {
	f.Add(sample, uint8(0), 0, 1, 1, 0, "novo")
	f.Add(sample, uint8(4), 0, 1, 1, 0, "")
	f.Add(sample, uint8(4), 0, 0, 0, 2, "")
	f.Add("## A\n- [ ] a\n- [ ] b\n## B\n", uint8(4), 0, 0, 1, 0, "")
	f.Add("## A\n- [ ] a", uint8(3), 0, 9, 0, 0, "x\ny")
	f.Fuzz(func(t *testing.T, text string, op uint8, c, i, tc, ti int, s string) {
		b := Parse(text)
		var es []Edit
		switch op % 6 {
		case 0:
			es = b.Toggle(c, i)
			if len(es) == 1 && (es[0].End-es[0].Start != 1 || len(es[0].New) != 1) {
				t.Fatalf("toggle is not a one-byte edit: %+v", es)
			}
		case 1:
			es = b.SetText(c, i, s)
		case 2:
			es = b.Delete(c, i)
		case 3:
			es = b.Insert(c, i, s)
		case 4:
			es = b.Move(c, i, tc, ti)
			if len(es) != 0 && len(es) != 2 {
				t.Fatalf("move is not one delete plus one insert: %+v", es)
			}
		case 5:
			es = b.AddColumn(s)
		}
		out := checkEdits(t, text, es)
		switch op % 6 {
		case 0, 4:
			// A toggle or a move must keep the same set of cards. Only
			// meaningful when the board text is plain LF and the moved
			// card's source is well-formed; the parser itself is the
			// oracle, so skip inputs it cannot re-read identically.
			if strings.ContainsRune(text, '\r') || strings.Contains(text, "```") || strings.Contains(text, "~~~") {
				return
			}
			after := Parse(out)
			if cardCount(after) != cardCount(b) {
				t.Fatalf("op %d changed the card count %d -> %d\nbefore:\n%q\nafter:\n%q", op%6, cardCount(b), cardCount(after), text, out)
			}
			if strings.Join(titles(after), "\x00") != strings.Join(titles(b), "\x00") {
				t.Fatalf("op %d changed the cards\nbefore %q\nafter  %q", op%6, titles(b), titles(after))
			}
		}
	})
}
