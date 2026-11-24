package canvas

import "testing"

const sample = `{
  "nodes": [
    {"id":"g1","type":"group","x":0,"y":0,"width":1000,"height":600,"label":"Projeto"},
    {"id":"g2","type":"group","x":20,"y":300,"width":400,"height":250,"label":"Ideias"},
    {"id":"t1","type":"text","x":40,"y":40,"width":200,"height":80,"text":"# Visão\nmais texto"},
    {"id":"f1","type":"file","x":300,"y":40,"width":200,"height":80,"file":"notes/plano.md"},
    {"id":"t2","type":"text","x":40,"y":320,"width":100,"height":60,"text":"ação rápida"},
    {"id":"l1","type":"link","x":1200,"y":0,"width":200,"height":80,"url":"https://jsoncanvas.org"}
  ],
  "edges": [
    {"id":"e1","fromNode":"t1","toNode":"f1","label":"detalha"},
    {"id":"e2","fromNode":"f1","toNode":"l1","toEnd":"none"}
  ]
}`

func TestOutline(t *testing.T) {
	c, err := Parse([]byte(sample))
	if err != nil {
		t.Fatal(err)
	}
	rows := c.Outline()
	want := []struct {
		depth int
		kind  RowKind
		label string
	}{
		{0, RowGroup, "Projeto"},
		{1, RowText, "Visão"},
		{1, RowFile, "notes/plano.md"},
		{1, RowGroup, "Ideias"},
		{2, RowText, "ação rápida"},
		{0, RowLink, "https://jsoncanvas.org"},
		{0, RowHeader, "ligações (2)"},
		{1, RowEdge, "Visão → notes/plano.md  (detalha)"},
		{1, RowEdge, "notes/plano.md — https://jsoncanvas.org"},
	}
	if len(rows) != len(want) {
		t.Fatalf("rows %+v", rows)
	}
	for i, w := range want {
		r := rows[i]
		if r.Depth != w.depth || r.Kind != w.kind || r.Label != w.label {
			t.Errorf("row %d: %+v want %+v", i, r, w)
		}
	}
	if rows[2].File != "notes/plano.md" {
		t.Fatal("file path")
	}
}

func TestParseErrors(t *testing.T) {
	if c, err := Parse([]byte("  ")); err != nil || len(c.Nodes) != 0 {
		t.Fatal("empty canvas")
	}
	if _, err := Parse([]byte("{nope")); err == nil {
		t.Fatal("bad json accepted")
	}
}
