package ui

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mattn/go-runewidth"
	"github.com/vinizap/lumi/tui-client/vim"
)

const mermaidNote = "---\ntitle: Diagram\n---\n\n# Diagram\n\nintro text with [site](https://example.com)\n\n```mermaid\nflowchart TD\n  A[Start] --> B{Ok?}\n  B -->|yes| C[Ship]\n```\n\n~~~mermaid\ngantt\n  title Plan\n~~~\n\nafter the diagrams\n"

func TestRenderProseMermaidBlocks(t *testing.T) {
	out := stripAnsi(renderProse(nil, mermaidNote, 60))
	for _, want := range []string{
		"╭─ ◇ mermaid · flowchart ─", "│ Start │", "yes", "▼",
		"╭─ ◇ mermaid · gantt ─", "title Plan", "after the diagrams", "intro text",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in\n%s", want, out)
		}
	}
	if strings.Contains(out, "flowchart TD") {
		t.Error("a drawn flowchart must not repeat its source")
	}
	for _, l := range strings.Split(out, "\n") {
		if w := runewidth.StringWidth(l); w > 60 {
			t.Errorf("line wider than 60 (%d): %q", w, l)
		}
	}
	// unknown kind: header without a kind, source shown
	out = stripAnsi(renderProse(nil, "```mermaid\nhello world\n```\n", 40))
	if !strings.Contains(out, "╭─ ◇ mermaid ─") || !strings.Contains(out, "│ hello world") {
		t.Errorf("unknown kind:\n%s", out)
	}
	// escape sequences in the source never reach the terminal
	out = renderProse(nil, "```mermaid\npie\n  \"x\x1b]52;c;Zm9v\x07\" : 1\n```\n", 40)
	if strings.Contains(out, "\x1b]52") || strings.Contains(out, "\x07") {
		t.Errorf("control sequence survived: %q", out)
	}
	// a long source line wraps inside the frame
	out = stripAnsi(renderProse(nil, "```mermaid\ngantt\n"+strings.Repeat("w", 100)+"\n```\n", 30))
	for _, l := range strings.Split(out, "\n") {
		if runewidth.StringWidth(l) > 30 {
			t.Errorf("wrapped line wider than 30: %q", l)
		}
	}
}

func TestMermaidFenceAt(t *testing.T) {
	body := "text\n```mermaid\ngraph TD\nA-->B\n```\nmore\n```go\nx\n```\n````mermaid\npie\n````\n"
	want := map[int]bool{0: false, 1: true, 2: true, 3: true, 4: true, 5: false, 6: false, 7: false, 8: false, 9: true, 10: true, 11: true, 12: false}
	for l, w := range want {
		if got := mermaidFenceAt(body, l); got != w {
			t.Errorf("line %d: %v, want %v", l, got, w)
		}
	}
}

func mermaidReader(t *testing.T) (Model, string) {
	t.Helper()
	dir := notesVault(t)
	mustWrite(t, filepath.Join(dir, "diagram.md"), mermaidNote)
	m := newTreeModel(t, dir)
	m.width, m.height = 120, 40
	m = openNamed(t, m, "Diagram")
	if m.ws.focus.view != viewReader {
		t.Fatalf("opened in %v", m.ws.focus.view)
	}
	return m, filepath.Join(dir, "diagram.md")
}

func TestReaderGxOnMermaidOpensTheNoteInTheApp(t *testing.T) {
	m, path := mermaidReader(t)
	d, _ := m.readerDoc(m.ws.focus)
	if len(d.mermaid) != 2 {
		t.Fatalf("mermaid blocks %v", d.mermaid)
	}

	// outside any block and off a link: nothing opens
	openedInApp, openedURLs = nil, nil
	m = moveToText(t, m, "after the diagrams")
	m = keys(t, m, "g", "x")
	if len(openedInApp) != 0 || len(openedURLs) != 0 || m.statusMsg != "nenhum link externo no cursor" {
		t.Fatalf("gx off a diagram: app %v urls %v status %q", openedInApp, openedURLs, m.statusMsg)
	}

	// an external link keeps its meaning
	m = moveToText(t, m, "site")
	m = keys(t, m, "g", "x")
	if len(openedURLs) != 1 || len(openedInApp) != 0 {
		t.Fatalf("gx on a link: urls %v app %v", openedURLs, openedInApp)
	}

	// inside the drawn flowchart (on the frame, a box, a blank line)
	for _, at := range []string{"Start", "◇ mermaid · flowchart", "◇ mermaid · gantt", "title Plan"} {
		openedInApp = nil
		m = moveToText(t, m, at)
		m = keys(t, m, "g", "x")
		if len(openedInApp) != 1 || openedInApp[0] != path {
			t.Fatalf("gx on %q: %v", at, openedInApp)
		}
		if !strings.HasPrefix(m.statusMsg, "aberto: lumi://open") {
			t.Fatalf("status after gx: %q", m.statusMsg)
		}
	}

	// a failing `lumi open` reports its error line
	defer func(f func(string) (string, error)) { openInAppResult = f }(openInAppResult)
	openInAppResult = func(string) (string, error) {
		return "Error: no book matches \"x\" (lumi books list)", errors.New("exit status 1")
	}
	m = moveToText(t, m, "Start")
	m = keys(t, m, "g", "x")
	if m.statusMsg != `lumi open: no book matches "x" (lumi books list)` {
		t.Fatalf("error status %q", m.statusMsg)
	}
}

func TestEditorGxInMermaidFence(t *testing.T) {
	m, path := mermaidReader(t)
	m = keys(t, m, "i")
	if m.ws.focus.view != viewSource {
		t.Fatalf("i → %v", m.ws.focus.view)
	}
	m = keys(t, m, "esc")
	e := m.ws.focus.buf.eng
	lines := strings.Split(e.Text(), "\n")
	at := func(s string) int {
		for i, l := range lines {
			if strings.Contains(l, s) {
				return i
			}
		}
		t.Fatalf("%q not in the source", s)
		return -1
	}
	openedInApp = nil
	e.SetCursor(vim.Pos{Line: at("after the diagrams")})
	m = keys(t, m, "g", "x")
	if len(openedInApp) != 0 {
		t.Fatalf("gx outside a fence: %v", openedInApp)
	}
	e.SetCursor(vim.Pos{Line: at("A[Start]"), Col: 4})
	m = keys(t, m, "g", "x")
	if len(openedInApp) != 1 || openedInApp[0] != path {
		t.Fatalf("gx in a mermaid fence: %v", openedInApp)
	}
}
