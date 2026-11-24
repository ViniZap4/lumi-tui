package kanban

import (
	"strings"
	"testing"
)

const sample = `---
kanban-plugin: board
tags: [x]
---

## A fazer

- [ ] escrever a especificação
- [ ] revisar ação
	com uma nota aninhada
	e outra

## Feito

- [x] lançar o TUI

some unknown text the plugin wrote

## Vazio


%% kanban:settings
` + "```" + `
{"kanban-plugin":"board"}
` + "```" + `
%%
`

func TestParse(t *testing.T) {
	if !IsBoard(sample) || IsBoard("# plain\n") || IsBoard("---\ntitle: x\n---\n") {
		t.Fatal("IsBoard")
	}
	b := Parse(sample)
	if len(b.Columns) != 3 {
		t.Fatalf("columns %+v", b.Columns)
	}
	if b.Columns[0].Title != "A fazer" || len(b.Columns[0].Cards) != 2 || len(b.Columns[1].Cards) != 1 || len(b.Columns[2].Cards) != 0 {
		t.Fatalf("board %+v", b.Columns)
	}
	c := b.Columns[0].Cards[1]
	if c.Title != "revisar ação" || len(c.Body) != 2 || c.Done {
		t.Fatalf("card %+v", c)
	}
	if !b.Columns[1].Cards[0].Done {
		t.Fatal("done")
	}
	if !strings.HasPrefix(sample[b.Settings:], "%% kanban:settings") {
		t.Fatal("settings offset")
	}
}

func apply(t *testing.T, text string, es []Edit) string {
	t.Helper()
	if es == nil {
		t.Fatal("no edits")
	}
	out := Apply(text, es)
	// settings and frontmatter always survive byte for byte
	if !strings.HasSuffix(out, sample[Parse(sample).Settings:]) || !strings.HasPrefix(out, "---\nkanban-plugin: board\ntags: [x]\n---\n") {
		t.Fatalf("preserved parts changed:\n%s", out)
	}
	return out
}

func TestToggleIsOneByte(t *testing.T) {
	b := Parse(sample)
	es := b.Toggle(0, 0)
	if len(es) != 1 || es[0].End-es[0].Start != 1 {
		t.Fatalf("edits %+v", es)
	}
	out := apply(t, sample, es)
	if !strings.Contains(out, "- [x] escrever a especificação") {
		t.Fatal(out)
	}
	if out2 := apply(t, out, Parse(out).Toggle(0, 0)); out2 != sample {
		t.Fatal("toggle twice is identity")
	}
}

func TestMoveBetweenColumns(t *testing.T) {
	b := Parse(sample)
	es := b.Move(0, 1, 1, 0)
	if len(es) != 2 {
		t.Fatalf("move is delete+insert: %+v", es)
	}
	out := apply(t, sample, es)
	nb := Parse(out)
	if len(nb.Columns[0].Cards) != 1 || len(nb.Columns[1].Cards) != 2 || nb.Columns[1].Cards[0].Title != "revisar ação" || len(nb.Columns[1].Cards[0].Body) != 2 {
		t.Fatalf("after move %+v\n%s", nb.Columns, out)
	}
	if !strings.Contains(out, "some unknown text the plugin wrote") {
		t.Fatal("unknown content lost")
	}
}

func TestMoveIntoEmptyColumnAndWithin(t *testing.T) {
	out := apply(t, sample, Parse(sample).Move(1, 0, 2, 0))
	nb := Parse(out)
	if len(nb.Columns[2].Cards) != 1 || nb.Columns[2].Cards[0].Title != "lançar o TUI" || len(nb.Columns[1].Cards) != 0 {
		t.Fatalf("%+v\n%s", nb.Columns, out)
	}
	out = apply(t, sample, Parse(sample).Move(0, 0, 0, 1))
	nb = Parse(out)
	if nb.Columns[0].Cards[0].Title != "revisar ação" || nb.Columns[0].Cards[1].Title != "escrever a especificação" {
		t.Fatalf("reorder %+v", nb.Columns[0].Cards)
	}
	if es := Parse(sample).Move(0, 0, 0, 0); es != nil {
		t.Fatal("no-op move")
	}
}

func TestInsertDeleteEditAddColumn(t *testing.T) {
	out := apply(t, sample, Parse(sample).Insert(2, 0, "nova"))
	if Parse(out).Columns[2].Cards[0].Title != "nova" {
		t.Fatal(out)
	}
	out = apply(t, sample, Parse(sample).Insert(0, 99, "fim"))
	if c := Parse(out).Columns[0].Cards; len(c) != 3 || c[2].Title != "fim" {
		t.Fatal(out)
	}
	out = apply(t, sample, Parse(sample).Delete(0, 1))
	if strings.Contains(out, "aninhada") || len(Parse(out).Columns[0].Cards) != 1 {
		t.Fatal(out)
	}
	out = apply(t, sample, Parse(sample).SetText(0, 0, "outro título"))
	if Parse(out).Columns[0].Cards[0].Title != "outro título" {
		t.Fatal(out)
	}
	out = apply(t, sample, Parse(sample).AddColumn("Bloqueado"))
	nb := Parse(out)
	if len(nb.Columns) != 4 || nb.Columns[3].Title != "Bloqueado" {
		t.Fatalf("%+v\n%s", nb.Columns, out)
	}
}

func TestNoTrailingNewline(t *testing.T) {
	src := "---\nkanban-plugin: basic\n---\n## A\n- [ ] one"
	out := Apply(src, Parse(src).Insert(0, 1, "two"))
	if out != src+"\n- [ ] two\n" {
		t.Fatalf("%q", out)
	}
	out = Apply(src, Parse(src).AddColumn("B"))
	if Parse(out).Columns[1].Title != "B" {
		t.Fatalf("%q", out)
	}
}
