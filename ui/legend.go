package ui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-runewidth"
)

// Command legend (which-key style): one line above the status line with
// the keys that matter in the current context, and the completions
// while a chord (`g…`, `z…`) or a count is pending. Same item lists and
// priority order as the web client's legend.ts, terminal glyphs.
// Pure: legendFor decides what to show, fitLegend what fits.

// LegendContext is where the user is.
type LegendContext string

const (
	CtxPreview  LegendContext = "preview" // explorer: preview column focused
	CtxParent   LegendContext = "parent"  // explorer: parent column focused
	CtxReader   LegendContext = "reader"
	CtxVisual   LegendContext = "visual"
	CtxFilter   LegendContext = "filter"
	CtxPrompt   LegendContext = "prompt"
	CtxConfirm  LegendContext = "confirm"
	CtxPalette  LegendContext = "palette"
	CtxBooks    LegendContext = "books"
	CtxSettings LegendContext = "settings"
	CtxHelp     LegendContext = "help"
	CtxDetails  LegendContext = "details"
	CtxHome     LegendContext = "home"
	// Part 2 (notes workspace)
	CtxEditor   LegendContext = "editor" // built-in editor, normal mode
	CtxInsert   LegendContext = "insert"
	CtxCmdline  LegendContext = "cmdline"
	CtxBoard    LegendContext = "board"
	CtxCanvas   LegendContext = "canvas"
	CtxWindow   LegendContext = "window" // Ctrl-w pending
	CtxFloat    LegendContext = "float"  // floating explorer (list column)
	CtxHover    LegendContext = "hover"
	CtxAccounts LegendContext = "accounts"
	CtxSplitPre LegendContext = "splitpreview"
)

var legendNoteG = []LegendItem{
	{"gg", "top"}, {"gf", "follow link"}, {"gx", "open URL"}, {"gb", "books"}, {"gs", "sync details"}, {"ga", "accounts"},
}

var legendNoteZ = []LegendItem{{"zv", "next view"}}

// legendWindow is the Ctrl-w which-key list (SPEC-PARTE2 §3).
var legendWindow = []LegendItem{
	{"v", "vertical split"}, {"s", "horizontal split"}, {"q", "close pane"}, {"o", "only this"},
	{"h j k l", "move"}, {"w p", "next / previous"}, {"f F", "link in split"}, {"=", "equalise"},
}

// LegendItem is one "keys label" pair.
type LegendItem struct {
	Keys, Label string
}

var legendG = []LegendItem{
	{"gg", "top"}, {"ge", "bottom"}, {"gh", "book root"}, {"gb", "books"},
	{"gs", "sync details"}, {"ga", "accounts"}, {"g1–9", "book n"},
}

var legendZ = []LegendItem{
	{"zp", "preview"}, {"zh", "book root"},
}

// legendFor returns the items for ctx with pending input (a chord
// prefix and/or a count), in priority order.
func legendFor(ctx LegendContext, pending string) []LegendItem {
	count, chord := splitCount(pending)
	switch ctx {
	case CtxFloat, CtxPreview, CtxParent:
		switch chord {
		case "g":
			return legendG
		case "z":
			return legendZ
		}
		if count != "" {
			return []LegendItem{{count + "j", "down " + count}, {count + "k", "up " + count}, {"Esc", "cancel"}}
		}
	}
	switch ctx {
	case CtxReader, CtxBoard, CtxCanvas:
		switch chord {
		case "g":
			return legendNoteG
		case "z":
			return legendNoteZ
		}
	}
	switch ctx {
	case CtxWindow:
		return legendWindow
	case CtxEditor:
		return []LegendItem{
			{"i a o", "insert"}, {"v V ⌃v", "visual"}, {":w", "save"}, {":q", "reader"}, {"u ⌃r", "undo/redo"},
			{"gf ⏎", "follow link"}, {"K", "link card"}, {"-", "explorer"}, {"⌃w", "panes"}, {":edit!", "$EDITOR"},
			{":preview", "split preview"}, {"zv", "next view"}, {"/ n", "search"}, {". ", "repeat"},
		}
	case CtxInsert:
		return []LegendItem{{"Esc", "normal"}, {"⌃w", "delete word"}, {"⌃r x", "insert register"}, {"⌃t ⌃d", "indent"}}
	case CtxCmdline:
		return []LegendItem{{"⏎", "run"}, {"Esc", "cancel"}, {"↑ ↓", "history"}, {"⌃r x", "register"}}
	case CtxBoard:
		return []LegendItem{
			{"h l", "column"}, {"j k", "card"}, {"H L", "move ←→"}, {"J K", "move ↑↓"}, {"a o", "new card"},
			{"⏎ i", "edit"}, {"x", "done"}, {"dd", "delete"}, {"A", "new column"}, {"/", "filter"},
			{"zv", "source"}, {"e E", "editors"}, {"-", "explorer"}, {"⌃w", "panes"}, {"q", "close"},
		}
	case CtxCanvas:
		return []LegendItem{
			{"j k Tab", "node"}, {"⏎ gf", "open file"}, {"⌃w f", "in split"}, {"gx", "open link"},
			{"zv", "JSON source"}, {"-", "explorer"}, {"⌃w", "panes"}, {"q", "close"},
		}
	case CtxFloat:
		return []LegendItem{
			{"⏎", "open here"}, {"⌃v ⌃s", "open in split"}, {"j k", "move"}, {"h l", "folder"},
			{"Esc q", "close"}, {"/", "filter"}, {"a", "new"}, {"r", "rename"}, {"d", "delete"},
			{"e i", "edit"}, {"y p", "copy"}, {"m", "move"}, {"gb", "books"}, {"space", "commands"},
			{"?", "keys"}, {"gg G", "top/end"}, {"H L", "history"}, {"zp", "preview col"},
			{"⌃d ⌃u", "half page"}, {".", "hidden"}, {"R", "refresh"}, {"[ ]", "prev/next book"},
			{"⌃h ⌃l", "panes"}, {"gs", "sync"}, {",", "settings"},
		}
	case CtxHover:
		return []LegendItem{{"⏎", "open"}, {"⌃w f", "open in split"}, {"j k", "scroll"}, {"Esc", "close"}}
	case CtxAccounts:
		return []LegendItem{{"a", "sign in"}, {"⏎", "sign in again"}, {"s", "sign out"}, {"d D", "remove account/server"}, {"w", "web address"}, {"Esc", "back"}}
	case CtxSplitPre:
		return []LegendItem{{"j k", "scroll"}, {"d u", "half page"}, {"g G", "top/end"}, {"q Esc ⌃h", "back to editor"}}
	case CtxParent:
		return []LegendItem{
			{"j k", "move"}, {"l ⏎", "enter"}, {"h", "up"}, {"⌃l Esc", "list"},
			{"space", "commands"}, {"?", "keys"},
		}
	case CtxPreview:
		return []LegendItem{
			{"j k", "scroll"}, {"⌃d ⌃u", "half page"}, {"gg G", "top/end"}, {"⏎ l", "open"},
			{"e i", "edit"}, {"⌃h Esc", "list"}, {"space", "commands"}, {"?", "keys"},
		}
	case CtxReader:
		return []LegendItem{
			{"j k", "scroll"}, {"d u", "half page"}, {"gg G", "top/end"}, {"i e", "edit"}, {"E", "other editor"},
			{"-", "explorer"}, {"Tab", "next link"}, {"⏎", "follow"}, {"K", "link card"}, {"q", "close"},
			{"zv", "next view"}, {"⌃w", "panes"}, {"y", "copy"}, {"/", "find"},
			{"space", "commands"}, {"?", "keys"}, {"[ ]", "prev/next book"}, {",", "settings"},
		}
	case CtxVisual:
		return []LegendItem{{"y", "copy"}, {"h j k l", "extend"}, {"w b e", "word"}, {"Esc", "normal"}}
	case CtxFilter:
		return []LegendItem{{"⏎", "keep filter"}, {"↑ ↓", "move"}, {"Esc", "clear"}, {"n N", "next/prev (after ⏎)"}}
	case CtxPrompt:
		return []LegendItem{{"⏎", "confirm"}, {"Esc", "cancel"}, {"a/b/name", "in folders"}, {"dir/", "folder"}, {"/x", "from root"}}
	case CtxConfirm:
		return []LegendItem{{"y ⏎", "confirm"}, {"n Esc", "cancel"}}
	case CtxPalette:
		return []LegendItem{{"↑ ↓ ⌃n ⌃p", "select"}, {"⏎", "run"}, {"Esc", "close"}, {":", "commands only"}, {"Tab", "next"}}
	case CtxBooks:
		return []LegendItem{
			{"j k", "move"}, {"h l", "accounts / books"}, {"⏎", "open"}, {"1–9", "book n"},
			{"J K", "reorder"}, {"s", "details"}, {"a", "accounts"}, {"Esc q", "close"},
		}
	case CtxSettings:
		return []LegendItem{{"j k", "move"}, {"h l", "change"}, {"⏎", "edit"}, {"Esc ,", "back"}}
	case CtxHelp:
		return []LegendItem{{"j k", "scroll"}, {"q Esc ?", "close"}}
	case CtxDetails:
		return []LegendItem{{"any key", "close"}}
	case CtxHome:
		return []LegendItem{
			{"-", "explore"}, {"j k", "recent"}, {"⏎ 1–9", "open"}, {"/", "search"}, {"gb", "books"},
			{"space", "commands"}, {"?", "keys"}, {",", "settings"}, {"q", "quit"},
		}
	}
	return nil
}

// splitCount separates a leading count from a chord ("12g" → "12","g").
func splitCount(p string) (count, chord string) {
	i := 0
	for i < len(p) && p[i] >= '0' && p[i] <= '9' {
		i++
	}
	return p[:i], p[i:]
}

// fitLegend picks which items fit on one line: greedy in priority
// order (a wide item that does not fit is skipped, smaller later ones
// may still fit), result in priority order. When something is left
// out, `reserve` columns stay free for the overflow marker.
func fitLegend(widths []int, available, gap, reserve int) (shown []int, overflow bool) {
	total := 0
	for i, w := range widths {
		total += w
		if i > 0 {
			total += gap
		}
	}
	if total <= available {
		for i := range widths {
			shown = append(shown, i)
		}
		return shown, false
	}
	room := available - reserve - gap
	used := 0
	for i, w := range widths {
		need := w
		if len(shown) > 0 {
			need += gap
		}
		if used+need <= room {
			shown = append(shown, i)
			used += need
		}
	}
	return shown, len(shown) < len(widths)
}

func legendItemWidth(it LegendItem) int {
	return runewidth.StringWidth(it.Keys) + 1 + runewidth.StringWidth(it.Label)
}

// renderLegendLine renders items into exactly width columns.
func renderLegendLine(items []LegendItem, width int) string {
	const gap = 3
	widths := make([]int, len(items))
	for i, it := range items {
		widths[i] = legendItemWidth(it)
	}
	avail := width - 2
	shown, overflow := fitLegend(widths, avail, gap, 2)
	keyStyle := lipgloss.NewStyle().Foreground(secondaryColor).Bold(true)
	descStyle := lipgloss.NewStyle().Foreground(mutedColor)
	var parts []string
	for _, i := range shown {
		parts = append(parts, keyStyle.Render(items[i].Keys)+" "+descStyle.Render(items[i].Label))
	}
	line := " " + strings.Join(parts, strings.Repeat(" ", gap))
	if overflow {
		line += strings.Repeat(" ", gap) + descStyle.Render("…")
	}
	return padVisual(line, width)
}

// padVisual pads (or cuts) an ANSI string to exactly width columns.
func padVisual(s string, width int) string {
	w := lipgloss.Width(s)
	if w > width {
		return truncateToVisualWidth(s, width)
	}
	return s + strings.Repeat(" ", width-w)
}
