package ui

import (
	"strings"
	"testing"
)

func TestSplitProseCalloutsAndFootnotes(t *testing.T) {
	segs, notes, ids := splitProse("a[^x]\n\n> [!TIP] Hi\n> body\n> more\n\nafter\n\n```\n> [!note] not a callout\n[^y]: not a def\n```\n[^x]: the note\n    continued")
	if len(segs) != 3 || segs[1].callout != "tip" || segs[1].title != "Hi" || segs[1].body != "body\nmore" {
		t.Fatalf("segs = %+v", segs)
	}
	if !strings.Contains(segs[2].body, "> [!note] not a callout") || !strings.Contains(segs[2].body, "[^y]: not a def") {
		t.Fatalf("fenced code was rewritten: %q", segs[2].body)
	}
	if len(notes) != 1 || notes[0] != "the note continued" || ids["x"] != 1 {
		t.Fatalf("notes = %v ids = %v", notes, ids)
	}
	if got := replaceFootRefs("see[^x] and[^new]", ids); got != "see¹ and²" {
		t.Fatalf("refs = %q", got)
	}
}

func TestProseGolden(t *testing.T) {
	golden(t, "prose-60", newProseCache().render(proseSample, 60))
}

func TestProseCacheHits(t *testing.T) {
	c := newProseCache()
	a := c.render("# x", 30)
	b := c.render("# x", 30)
	if a != b || len(c.out) != 1 {
		t.Fatalf("cache: %d entries", len(c.out))
	}
	for i := 0; i < proseCacheCap+5; i++ {
		c.render(strings.Repeat("y", i+1), 30)
	}
	if len(c.out) > proseCacheCap {
		t.Fatalf("cache grew to %d", len(c.out))
	}
}

func TestExplorerGolden(t *testing.T) {
	root := t.TempDir()
	mustMkdir(t, root+"/projects")
	mustWrite(t, root+"/projects/plan.md", "# plan\n")
	mustWrite(t, root+"/guide.md", proseSample)
	m := NewModelWithInitialNote(root, "")
	m = drive(m)
	m.width, m.height = 100, 30
	m = keys(t, m, "j")
	golden(t, "explorer-100x30", headerless(m.View()))
	m = keys(t, m, "g")
	golden(t, "explorer-which-key-g", lastLines(m.View(), 2))
	m = keys(t, m, "esc", "enter")
	golden(t, "reader-100x30", headerless(m.View()))
}

// headerless drops the first line (it names the temp folder).
func headerless(s string) string {
	lines := strings.Split(s, "\n")
	return strings.Join(lines[1:], "\n")
}

func lastLines(s string, n int) string {
	lines := strings.Split(s, "\n")
	return strings.Join(lines[len(lines)-n:], "\n")
}
