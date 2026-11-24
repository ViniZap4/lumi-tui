package mermaid

import (
	"fmt"
	"regexp"
	"strings"
)

type participant struct {
	id, label string
	actor     bool
}

type seqKind int

const (
	seqMsg seqKind = iota
	seqNote
	seqBlock // loop / alt / opt … header
	seqElse  // else / and / option
	seqEnd
)

type seqItem struct {
	kind     seqKind
	from, to int // participants (note: span)
	text     string
	style    int
	head     string // "▶" style arrowhead ("" none)
	side     int    // note: -1 left of, 0 over, 1 right of
}

type sequence struct {
	parts      []participant
	index      map[string]int
	items      []seqItem
	autonumber bool
}

var (
	reParticipant = regexp.MustCompile(`^(?:create\s+)?(participant|actor)\s+(.+?)(?:\s+as\s+(.+))?$`)
	reMessage     = regexp.MustCompile(`^(.+?)\s*(-->>|->>|-->|->|--x|-x|--\)|-\))\s*([+-]?)\s*(.+?)\s*:\s*(.*)$`)
	reNote        = regexp.MustCompile(`(?i)^note\s+(left of|right of|over)\s+([^:]+?)\s*:\s*(.*)$`)
	reBlock       = regexp.MustCompile(`^(loop|alt|opt|par|critical|break)\b\s*(.*)$`)
	reElse        = regexp.MustCompile(`^(else|and|option)\b\s*(.*)$`)
	reGroup       = regexp.MustCompile(`^(rect|box)\b`)
	reSeqIgnore   = regexp.MustCompile(`^(activate|deactivate|destroy|title|accTitle|accDescr|links?|properties|details)\b`)
	reSeqID       = regexp.MustCompile(`^[^\s:;,<>]+$`)
)

func parseSequence(st []string) (*sequence, error) {
	s := &sequence{index: map[string]int{}}
	if rest := strings.TrimSpace(st[0][len(reKeyword.FindString(st[0])):]); rest != "" {
		return nil, errUnsupported
	}
	var stack []bool // true: a block lumi draws (false: rect/box)
	for _, line := range st[1:] {
		line = strings.TrimSuffix(line, ";")
		switch {
		case line == "autonumber" || strings.HasPrefix(line, "autonumber "):
			s.autonumber = true
		case reSeqIgnore.MatchString(line):
		case reGroup.MatchString(line):
			stack = append(stack, false)
		case line == "end":
			if len(stack) == 0 {
				return nil, errUnsupported
			}
			if stack[len(stack)-1] {
				s.items = append(s.items, seqItem{kind: seqEnd})
			}
			stack = stack[:len(stack)-1]
		case reBlock.MatchString(line):
			m := reBlock.FindStringSubmatch(line)
			stack = append(stack, true)
			s.items = append(s.items, seqItem{kind: seqBlock, text: cleanLabel(m[1]+" "+m[2], maxMsgLabel)})
		case reElse.MatchString(line):
			m := reElse.FindStringSubmatch(line)
			s.items = append(s.items, seqItem{kind: seqElse, text: cleanLabel(m[1]+" "+m[2], maxMsgLabel)})
		case reParticipant.MatchString(line):
			m := reParticipant.FindStringSubmatch(line)
			i, err := s.part(m[2])
			if err != nil {
				return nil, err
			}
			if m[3] != "" {
				s.parts[i].label = cleanLabel(m[3], maxLabel)
			}
			s.parts[i].actor = m[1] == "actor"
		case reNote.MatchString(line):
			m := reNote.FindStringSubmatch(line)
			it := seqItem{kind: seqNote, text: cleanLabel(m[3], maxMsgLabel)}
			ids := strings.Split(m[2], ",")
			if len(ids) > 2 {
				return nil, errUnsupported
			}
			a, err := s.part(ids[0])
			if err != nil {
				return nil, err
			}
			b := a
			if len(ids) == 2 {
				if b, err = s.part(ids[1]); err != nil {
					return nil, err
				}
			}
			it.from, it.to = min(a, b), max(a, b)
			switch strings.ToLower(m[1]) {
			case "left of":
				it.side = -1
			case "right of":
				it.side = 1
			}
			if it.side != 0 && len(ids) == 2 {
				return nil, errUnsupported
			}
			s.items = append(s.items, it)
		case reMessage.MatchString(line):
			m := reMessage.FindStringSubmatch(line)
			a, err := s.part(m[1])
			if err != nil {
				return nil, err
			}
			b, err := s.part(m[4])
			if err != nil {
				return nil, err
			}
			it := seqItem{kind: seqMsg, from: a, to: b, text: cleanLabel(m[5], maxMsgLabel)}
			if strings.HasPrefix(m[2], "--") {
				it.style = dotted
			}
			switch strings.TrimLeft(m[2], "-") {
			case ">>":
				it.head = "▶"
			case "x":
				it.head = "✕"
			case ")":
				it.head = "▷"
			}
			s.items = append(s.items, it)
		default:
			return nil, errUnsupported
		}
		if len(s.parts) > maxParticipants || len(s.items) > maxMessages {
			return nil, errUnsupported
		}
	}
	if len(stack) != 0 || len(s.parts) == 0 {
		return nil, errUnsupported
	}
	return s, nil
}

// part returns participant id's index, declaring it on first use.
func (s *sequence) part(id string) (int, error) {
	id = strings.TrimSpace(id)
	if !reSeqID.MatchString(id) {
		return 0, errUnsupported
	}
	if i, ok := s.index[id]; ok {
		return i, nil
	}
	s.index[id] = len(s.parts)
	s.parts = append(s.parts, participant{id: id, label: cleanLabel(id, maxLabel)})
	return len(s.parts) - 1, nil
}

// --- drawing -------------------------------------------------------------

func (s *sequence) msgText(n int, it seqItem) string {
	if s.autonumber {
		return fmt.Sprintf("%d. %s", n, it.text)
	}
	return it.text
}

func (s *sequence) render() []Line {
	np := len(s.parts)
	w := make([]int, np)
	for i, p := range s.parts {
		w[i] = boxWidth(p.label)
	}
	// need[i][j]: minimum distance between lifelines i < j
	need := make([][]int, np)
	for i := range need {
		need[i] = make([]int, np)
	}
	for i := 0; i+1 < np; i++ {
		need[i][i+1] = (w[i]+1)/2 + (w[i+1]+1)/2 + 2
	}
	left, right := 0, 0 // room needed left of the first / right of the last lifeline
	n := 0
	for _, it := range s.items {
		switch it.kind {
		case seqMsg:
			n++
			tw := textWidth(s.msgText(n, it))
			a, b := min(it.from, it.to), max(it.from, it.to)
			if a == b {
				if a+1 < np {
					need[a][a+1] = max(need[a][a+1], tw+7)
				} else {
					right = max(right, tw+6)
				}
				continue
			}
			need[a][b] = max(need[a][b], tw+4)
		case seqNote:
			tw := textWidth(it.text) + 4
			switch {
			case it.side < 0 && it.from == 0:
				left = max(left, tw+1)
			case it.side < 0:
				need[it.from-1][it.from] = max(need[it.from-1][it.from], tw+2)
			case it.side > 0 && it.from == np-1:
				right = max(right, tw+1)
			case it.side > 0:
				need[it.from][it.from+1] = max(need[it.from][it.from+1], tw+2)
			case it.from == it.to:
				left = max(left, (tw+1)/2)
				right = max(right, (tw+1)/2)
			default:
				need[it.from][it.to] = max(need[it.from][it.to], tw-4)
			}
		case seqBlock, seqElse:
			right = max(right, textWidth(it.text)+6)
		}
	}
	cx := make([]int, np)
	cx[0] = max((w[0]+1)/2, left)
	for i := 1; i < np; i++ {
		cx[i] = cx[i-1] + need[i-1][i]
	}
	for span := 2; span < np; span++ {
		for i := 0; i+span < np; i++ {
			j := i + span
			if d := need[i][j] - (cx[j] - cx[i]); d > 0 {
				for k := j; k < np; k++ {
					cx[k] += d
				}
			}
		}
	}
	width := cx[np-1] + max((w[np-1]+1)/2, right)

	c := &canvas{}
	for i, p := range s.parts {
		x := cx[i] - w[i]/2
		drawBox(c, x, 0, w[i], fnode{label: p.label, shape: map[bool]shape{true: shapeRound}[p.actor]})
	}
	y := 3
	row := func() int { y++; return y - 1 }
	row() // gap under the headers
	n = 0
	for _, it := range s.items {
		switch it.kind {
		case seqMsg:
			n++
			text := s.msgText(n, it)
			a, b := cx[it.from], cx[it.to]
			if a == b {
				r := row()
				c.hline(a, a+3, r, it.style, Edge)
				c.vline(a+3, r, r+1, it.style, Edge)
				r2 := row()
				c.hline(a+1, a+3, r2, it.style, Edge)
				if it.head != "" {
					c.put(a+1, r2, "◀", Arrow)
				}
				c.put(a+5, r, text, Label)
				continue
			}
			lr := row()
			tw := textWidth(text)
			c.put(min(a, b)+(abs(b-a)-tw)/2, lr, text, Label)
			r := row()
			if b > a {
				c.hline(a, b-1, r, it.style, Edge)
				if it.head != "" {
					c.put(b-1, r, it.head, Arrow)
				} else {
					c.hline(a, b, r, it.style, Edge)
				}
			} else {
				c.hline(b+1, a, r, it.style, Edge)
				if it.head != "" {
					c.put(b+1, r, mirror(it.head), Arrow)
				} else {
					c.hline(b, a, r, it.style, Edge)
				}
			}
		case seqNote:
			tw := textWidth(it.text) + 4
			var x int
			switch {
			case it.side < 0:
				x = cx[it.from] - 1 - tw
			case it.side > 0:
				x = cx[it.from] + 2
			default:
				mid := (cx[it.from] + cx[it.to]) / 2
				tw = max(tw, cx[it.to]-cx[it.from]+5)
				x = mid - tw/2
			}
			x = max(0, x)
			r := row()
			row()
			row()
			c.put(x, r, "┌"+strings.Repeat("─", tw-2)+"┐", Note)
			lead := (tw - 2 - textWidth(it.text)) / 2
			c.put(x, r+1, "│"+padRight(strings.Repeat(" ", lead)+it.text, tw-2)+"│", Note)
			c.put(x, r+2, "└"+strings.Repeat("─", tw-2)+"┘", Note)
		case seqBlock, seqElse, seqEnd:
			r := row()
			for x := 0; x < width; x++ {
				if !isLifeline(cx, x) {
					c.put(x, r, "┄", Note)
				}
			}
			if it.text != "" {
				c.put(1, r, " "+it.text+" ", Note)
			}
		}
	}
	row() // the lifelines run one row past the last message
	// lifelines under everything else (connectors merge, text wins)
	for i := range s.parts {
		c.vline(cx[i], 2, y-1, solid, Edge)
	}
	return c.lines()
}

func isLifeline(cx []int, x int) bool {
	for _, c := range cx {
		if c == x {
			return true
		}
	}
	return false
}

func mirror(head string) string {
	switch head {
	case "▶":
		return "◀"
	case "▷":
		return "◁"
	}
	return head
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

func padRight(s string, w int) string {
	if d := w - textWidth(s); d > 0 {
		return s + strings.Repeat(" ", d)
	}
	return s
}
