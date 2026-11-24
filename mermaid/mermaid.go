// Package mermaid gives the terminal reader a text rendering of
// ```mermaid blocks. Terminals cannot draw the SVG mermaid.js makes, so
// lumi names the diagram kind and, for the two most common shapes —
// simple flowcharts and sequence diagrams — draws it with box-drawing
// characters. Anything outside that subset is reported as unparsed and
// the host shows the source instead (never a wrong picture).
//
// Pure Go, no dependencies beyond go-runewidth; every input is bounded
// (node, edge, participant and message caps) so a hostile note cannot
// make the reader slow.
package mermaid

import (
	"regexp"
	"strings"
)

// Limits: past these the diagram falls back to its source.
const (
	maxNodes        = 40
	maxEdges        = 80
	maxParticipants = 10
	maxMessages     = 80
	maxLabel        = 28 // columns per node / participant label
	maxMsgLabel     = 40 // columns per edge / message label
	maxSourceBytes  = 16 << 10
)

// kinds maps a diagram keyword to its display name.
var kinds = map[string]string{
	"graph":              "flowchart",
	"flowchart":          "flowchart",
	"flowchart-elk":      "flowchart",
	"sequencediagram":    "sequence",
	"classdiagram":       "class",
	"classdiagram-v2":    "class",
	"statediagram":       "state",
	"statediagram-v2":    "state",
	"erdiagram":          "er",
	"gantt":              "gantt",
	"pie":                "pie",
	"mindmap":            "mindmap",
	"journey":            "journey",
	"gitgraph":           "gitgraph",
	"timeline":           "timeline",
	"quadrantchart":      "quadrant",
	"requirementdiagram": "requirement",
	"c4context":          "c4",
	"c4container":        "c4",
	"c4component":        "c4",
	"c4dynamic":          "c4",
	"c4deployment":       "c4",
	"sankey-beta":        "sankey",
	"sankey":             "sankey",
	"xychart-beta":       "xychart",
	"xychart":            "xychart",
	"block-beta":         "block",
	"block":              "block",
	"packet-beta":        "packet",
	"packet":             "packet",
	"architecture-beta":  "architecture",
	"architecture":       "architecture",
	"kanban":             "kanban",
	"radar-beta":         "radar",
	"treemap-beta":       "treemap",
	"zenuml":             "zenuml",
}

// statements returns the diagram's meaningful lines: comments (%%),
// directives (%%{…}%%), a leading --- frontmatter block and blank lines
// are dropped; surrounding spaces are trimmed.
func statements(src string) []string {
	lines := strings.Split(strings.ReplaceAll(src, "\r\n", "\n"), "\n")
	var out []string
	i := 0
	// frontmatter: only when --- is the first non-blank line
	for i < len(lines) && strings.TrimSpace(lines[i]) == "" {
		i++
	}
	if i < len(lines) && strings.TrimSpace(lines[i]) == "---" {
		j := i + 1
		for j < len(lines) && strings.TrimSpace(lines[j]) != "---" {
			j++
		}
		if j < len(lines) {
			i = j + 1
		}
	}
	for ; i < len(lines); i++ {
		t := strings.TrimSpace(lines[i])
		if t == "" || strings.HasPrefix(t, "%%") {
			continue
		}
		out = append(out, t)
	}
	return out
}

// Kind is the diagram kind of a mermaid source ("flowchart",
// "sequence", "gantt" …), "" when the first meaningful line names none.
func Kind(src string) string {
	st := statements(src)
	if len(st) == 0 {
		return ""
	}
	return kindOf(st[0])
}

var reKeyword = regexp.MustCompile(`^[A-Za-z0-9-]+`)

func kindOf(header string) string {
	return kinds[strings.ToLower(reKeyword.FindString(header))]
}

// Render draws src in at most maxWidth columns. ok=false means the
// source is outside the supported subset (or too large / too wide):
// show the source instead.
func Render(src string, maxWidth int) (lines []Line, ok bool) {
	if len(src) > maxSourceBytes || maxWidth < 8 {
		return nil, false
	}
	st := statements(src)
	if len(st) == 0 {
		return nil, false
	}
	switch kindOf(st[0]) {
	case "flowchart":
		f, err := parseFlowchart(st)
		if err != nil {
			return nil, false
		}
		lines = f.render()
	case "sequence":
		s, err := parseSequence(st)
		if err != nil {
			return nil, false
		}
		lines = s.render()
	default:
		return nil, false
	}
	for _, l := range lines {
		if l.Width() > maxWidth {
			return nil, false
		}
	}
	return lines, len(lines) > 0
}

var reBreak = regexp.MustCompile(`(?i)<br\s*/?>`)

// cleanLabel turns a mermaid label into one display line.
func cleanLabel(s string, w int) string {
	s = strings.TrimSpace(s)
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		s = s[1 : len(s)-1]
	}
	if len(s) >= 2 && s[0] == '`' && s[len(s)-1] == '`' { // markdown string
		s = s[1 : len(s)-1]
	}
	s = reBreak.ReplaceAllString(s, " ")
	s = strings.NewReplacer("#quot;", `"`, "#amp;", "&", "#lt;", "<", "#gt;", ">", "#35;", "#").Replace(s)
	s = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return ' '
		}
		return r
	}, s)
	return clip(strings.Join(strings.Fields(s), " "), w)
}
