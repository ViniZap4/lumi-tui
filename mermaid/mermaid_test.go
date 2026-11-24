package mermaid

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var update = flag.Bool("update", false, "rewrite testdata/*.golden")

func TestKind(t *testing.T) {
	cases := map[string]string{
		"graph TD\nA-->B":                               "flowchart",
		"flowchart LR\nA-->B":                           "flowchart",
		"  \n%% a comment\nsequenceDiagram\nA->>B: hi":  "sequence",
		"%%{init: {'theme':'dark'}}%%\ngantt\ntitle x":  "gantt",
		"---\ntitle: Pets\n---\nclassDiagram\nA <|-- B": "class",
		"classDiagram-v2\n":                             "class",
		"stateDiagram-v2\n[*] --> S":                    "state",
		"stateDiagram\n":                                "state",
		"erDiagram\nA ||--o{ B : has":                   "er",
		"pie title Pets\n\"Dogs\" : 3":                  "pie",
		"mindmap\n  root":                               "mindmap",
		"journey\ntitle x":                              "journey",
		"gitGraph\ncommit":                              "gitgraph",
		"timeline\ntitle x":                             "timeline",
		"quadrantChart\n":                               "quadrant",
		"xychart-beta\n":                                "xychart",
		"sankey-beta\n":                                 "sankey",
		"C4Context\n":                                   "c4",
		"requirementDiagram\n":                          "requirement",
		"block-beta\n":                                  "block",
		"architecture-beta\n":                           "architecture",
		"graph":                                         "flowchart",
		"hello world":                                   "",
		"":                                              "",
		"%% only a comment":                             "",
		"---\nunterminated frontmatter\ngraph TD":       "",
	}
	for src, want := range cases {
		if got := Kind(src); got != want {
			t.Errorf("Kind(%q) = %q, want %q", src, got, want)
		}
	}
}

func TestParseFlowchart(t *testing.T) {
	f, err := parseFlowchart(statements(`flowchart LR
  A[Rect] --> B(Round) -->|yes| C{Decision}
  C -- no --> D([Stadium]):::hot
  A --- D; D ==> E((Circle))
  E -.-> A
  style A fill:#f9f
  click A "https://example.com"
  A`))
	if err != nil {
		t.Fatal(err)
	}
	if !f.horizontal {
		t.Error("LR not horizontal")
	}
	var labels []string
	for _, n := range f.nodes {
		labels = append(labels, n.label)
	}
	if got := strings.Join(labels, ","); got != "Rect,Round,Decision,Stadium,Circle" {
		t.Errorf("labels %s", got)
	}
	shapes := []shape{shapeRect, shapeRound, shapeDiamond, shapeRound, shapeRound}
	for i, s := range shapes {
		if f.nodes[i].shape != s {
			t.Errorf("node %d shape %v, want %v", i, f.nodes[i].shape, s)
		}
	}
	type e struct {
		from, to string
		label    string
		arrow    bool
		style    int
	}
	want := []e{
		{"A", "B", "", true, solid},
		{"B", "C", "yes", true, solid},
		{"C", "D", "no", true, solid},
		{"A", "D", "", false, solid},
		{"D", "E", "", true, thick},
		{"E", "A", "", true, dotted},
	}
	if len(f.edges) != len(want) {
		t.Fatalf("%d edges, want %d", len(f.edges), len(want))
	}
	for i, w := range want {
		g := f.edges[i]
		got := e{f.nodes[g.from].id, f.nodes[g.to].id, g.label, g.arrow, g.style}
		if got != w {
			t.Errorf("edge %d = %+v, want %+v", i, got, w)
		}
	}
}

func TestParseFlowchartUnsupported(t *testing.T) {
	for _, src := range []string{
		"graph BT\nA-->B",
		"graph RL\nA-->B",
		"graph TD\nA --> B & C",
		"graph TD\nsubgraph one\nA-->B\nend",
		"graph TD\nA[unclosed --> B",
		"graph TD\nA ~~~ B",
		"graph TD\nA <--> B",
		"graph TD",
	} {
		if _, err := parseFlowchart(statements(src)); err == nil {
			t.Errorf("parsed %q", src)
		}
		if _, ok := Render(src, 200); ok {
			t.Errorf("rendered %q", src)
		}
	}
}

func TestParseSequence(t *testing.T) {
	s, err := parseSequence(statements(`sequenceDiagram
  participant A as Alice
  actor B
  A->>+B: hi
  B-->>-A: yo
  A-xB: lost
  A--)B: async
  A->B: plain
  activate A
  Note over A,B: shared
  Note left of A: l
  rect rgb(0,0,0)
  loop forever
    B->>B: self
  else other
  end
  end`))
	if err != nil {
		t.Fatal(err)
	}
	if len(s.parts) != 2 || s.parts[0].label != "Alice" || !s.parts[1].actor {
		t.Fatalf("participants %+v", s.parts)
	}
	kinds := []seqKind{seqMsg, seqMsg, seqMsg, seqMsg, seqMsg, seqNote, seqNote, seqBlock, seqMsg, seqElse, seqEnd}
	if len(s.items) != len(kinds) {
		t.Fatalf("%d items, want %d: %+v", len(s.items), len(kinds), s.items)
	}
	for i, k := range kinds {
		if s.items[i].kind != k {
			t.Errorf("item %d kind %v, want %v", i, s.items[i].kind, k)
		}
	}
	heads := []string{"▶", "▶", "✕", "▷", ""}
	styles := []int{solid, dotted, solid, dotted, solid}
	for i := range heads {
		if s.items[i].head != heads[i] || s.items[i].style != styles[i] {
			t.Errorf("msg %d head %q style %d", i, s.items[i].head, s.items[i].style)
		}
	}
	if s.items[0].text != "hi" || s.items[1].from != 1 || s.items[1].to != 0 {
		t.Errorf("first messages %+v %+v", s.items[0], s.items[1])
	}
}

func TestParseSequenceUnsupported(t *testing.T) {
	for _, src := range []string{
		"sequenceDiagram\nA->>B hi",         // no colon
		"sequenceDiagram\nloop x\nA->>B: m", // unclosed block
		"sequenceDiagram\nend",
		"sequenceDiagram\nA<<->>B: both",
		"sequenceDiagram\nNote over A,B,C: x",
		"sequenceDiagram\nsomething odd",
		"sequenceDiagram",
	} {
		if _, ok := Render(src, 200); ok {
			t.Errorf("rendered %q", src)
		}
	}
}

func TestRenderGolden(t *testing.T) {
	files, _ := filepath.Glob(filepath.Join("testdata", "*.mmd"))
	if len(files) == 0 {
		t.Fatal("no testdata")
	}
	for _, f := range files {
		t.Run(filepath.Base(f), func(t *testing.T) {
			src, err := os.ReadFile(f)
			if err != nil {
				t.Fatal(err)
			}
			lines, ok := Render(string(src), 120)
			if !ok {
				t.Fatalf("not rendered")
			}
			var b strings.Builder
			for _, l := range lines {
				b.WriteString(l.String())
				b.WriteByte('\n')
				if l.Width() > 120 {
					t.Errorf("line wider than 120: %q", l.String())
				}
			}
			golden := strings.TrimSuffix(f, ".mmd") + ".golden"
			if *update {
				if err := os.WriteFile(golden, []byte(b.String()), 0o644); err != nil {
					t.Fatal(err)
				}
				return
			}
			want, err := os.ReadFile(golden)
			if err != nil {
				t.Fatalf("missing %s (go test ./mermaid -update): %v", golden, err)
			}
			if string(want) != b.String() {
				t.Errorf("%s mismatch\n--- want\n%s--- got\n%s", golden, want, b.String())
			}
		})
	}
}

func TestRenderFallsBackWhenTooWide(t *testing.T) {
	src := "graph LR\nA[a fairly long label] --> B[another long label] --> C[and a third one]"
	if _, ok := Render(src, 200); !ok {
		t.Fatal("not rendered at 200")
	}
	if _, ok := Render(src, 40); ok {
		t.Fatal("rendered wider than 40 columns")
	}
}

func TestRenderCaps(t *testing.T) {
	var b strings.Builder
	b.WriteString("graph TD\n")
	for i := 0; i <= maxNodes; i++ {
		fmt.Fprintf(&b, "n%d --> z\n", i)
	}
	if _, ok := Render(b.String(), 10000); ok {
		t.Error("rendered past the node cap")
	}
	if _, ok := Render("graph TD\n"+strings.Repeat("A-->B\n", maxSourceBytes/6+1), 10000); ok {
		t.Error("rendered past the source cap")
	}
	if _, ok := Render("graph TD\nA-->B", 4); ok {
		t.Error("rendered in 4 columns")
	}
}

func TestLabelsAreSanitised(t *testing.T) {
	lines, ok := Render("graph TD\nA[\"evil \x1b[31mred\x1b[0m\"] --> B[ok]", 80)
	if !ok {
		t.Fatal("not rendered")
	}
	for _, l := range lines {
		if strings.ContainsAny(l.String(), "\x1b\x07") {
			t.Fatalf("control character survived: %q", l.String())
		}
	}
	if got := cleanLabel(strings.Repeat("w", 100), 10); textWidth(got) > 10 || !strings.HasSuffix(got, "…") {
		t.Errorf("clip: %q", got)
	}
}

func TestCanvasJunctions(t *testing.T) {
	c := &canvas{}
	c.hline(0, 4, 1, solid, Edge)
	c.vline(2, 0, 2, solid, Edge)
	c.vline(0, 1, 2, solid, Edge)
	c.put(4, 2, "▼", Arrow)
	var got []string
	for _, l := range c.lines() {
		got = append(got, l.String())
	}
	want := []string{"  │", "┌─┼──", "│ │ ▼"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("got %q, want %q", got, want)
	}
}
