package ui

import (
	"reflect"
	"strings"
	"testing"
)

func TestLegendWhichKey(t *testing.T) {
	if got := legendFor(CtxFloat, "g"); !reflect.DeepEqual(got, legendG) {
		t.Fatalf("g → %+v", got)
	}
	if got := legendFor(CtxFloat, "z"); !reflect.DeepEqual(got, legendZ) {
		t.Fatalf("z → %+v", got)
	}
	if got := legendFor(CtxReader, "z"); !reflect.DeepEqual(got, legendNoteZ) {
		t.Fatalf("reader z → %+v", got)
	}
	if got := legendFor(CtxFloat, "12"); got[0].Keys != "12j" || got[1].Keys != "12k" {
		t.Fatalf("count → %+v", got)
	}
	if got := legendFor(CtxFloat, "3g"); !reflect.DeepEqual(got, legendG) {
		t.Fatalf("count+g → %+v", got)
	}
	// Chords are not offered in text contexts.
	if got := legendFor(CtxFilter, "g"); got[0].Keys != "⏎" {
		t.Fatalf("filter → %+v", got)
	}
}

func TestLegendContexts(t *testing.T) {
	first := map[LegendContext]string{
		CtxFloat: "⏎", CtxReader: "h j k l", CtxReadVisual: "y", CtxSplitPre: "h j k l", CtxPreview: "j k", CtxParent: "j k",
		CtxPrompt: "⏎", CtxConfirm: "y ⏎", CtxPalette: "↑ ↓ ⌃n ⌃p", CtxBooks: "j k",
		CtxSettings: "j k", CtxHelp: "j k", CtxVisual: "y", CtxHome: "-",
	}
	for ctx, keys := range first {
		got := legendFor(ctx, "")
		if len(got) == 0 || got[0].Keys != keys {
			t.Errorf("%s: %+v", ctx, got)
		}
	}
}

func TestFitLegend(t *testing.T) {
	shown, over := fitLegend([]int{5, 5, 5}, 20, 2, 1)
	if over || len(shown) != 3 {
		t.Fatalf("all fit: %v %v", shown, over)
	}
	// 10 + 2 + 10 > 15: the second is skipped but the small third fits.
	shown, over = fitLegend([]int{10, 10, 2}, 15, 2, 1)
	if !over || !reflect.DeepEqual(shown, []int{0}) {
		t.Fatalf("greedy: %v %v", shown, over)
	}
	shown, _ = fitLegend([]int{6, 30, 3}, 20, 2, 1)
	if !reflect.DeepEqual(shown, []int{0, 2}) {
		t.Fatalf("skip wide keep small: %v", shown)
	}
}

func TestLegendLineIsFullWidth(t *testing.T) {
	for _, w := range []int{20, 60, 140} {
		line := renderLegendLine(legendFor(CtxFloat, ""), w)
		if got := len([]rune(stripAnsi(line))); got != w {
			t.Errorf("width %d: rendered %d columns", w, got)
		}
		if w < 140 && !strings.Contains(stripAnsi(line), "…") {
			t.Errorf("width %d: overflow marker missing", w)
		}
	}
}

func TestLegendFollowsModel(t *testing.T) {
	m, _ := explorer(t)
	// the floating explorer's footer carries the legend
	foot := func(m Model) string { f, _ := m.explorerFooter(m.width - 2); return f }
	m = keys(t, m, "g")
	if row := stripAnsi(foot(m)); !strings.Contains(row, "gb books") || !strings.Contains(row, "gs sync details") {
		t.Fatalf("which-key row = %q", row)
	}
	m = keys(t, m, "esc")
	if row := stripAnsi(foot(m)); !strings.Contains(row, "j k move") {
		t.Fatalf("explorer row = %q", row)
	}
}
