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
		return nil, fmt.Errorf("invalid JSON Canvas: %w", err)
	}
	return c, nil
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
