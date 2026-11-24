package ui

import (
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/vinizap/lumi/tui-client/canvas"
	"github.com/vinizap/lumi/tui-client/kanban"
	"github.com/vinizap/lumi/tui-client/theme"
)

// Note kinds (markdown, kanban board, JSON canvas) get distinct glyphs
// in the explorer rows, the palette, pane headers and the breadcrumbs,
// and a subtle " · canvas" / " · quadro" suffix when two items in a
// list share a title. The explorer preview shows a canvas as its
// outline and a board as a mini board, never raw JSON.

type noteKind int

const (
	kindMarkdown noteKind = iota
	kindKanban
	kindCanvas
)

// kindGlyphs are the Unicode glyphs; asciiGlyphs the fallbacks
// (LUMI_ASCII=1, TERM=linux / vt*, or a non-UTF-8 locale).
var (
	kindGlyphs  = [...]string{"▤", "▥", "◈"}
	asciiGlyphs = [...]string{"-", "#", "%"}
	kindNames   = [...]string{"nota", "quadro", "canvas"}
)

var asciiOnly = sync.OnceValue(func() bool {
	if v := os.Getenv("LUMI_ASCII"); v != "" && v != "0" && v != "false" {
		return true
	}
	t := os.Getenv("TERM")
	if t == "linux" || strings.HasPrefix(t, "vt") {
		return true
	}
	for _, k := range []string{"LC_ALL", "LC_CTYPE", "LANG"} {
		if v := os.Getenv(k); v != "" {
			u := strings.ToUpper(v)
			return !strings.Contains(u, "UTF-8") && !strings.Contains(u, "UTF8") && u != "C.UTF-8"
		}
	}
	return false
})

func (k noteKind) glyph() string {
	if asciiOnly() {
		return asciiGlyphs[k]
	}
	return kindGlyphs[k]
}

func (k noteKind) String() string { return kindNames[k] }

type kindEntry struct {
	mod  time.Time
	size int64
	kind noteKind
}

var kindCache sync.Map // path → kindEntry

// kindOf is the kind of the note at path (cached by mtime and size;
// a board is detected from the frontmatter in the first 4 KB).
func kindOf(path string) noteKind {
	if isCanvasPath(path) {
		return kindCanvas
	}
	st, err := os.Stat(path)
	if err != nil {
		return kindMarkdown
	}
	if v, ok := kindCache.Load(path); ok {
		e := v.(kindEntry)
		if e.mod.Equal(st.ModTime()) && e.size == st.Size() {
			return e.kind
		}
	}
	k := kindMarkdown
	if f, err := os.Open(path); err == nil {
		head := make([]byte, 4096)
		n, _ := io.ReadFull(f, head)
		f.Close()
		if kanban.IsBoard(string(head[:n])) {
			k = kindKanban
		}
	}
	kindCache.Store(path, kindEntry{mod: st.ModTime(), size: st.Size(), kind: k})
	return k
}

// itemKind is an explorer item's kind (folders are markdown here; the
// caller draws folders on its own).
func itemKind(it Item) noteKind {
	if it.IsFolder {
		return kindMarkdown
	}
	return kindOf(it.Path)
}

// kindStyle colours a glyph by kind.
func kindStyle(k noteKind) lipgloss.Style {
	t := theme.Current
	switch k {
	case kindCanvas:
		return lipgloss.NewStyle().Foreground(t.Accent)
	case kindKanban:
		return lipgloss.NewStyle().Foreground(t.Warning)
	}
	return lipgloss.NewStyle().Foreground(t.TextDim)
}

// dupTitles reports, for a list, which items share a title with
// another item (case-insensitive): those get a kind suffix.
func dupTitles(items []Item) map[int]bool {
	seen := map[string][]int{}
	for i, it := range items {
		if it.IsFolder {
			continue
		}
		k := strings.ToLower(strings.TrimSpace(it.Name))
		seen[k] = append(seen[k], i)
	}
	out := map[int]bool{}
	for _, idx := range seen {
		if len(idx) > 1 {
			for _, i := range idx {
				out[i] = true
			}
		}
	}
	return out
}

// kindSuffix is " · canvas" (etc.) for a duplicated title.
func kindSuffix(it Item) string { return " · " + itemKind(it).String() }

// --- explorer preview: canvas outline and mini board ------------------

// renderCanvasPreview is a canvas as an outline: groups → nodes (text
// excerpts, file nodes by title), then the edges "A → B (label)".
func renderCanvasPreview(path string, width, rows int) []string {
	t := theme.Current
	data, err := os.ReadFile(path)
	if err != nil {
		return []string{lipgloss.NewStyle().Foreground(t.Error).Render(" não foi possível ler")}
	}
	c, err := canvas.Parse(data)
	if err != nil {
		return []string{lipgloss.NewStyle().Foreground(t.Warning).Render(" ⚠ JSON inválido")}
	}
	if len(c.Nodes) == 0 {
		return []string{lipgloss.NewStyle().Foreground(t.TextDim).Render(" canvas vazio")}
	}
	glyph := map[canvas.RowKind]string{canvas.RowGroup: "▣ ", canvas.RowText: "¶ ", canvas.RowFile: "◇ ", canvas.RowLink: "↗ ", canvas.RowEdge: "", canvas.RowHeader: ""}
	if asciiOnly() {
		glyph = map[canvas.RowKind]string{canvas.RowGroup: "+ ", canvas.RowText: "* ", canvas.RowFile: "> ", canvas.RowLink: "@ ", canvas.RowEdge: "", canvas.RowHeader: ""}
	}
	var out []string
	groups, nodes := 0, 0
	for _, n := range c.Nodes {
		if n.Type == "group" {
			groups++
		} else {
			nodes++
		}
	}
	out = append(out, lipgloss.NewStyle().Foreground(t.TextDim).Render(fmt.Sprintf(" %d nós · %d grupos · %d ligações", nodes, groups, len(c.Edges))), "")
	for _, r := range c.Outline() {
		st := lipgloss.NewStyle().Foreground(t.Text)
		label := r.Label
		switch r.Kind {
		case canvas.RowGroup:
			st = st.Bold(true).Foreground(t.Secondary)
		case canvas.RowFile:
			st = st.Foreground(t.Accent)
			label = strings.TrimSuffix(label[strings.LastIndex(label, "/")+1:], ".md")
		case canvas.RowHeader:
			st = st.Bold(true).Foreground(t.Primary)
			out = append(out, "")
		case canvas.RowEdge:
			st = st.Foreground(t.TextDim)
		}
		text := strings.Repeat("  ", r.Depth) + glyph[r.Kind] + label
		out = append(out, " "+st.Render(truncate(text, width-2)))
		if len(out) >= rows {
			break
		}
	}
	return out
}

// renderBoardPreview is a mini board: columns side by side with their
// first cards.
func renderBoardPreview(path string, width, rows int) []string {
	t := theme.Current
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	b := kanban.Parse(string(data))
	if len(b.Columns) == 0 {
		return []string{lipgloss.NewStyle().Foreground(t.TextDim).Render(" quadro vazio")}
	}
	n := len(b.Columns)
	colW := max(10, (width-1)/n-1)
	shown := min(n, max(1, (width-1)/(colW+1)))
	cardRows := max(1, rows-3)
	cols := make([][]string, shown)
	for ci := 0; ci < shown; ci++ {
		col := b.Columns[ci]
		h := lipgloss.NewStyle().Bold(true).Foreground(t.Secondary).Render(truncate(col.Title, colW-4)) +
			lipgloss.NewStyle().Foreground(t.TextDim).Render(fmt.Sprintf(" %d", len(col.Cards)))
		lines := []string{h, lipgloss.NewStyle().Foreground(t.Separator).Render(strings.Repeat("─", colW))}
		box, done := "☐ ", "☑ "
		if asciiOnly() {
			box, done = "[ ] ", "[x] "
		}
		for i, cd := range col.Cards {
			if len(lines) >= cardRows+1 {
				lines = append(lines, lipgloss.NewStyle().Foreground(t.TextDim).Italic(true).Render(fmt.Sprintf("… +%d", len(col.Cards)-i)))
				break
			}
			mark, st := box, lipgloss.NewStyle().Foreground(t.Text)
			if cd.Done {
				mark, st = done, st.Foreground(t.TextDim).Strikethrough(true)
			}
			lines = append(lines, st.Render(truncate(mark+stripAnsi(cd.Title), colW)))
		}
		cols[ci] = fitBlock(lines, colW, len(lines))
	}
	h := 0
	for _, c := range cols {
		h = max(h, len(c))
	}
	sep := lipgloss.NewStyle().Foreground(t.Separator).Render("│")
	out := make([]string, 0, h+1)
	for r := 0; r < h; r++ {
		var row strings.Builder
		row.WriteString(" ")
		for i, c := range cols {
			if i > 0 {
				row.WriteString(sep)
			}
			cell := ""
			if r < len(c) {
				cell = c[r]
			}
			row.WriteString(padVisual(cell, colW))
		}
		out = append(out, row.String())
	}
	if shown < n {
		out = append(out, lipgloss.NewStyle().Foreground(t.TextDim).Italic(true).Render(fmt.Sprintf(" + %d colunas", n-shown)))
	}
	return out
}
