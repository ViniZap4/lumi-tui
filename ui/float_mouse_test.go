package ui

import (
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/mattn/go-runewidth"
)

// explorerOverNote opens One, then the floating explorer on name.
func explorerOverNote(t *testing.T, name string) Model {
	t.Helper()
	dir := notesVault(t)
	mustWrite(t, filepath.Join(dir, "map.md"), "---\ntitle: map\n---\n\nsame title as the canvas\n")
	m := newTreeModel(t, dir)
	m.width, m.height = 120, 34
	m = openNamed(t, m, "One")
	m = keys(t, m, "-")
	if !m.floatExp {
		t.Fatal("- opens the explorer")
	}
	return moveTo(t, keys(t, m, "g", "g"), name)
}

func mpress(m Model, x, y int, b tea.MouseButton, ctrl bool) Model {
	next, cmd := m.Update(tea.MouseMsg{X: x, Y: y, Button: b, Action: tea.MouseActionPress, Ctrl: ctrl})
	return settle(next.(Model), cmd)
}

// selectedRowXY is the screen position of the selected list row and
// the x of its inline action a.
func selectedRowXY(t *testing.T, m Model, a int) (int, int) {
	t.Helper()
	inner, off := m.explorerFrame()
	_, cw, _, _ := treeColumnWidths(inner.width, inner.previewHidden)
	iw := max(4, cw-1)
	text, spans := rowActionsText(iw)
	if text == "" {
		t.Fatal("no room for row actions")
	}
	start, _ := listWindow(inner.cursor, len(inner.items), inner.contentHeight()-treeTop)
	y := off.y + treeTop + inner.cursor - start
	x := off.x + inner.centerColX() + iw + 1 - runewidth.StringWidth(text) + spans[a][0]
	return x, y
}

func TestExplorerKindGlyphsAndDuplicateTitles(t *testing.T) {
	m := explorerOverNote(t, "Board")
	v := stripAnsi(m.View())
	for _, want := range []string{"▥ Board", "◈ map · canvas", "▤ map · nota", "▤ One"} {
		if !strings.Contains(v, want) {
			t.Errorf("explorer missing %q:\n%s", want, v)
		}
	}
	// the board previews as a mini board
	if !strings.Contains(v, "Todo") || !strings.Contains(v, "Done") || !strings.Contains(v, "☐ alpha") {
		t.Errorf("board preview:\n%s", v)
	}
	// pane header and status line carry the kind glyph
	m = keys(t, m, "esc")
	if v := stripAnsi(m.View()); !strings.Contains(v, "▤ One") || !strings.Contains(v, "▤ ~/one.md") {
		t.Errorf("header / status glyph:\n%s", v)
	}
}

func TestExplorerCanvasPreviewIsAnOutline(t *testing.T) {
	m := explorerOverNote(t, "One")
	for i, it := range m.items {
		if strings.HasSuffix(it.Path, ".canvas") {
			m.cursor = i
		}
	}
	v := stripAnsi(m.View())
	if strings.Contains(v, `"nodes"`) || strings.Contains(v, "fromNode") {
		t.Fatalf("raw JSON in the preview:\n%s", v)
	}
	for _, want := range []string{"Plano", "ideia", "ideia → two.md  (usa)", "2 nós · 1 grupos · 1 ligações"} {
		if !strings.Contains(v, want) {
			t.Errorf("canvas preview missing %q:\n%s", want, v)
		}
	}
}

func TestExplorerInlineActionsAndContextMenu(t *testing.T) {
	m := explorerOverNote(t, "Two")
	x, y := selectedRowXY(t, m, 1) // ⇥ right
	m = mpress(m, x, y, tea.MouseButtonLeft, false)
	if m.floatExp || len(m.ws.leaves()) != 2 || m.ws.root.dir != 'v' || m.ws.focus.buf.note.Title != "Two" {
		t.Fatalf("⇥ right: float=%v leaves=%d", m.floatExp, len(m.ws.leaves()))
	}
	// right-click → menu → "Open below"
	m = keys(t, m, "-", "g", "g")
	m = moveTo(t, m, "Board")
	_, y = selectedRowXY(t, m, 0)
	inner, off := m.explorerFrame()
	m = mpress(m, off.x+inner.centerColX()+4, y, tea.MouseButtonRight, false)
	if m.ctxMenu == nil {
		t.Fatal("right-click opens the context menu")
	}
	if v := stripAnsi(m.View()); !strings.Contains(v, "Open right") || !strings.Contains(v, "Open below") {
		t.Fatalf("menu not drawn:\n%s", v)
	}
	_, r := m.ctxMenuBox()
	m = mpress(m, r.x+2, r.y+3, tea.MouseButtonLeft, false)
	if m.floatExp || m.ws.focus.buf.note.Title != "Board" || len(m.ws.leaves()) != 3 {
		t.Fatalf("menu Open below: float=%v leaves=%d focus=%s", m.floatExp, len(m.ws.leaves()), m.ws.focus.buf.note.Title)
	}
	// keyboard in the menu: Esc closes it, the explorer stays
	m = keys(t, m, "-")
	m = mpress(m, off.x+inner.centerColX()+4, y, tea.MouseButtonRight, false)
	m = keys(t, m, "esc")
	if m.ctxMenu != nil || !m.floatExp {
		t.Fatal("Esc closes only the menu")
	}
}

func TestExplorerFooterLegendIsClickable(t *testing.T) {
	m := explorerOverNote(t, "Two")
	r := m.overlayRect()
	foot, spans := m.explorerFooter(r.w - 2)
	if !strings.Contains(stripAnsi(foot), "⏎ open here") || !strings.HasSuffix(strings.TrimRight(stripAnsi(foot), " "), "5 itens") {
		t.Fatalf("footer %q", stripAnsi(foot))
	}
	if v := stripAnsi(m.View()); !strings.Contains(v, "⌃v ⌃s open in split") {
		t.Fatalf("footer not drawn:\n%s", v)
	}
	// clicking "/ filter" presses /
	for _, s := range spans {
		if len(s.keys) == 1 && s.keys[0] == "/" {
			m = mpress(m, r.x+1+s.x0, r.y+r.h-2, tea.MouseButtonLeft, false)
		}
	}
	if !m.filterTyping {
		t.Fatal("footer click on / filter did not start the filter")
	}
	m = keys(t, m, "esc", "g", "g")
	// clicking "⌃v ⌃s open in split" opens a vertical split
	m = moveTo(t, m, "Two")
	_, spans = m.explorerFooter(r.w - 2)
	for _, s := range spans {
		if len(s.keys) == 1 && s.keys[0] == "ctrl+v" {
			m = mpress(m, r.x+1+s.x0, r.y+r.h-2, tea.MouseButtonLeft, false)
			break
		}
	}
	if m.floatExp || len(m.ws.leaves()) != 2 {
		t.Fatal("footer ⌃v click opens a split")
	}
}
