// Package canvas reads JSON Canvas 1.0 files (jsoncanvas.org, as used by
// Obsidian) and builds the TUI's read-only outline (SPEC-PARTE2 §6):
// groups → the nodes inside them, then the edges. The terminal never
// draws the graphical board.
package canvas

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// Node is one canvas node.
type Node struct {
	ID      string `json:"id"`
	Type    string `json:"type"` // text | file | link | group
	X       int    `json:"x"`
	Y       int    `json:"y"`
	Width   int    `json:"width"`
	Height  int    `json:"height"`
	Color   string `json:"color,omitempty"`
	Text    string `json:"text,omitempty"`
	File    string `json:"file,omitempty"`
	Subpath string `json:"subpath,omitempty"`
	URL     string `json:"url,omitempty"`
	Label   string `json:"label,omitempty"`
}

// Edge connects two nodes.
type Edge struct {
	ID       string `json:"id"`
	FromNode string `json:"fromNode"`
	FromSide string `json:"fromSide,omitempty"`
	ToNode   string `json:"toNode"`
	ToSide   string `json:"toSide,omitempty"`
	FromEnd  string `json:"fromEnd,omitempty"`
	ToEnd    string `json:"toEnd,omitempty"`
	Label    string `json:"label,omitempty"`
	Color    string `json:"color,omitempty"`
}

// Canvas is a parsed file.
type Canvas struct {
	Nodes []Node `json:"nodes"`
	Edges []Edge `json:"edges"`
}

// MaxBytes caps what the outline parses (a canvas is small JSON).
const MaxBytes = 8 << 20

// Parse decodes a canvas. An empty file is an empty canvas.
func Parse(data []byte) (*Canvas, error) {
	if len(data) > MaxBytes {
		return nil, fmt.Errorf("canvas larger than %d MB", MaxBytes>>20)
	}
	c := &Canvas{}
	if strings.TrimSpace(string(data)) == "" {
		return c, nil
	}
	if err := json.Unmarshal(data, c); err != nil {
		// Concurrent first cards: objects in an array with no comma
		// between them. Read them as if the comma were there (a read-side
		// view only; nothing is written back).
		healed, ok := HealMissingCommas(string(data))
		if !ok {
			return nil, fmt.Errorf("invalid JSON Canvas: %w", err)
		}
		c = &Canvas{}
		if json.Unmarshal([]byte(healed), c) != nil {
			return nil, fmt.Errorf("invalid JSON Canvas: %w", err)
		}
	}
	return c, nil
}

// EmptyJSON is a new canvas's exact bytes, the same on every client (no
// trailing newline).
const EmptyJSON = "{\n\t\"nodes\":[],\n\t\"edges\":[]\n}"

// HealMissingCommas is the one corruption tolerated on read: two array
// elements that are objects with NO comma between them (`}` whitespace
// `{` inside an array, outside strings). Two replicas adding the first
// card to an empty multi-line canvas at the same time merge exactly like
// that. It returns the text with those commas added, or ok=false when
// there is nothing to heal. Nothing broader is accepted: no top-level
// concatenation, no other missing commas. Port of the web's
// healMissingCommas (lib/canvas/canvas-recover.ts).
func HealMissingCommas(s string) (string, bool) {
	var stack []byte
	var at []int
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '"' {
			// skip a string, escapes included
			for i++; i < len(s); i++ {
				if s[i] == '\\' {
					i++
				} else if s[i] == '"' {
					break
				}
			}
			continue
		}
		switch c {
		case '{', '[':
			stack = append(stack, c)
		case '}', ']':
			if len(stack) > 0 {
				stack = stack[:len(stack)-1]
			}
			if c == '}' && len(stack) > 0 && stack[len(stack)-1] == '[' {
				j := i + 1
				for j < len(s) && (s[j] == ' ' || s[j] == '\t' || s[j] == '\n' || s[j] == '\r') {
					j++
				}
				if j < len(s) && s[j] == '{' {
					at = append(at, i+1)
				}
			}
		}
	}
	if len(at) == 0 {
		return "", false
	}
	var b strings.Builder
	prev := 0
	for _, p := range at {
		b.WriteString(s[prev:p])
		b.WriteByte(',')
		prev = p
	}
	b.WriteString(s[prev:])
	return b.String(), true
}

// RowKind tells what an outline row is.
type RowKind int

const (
	RowGroup RowKind = iota
	RowText
	RowFile
	RowLink
	RowHeader // section header ("ligações")
	RowEdge
)

// Row is one outline line.
type Row struct {
	Depth  int
	Kind   RowKind
	Label  string
	NodeID string
	File   string // file nodes: vault-relative path
	URL    string // link nodes
	Color  string
}

func contains(g, n Node) bool {
	return n.X >= g.X && n.Y >= g.Y && n.X+n.Width <= g.X+g.Width && n.Y+n.Height <= g.Y+g.Height
}

func area(n Node) int { return n.Width * n.Height }

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = strings.TrimSpace(s[:i])
	}
	return strings.TrimLeft(s, "# ")
}

// Label is a node's one-line description.
func (n Node) Title() string {
	switch n.Type {
	case "group":
		if n.Label != "" {
			return n.Label
		}
		return "(grupo)"
	case "file":
		if n.Subpath != "" {
			return n.File + n.Subpath
		}
		return n.File
	case "link":
		return n.URL
	}
	if t := firstLine(n.Text); t != "" {
		return t
	}
	return "(texto vazio)"
}

// Outline returns groups (nested by geometric containment, the smallest
// enclosing group wins), their nodes in reading order (top to bottom,
// left to right), the ungrouped nodes, then the edges.
func (c *Canvas) Outline() []Row {
	byID := map[string]Node{}
	for _, n := range c.Nodes {
		byID[n.ID] = n
	}
	parent := map[string]string{}
	for _, n := range c.Nodes {
		best := ""
		for _, g := range c.Nodes {
			if g.Type != "group" || g.ID == n.ID || !contains(g, n) {
				continue
			}
			if n.Type == "group" && area(g) <= area(n) {
				continue
			}
			if best == "" || area(g) < area(byID[best]) {
				best = g.ID
			}
		}
		parent[n.ID] = best
	}
	children := map[string][]Node{}
	for _, n := range c.Nodes {
		children[parent[n.ID]] = append(children[parent[n.ID]], n)
	}
	for k := range children {
		cs := children[k]
		sort.SliceStable(cs, func(i, j int) bool {
			gi, gj := cs[i].Type == "group", cs[j].Type == "group"
			if gi != gj && k == "" {
				return gi // top level: groups first
			}
			if cs[i].Y != cs[j].Y {
				return cs[i].Y < cs[j].Y
			}
			return cs[i].X < cs[j].X
		})
	}
	var rows []Row
	seen := map[string]bool{}
	var walk func(id string, depth int)
	walk = func(id string, depth int) {
		for _, n := range children[id] {
			if seen[n.ID] {
				continue
			}
			seen[n.ID] = true
			r := Row{Depth: depth, Label: n.Title(), NodeID: n.ID, Color: n.Color}
			switch n.Type {
			case "group":
				r.Kind = RowGroup
			case "file":
				r.Kind, r.File = RowFile, n.File
			case "link":
				r.Kind, r.URL = RowLink, n.URL
			default:
				r.Kind = RowText
			}
			rows = append(rows, r)
			if n.Type == "group" {
				walk(n.ID, depth+1)
			}
		}
	}
	walk("", 0)
	if len(c.Edges) > 0 {
		rows = append(rows, Row{Kind: RowHeader, Label: fmt.Sprintf("ligações (%d)", len(c.Edges))})
		for _, e := range c.Edges {
			from, to := e.FromNode, e.ToNode
			if n, ok := byID[from]; ok {
				from = n.Title()
			}
			if n, ok := byID[to]; ok {
				to = n.Title()
			}
			arrow := "→"
			if e.FromEnd == "arrow" && e.ToEnd != "none" {
				arrow = "↔"
			} else if e.ToEnd == "none" && e.FromEnd != "arrow" {
				arrow = "—"
			}
			lbl := from + " " + arrow + " " + to
			if e.Label != "" {
				lbl += "  (" + e.Label + ")"
			}
			rows = append(rows, Row{Depth: 1, Kind: RowEdge, Label: lbl, NodeID: e.ToNode, Color: e.Color})
		}
	}
	return rows
}

// TextOf returns the full text of a text node (for the outline preview).
func (c *Canvas) TextOf(id string) string {
	for _, n := range c.Nodes {
		if n.ID == id {
			return n.Text
		}
	}
	return ""
}
