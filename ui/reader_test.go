package ui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/vinizap/lumi/tui-client/vim"
)

func readerVault(t *testing.T) string {
	t.Helper()
	dir := notesVault(t)
	mustWrite(t, filepath.Join(dir, "tasks.md"), "---\ntitle: Tasks\n---\n\n# Tasks\n\nalpha beta gamma\nsecond line here\n\n- [ ] open task\n- [x] done task\n\n```\n- [ ] not a task\n```\n")
	return dir
}

func openReader(t *testing.T, name string) (Model, string) {
	t.Helper()
	dir := readerVault(t)
	m := newTreeModel(t, dir)
	m.width, m.height = 120, 30
	m = openNamed(t, m, name)
	if m.ws.focus.view != viewReader {
		t.Fatalf("opened in %v", m.ws.focus.view)
	}
	return m, dir
}

func readerCursor(m Model) (vim.Pos, string) {
	d, _ := m.readerDoc(m.ws.focus)
	c := d.eng.Cursor()
	return c, string(d.plain[c.Line])
}

// moveToText puts the reader cursor on the first rendered line holding s.
func moveToText(t *testing.T, m Model, s string) Model {
	t.Helper()
	d, _ := m.readerDoc(m.ws.focus)
	for i, l := range d.plain {
		if idx := strings.Index(string(l), s); idx >= 0 {
			d.eng.SetCursor(vim.Pos{Line: i, Col: len([]rune(string(l)[:idx]))})
			return m
		}
	}
	t.Fatalf("%q not rendered", s)
	return m
}

func TestReaderCursorMotionsVisualAndYank(t *testing.T) {
	m, _ := openReader(t, "Tasks")
	clip := systemClipboard.(*vim.MemClipboard)
	m = moveToText(t, m, "alpha")
	m = keys(t, m, "w")
	if c, l := readerCursor(m); !strings.HasPrefix(string([]rune(l)[c.Col:]), "beta") {
		t.Fatalf("w → %q col %d", l, c.Col)
	}
	m = keys(t, m, "e")
	if c, l := readerCursor(m); string([]rune(l)[c.Col]) != "a" || !strings.HasPrefix(string([]rune(l)[c.Col-3:]), "beta") {
		t.Fatalf("e → col %d of %q", c.Col, l)
	}
	m = keys(t, m, "b", "0")
	if c, _ := readerCursor(m); c.Col != 0 {
		t.Fatalf("0 → %d", c.Col)
	}
	m = keys(t, m, "$")
	if c, l := readerCursor(m); c.Col != len([]rune(l))-1 {
		t.Fatalf("$ → %d of %q", c.Col, l)
	}
	start, _ := readerCursor(m)
	m = keys(t, m, "2", "j")
	if c, _ := readerCursor(m); c.Line != start.Line+2 {
		t.Fatalf("2j → %d from %d", c.Line, start.Line)
	}
	m = keys(t, m, "G")
	d, _ := m.readerDoc(m.ws.focus)
	if c, _ := readerCursor(m); c.Line != len(d.plain)-1 {
		t.Fatalf("G → %d", c.Line)
	}
	m = keys(t, m, "g", "g")
	if c, _ := readerCursor(m); c.Line != 0 {
		t.Fatalf("gg → %d", c.Line)
	}
	// / search moves the cursor
	m = keys(t, m, "/")
	m = typeText(t, m, "second")
	m = keys(t, m, "enter")
	if c, l := readerCursor(m); !strings.HasPrefix(string([]rune(l)[c.Col:]), "second") {
		t.Fatalf("/second → %q col %d", l, c.Col)
	}
	// v + e + y copies the word through the clipboard
	clip.Text = ""
	m = keys(t, m, "v")
	if d.eng.Mode() != vim.ModeVisual || m.legendContext() != CtxReadVisual {
		t.Fatalf("v → %v / %v", d.eng.Mode(), m.legendContext())
	}
	m = keys(t, m, "e", "y")
	if clip.Text != "second" {
		t.Fatalf("visual yank → %q", clip.Text)
	}
	if !strings.Contains(m.statusMsg, "copiad") {
		t.Fatalf("status %q", m.statusMsg)
	}
	// the selection is drawn while it is active
	m = keys(t, m, "V")
	if v := m.View(); !strings.Contains(v, "second line here") {
		t.Fatal("selection line missing")
	}
	m = keys(t, m, "y")
	// (the two source lines are one paragraph once rendered)
	if clip.Text != "alpha beta gamma second line here\n" {
		t.Fatalf("V y → %q", clip.Text)
	}
	m = keys(t, m, "y", "y")
	if clip.Text != "alpha beta gamma second line here\n" {
		t.Fatalf("yy → %q", clip.Text)
	}
	// ⌃v block selection
	m = keys(t, m, "0", "ctrl+v", "l", "k", "y")
	if !strings.Contains(clip.Text, "\n") {
		t.Fatalf("block yank %q", clip.Text)
	}
	// p and every other change are refused (read-only)
	before := m.ws.focus.buf.eng.Text()
	m = keys(t, m, "p", "P", "D", "J")
	if m.ws.focus.buf.eng.Text() != before || m.ws.focus.buf.dirty() {
		t.Fatal("the reader changed the note")
	}
	if msg, isErr := d.eng.Message(); !isErr || !strings.Contains(msg, "E21") {
		t.Fatalf("message %q", msg)
	}
}

func TestReaderLinkUnderCursorFollowSplitAndGx(t *testing.T) {
	m, _ := openReader(t, "One")
	m = moveToText(t, m, "two")
	m = keys(t, m, "K")
	if m.hover == nil || m.hover.note == nil || m.hover.note.Title != "Two" {
		t.Fatalf("K on the cursor's link: %+v", m.hover)
	}
	m = keys(t, m, "esc", "ctrl+w", "f")
	if len(m.ws.leaves()) != 2 || m.ws.focus.buf.note.Title != "Two" || m.ws.root.dir != 'v' {
		t.Fatal("⌃w f: vertical split on the link")
	}
	m = keys(t, m, "ctrl+w", "q")
	m = moveToText(t, m, "two")
	m = keys(t, m, "ctrl+w", "F")
	if len(m.ws.leaves()) != 2 || m.ws.root.dir != 'h' {
		t.Fatal("⌃w F: horizontal split")
	}
	m = keys(t, m, "ctrl+w", "q")
	openedURLs = nil
	m = moveToText(t, m, "site")
	m = keys(t, m, "g", "x")
	if len(openedURLs) != 1 || openedURLs[0] != "https://example.com" {
		t.Fatalf("gx → %v", openedURLs)
	}
	m = moveToText(t, m, "two")
	m = keys(t, m, "g", "f")
	if m.ws.focus.buf.note.Title != "Two" || len(m.ws.leaves()) != 1 {
		t.Fatal("gf follows in the pane")
	}
}

func TestReaderTabMovesCursorAndEnterFollows(t *testing.T) {
	m, _ := openReader(t, "One")
	m = keys(t, m, "tab")
	d, _ := m.readerDoc(m.ws.focus)
	if d.linkAtCursor() != 0 {
		t.Fatal("Tab puts the cursor on the first link")
	}
	m = keys(t, m, "tab")
	if d.linkAtCursor() != 1 {
		t.Fatal("Tab again: second link")
	}
	m = keys(t, m, "shift+tab")
	if d.linkAtCursor() != 0 {
		t.Fatal("⇧Tab: back")
	}
	m = keys(t, m, "enter")
	if m.ws.focus.buf.note.Title != "Two" {
		t.Fatal("⏎ follows the link under the cursor")
	}
}

func TestReaderMouseClickOnLink(t *testing.T) {
	m, _ := openReader(t, "One")
	p := m.ws.focus
	d, _ := m.readerDoc(p)
	r, _, _ := m.paneRect(p)
	lp := d.pos[0]
	click := func(m Model, ctrl bool, lp linkPos) Model {
		msg := tea.MouseMsg{X: r.x + readerIndent + lp.col + 1, Y: r.y + 1 + lp.line - d.eng.Top,
			Button: tea.MouseButtonLeft, Action: tea.MouseActionPress, Ctrl: ctrl}
		next, cmd := m.Update(msg)
		return settle(next.(Model), cmd)
	}
	// a click on plain text places the cursor
	msg := tea.MouseMsg{X: r.x + readerIndent + 1, Y: r.y + 1, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress}
	next, _ := m.Update(msg)
	m = next.(Model)
	if c := d.eng.Cursor(); c.Line != d.eng.Top || c.Col != 1 {
		t.Fatalf("click → %+v", c)
	}
	m = click(m, true, lp)
	if len(m.ws.leaves()) != 2 || m.ws.focus.buf.note.Title != "Two" {
		t.Fatal("Ctrl-click opens the link in a vertical split")
	}
	m = keys(t, m, "ctrl+w", "q")
	m = click(m, false, lp)
	if len(m.ws.leaves()) != 1 || m.ws.focus.buf.note.Title != "Two" {
		t.Fatal("a click on a link follows it")
	}
}

func TestReaderTaskToggleSaves(t *testing.T) {
	m, dir := openReader(t, "Tasks")
	m = moveToText(t, m, "open task")
	m = keys(t, m, "enter")
	data, _ := os.ReadFile(filepath.Join(dir, "tasks.md"))
	if !strings.Contains(string(data), "- [x] open task") || !strings.HasPrefix(string(data), "---\ntitle: Tasks\n---\n") {
		t.Fatalf("⏎ on a task:\n%s", data)
	}
	if m.ws.focus.buf.dirty() {
		t.Fatal("toggle must be saved")
	}
	m = moveToText(t, m, "done task")
	m = keys(t, m, "x")
	data, _ = os.ReadFile(filepath.Join(dir, "tasks.md"))
	if !strings.Contains(string(data), "- [ ] done task") || !strings.Contains(string(data), "```\n- [ ] not a task\n```") {
		t.Fatalf("x on a task:\n%s", data)
	}
	// the cursor stays where it was after the re-render
	if _, l := readerCursor(m); !strings.Contains(l, "done task") {
		t.Fatalf("cursor moved to %q", l)
	}
}

func TestPreviewFocusHasACursor(t *testing.T) {
	m, _ := openReader(t, "One")
	m = keys(t, m, "i", ":")
	m = typeText(t, m, "preview")
	m = keys(t, m, "enter")
	if m.ws.focus.view != viewSplit {
		t.Fatalf("view %v", m.ws.focus.view)
	}
	m = keys(t, m, "ctrl+l")
	if !m.ws.focus.previewFocus {
		t.Fatal("⌃l focuses the preview")
	}
	m = keys(t, m, "g", "g", "j", "j", "w")
	d, _ := m.previewDoc(m.ws.focus)
	if c := d.eng.Cursor(); c.Line != 2 {
		t.Fatalf("preview cursor %+v", c)
	}
	clip := systemClipboard.(*vim.MemClipboard)
	m = keys(t, m, "y", "y")
	if !strings.HasSuffix(clip.Text, "\n") || clip.Text == "\n" {
		t.Fatalf("preview yy %q", clip.Text)
	}
	m = keys(t, m, "esc", "esc")
	if m.ws.focus.previewFocus {
		t.Fatal("Esc returns to the editor")
	}
}

func TestPaneZoom(t *testing.T) {
	m, _ := openReader(t, "One")
	m = keys(t, m, "ctrl+w", "v")
	if len(m.ws.leaves()) != 2 {
		t.Fatal("split")
	}
	focus := m.ws.focus
	m = keys(t, m, "ctrl+w", "z")
	if m.zoomed() != focus {
		t.Fatal("⌃w z zooms the focused pane")
	}
	rects := m.layoutRects()
	if len(rects) != 1 || rects[focus] != (rect{0, 0, m.width, m.contentHeight()}) {
		t.Fatalf("zoomed rects %+v", rects)
	}
	v := stripAnsi(m.View())
	if !strings.Contains(v, "zoom") || strings.Contains(v, "│") && strings.Count(strings.Split(v, "\n")[3], "│") > 0 {
		t.Fatalf("zoomed view:\n%s", v)
	}
	if len(m.ws.leaves()) != 2 {
		t.Fatal("the other pane is hidden, not closed")
	}
	m = keys(t, m, "ctrl+w", "z")
	if m.zoomed() != nil || len(m.layoutRects()) != 2 {
		t.Fatal("⌃w z again restores the layout")
	}
	// moving to another pane unzooms
	m = keys(t, m, "ctrl+w", "z", "ctrl+w", "w")
	if m.zoomed() != nil || m.ws.focus == focus {
		t.Fatal("⌃w w unzooms and moves")
	}
	// a split unzooms
	m = keys(t, m, "ctrl+w", "z", "ctrl+w", "s")
	if m.zoomed() != nil || len(m.ws.leaves()) != 3 {
		t.Fatal("a split unzooms first")
	}
	// mouse: the ⤢ button in the header
	p := m.ws.focus
	r := m.layoutRects()[p]
	next, _ := m.Update(tea.MouseMsg{X: r.x + r.w - 2, Y: r.y, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
	m = next.(Model)
	if m.zoomed() != p {
		t.Fatal("clicking ⤢ zooms")
	}
	next, _ = m.Update(tea.MouseMsg{X: m.width - 2, Y: 0, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
	m = next.(Model)
	if m.zoomed() != nil {
		t.Fatal("clicking ⤡ unzooms")
	}
	// palette / :zoom from the editor
	m = keys(t, m, "i", ":")
	m = typeText(t, m, "zoom")
	m = keys(t, m, "enter")
	if m.zoomed() == nil {
		t.Fatal(":zoom")
	}
	// not persisted
	data, _ := os.ReadFile(layoutFile(m.rootDir))
	if strings.Contains(string(data), "zoom") {
		t.Fatalf("zoom persisted: %s", data)
	}
}

func TestClickFocusesAndActsInOnePress(t *testing.T) {
	m, _ := openReader(t, "One")
	m = keys(t, m, "ctrl+w", "v", "i") // right pane: editor
	left := m.ws.leaves()[0]
	r := m.layoutRects()[left]
	next, _ := m.Update(tea.MouseMsg{X: r.x + readerIndent + 2, Y: r.y + 1, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
	m = next.(Model)
	if m.ws.focus != left {
		t.Fatal("click focuses the pane")
	}
	d, _ := m.readerDoc(left)
	if c := d.eng.Cursor(); c.Col != 2 || c.Line != d.eng.Top {
		t.Fatalf("the same click placed the cursor: %+v", c)
	}
}

func TestZoomReducedMotionIsInstant(t *testing.T) {
	m, _ := openReader(t, "One")
	m = keys(t, m, "ctrl+w", "v", "ctrl+w", "z")
	if m.mo.frames != 0 || m.mo.busy() {
		t.Fatal("reduced motion: no frames")
	}
}
