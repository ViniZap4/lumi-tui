package ui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/vinizap/lumi/tui-client/theme"
)

// keymapSection is one block of the `?` overlay. The map mirrors the
// web client's keymap-help.ts and the Apple KeymapHelp (same keys,
// terminal glyphs).
type keymapSection struct {
	title string
	rows  [][2]string
}

var keymapHelp = []keymapSection{
	{"Navigate (floating explorer)", [][2]string{
		{"- <leader>e :explore", "open it (also ≡ notes, palette)"},
		{"⏎  ⌃v  ⌃s", "open here / vertical / horizontal split"},
		{"Esc  q", "close (click outside too)"},
		{"gb  h at book root", "books level (accounts → books)"},
		{"[  ]", "previous / next book"},
	}},
	{"Explore", [][2]string{
		{"j k  ↓ ↑", "move"},
		{"h  ←  backspace", "up a folder"},
		{"l  →  ⏎", "open / enter"},
		{"gg  G", "top / bottom"},
		{"⌃d ⌃u", "half page"},
		{"5j  12k", "count"},
		{"H  L", "folder history"},
		{"~  gh  zh", "book root"},
		{"/", "filter in place"},
		{"n  N", "next / prev match"},
		{"Esc", "clear filter / close"},
	}},
	{"Files", [][2]string{
		{"a", "new note (a/b/name, dir/)"},
		{"r", "rename / move (dir/name, file.md)"},
		{"d", "delete (confirm y)"},
		{"y  p", "yank / paste copy"},
		{"m", "move with picker"},
		{".", "show hidden"},
		{"zp", "preview column"},
		{"R", "refresh"},
	}},
	{"Panes", [][2]string{
		{"⌃h  ⌃l", "focus parent / list / preview"},
		{"⌃j  ⌃k", "scroll the preview"},
		{"mouse", "click select · double-click open · wheel scroll"},
	}},
	{"Note (reader: read-only vim cursor)", [][2]string{
		{"h j k l  w b e  0 ^ $", "move the cursor (counts: 5j 3w)"},
		{"gg  G  { }  ⌃e ⌃y", "top / bottom · paragraph · scroll a line"},
		{"⌃d ⌃u  d u  ⌃f ⌃b", "half / full page"},
		{"/ ?  n N  * #", "search (moves the cursor)"},
		{"v  V  ⌃v", "select characters / lines / block"},
		{"y  yy  yw  Y", "copy to the clipboard (OSC 52 fallback)"},
		{"p x d c …", "refused: the reader is read-only (E21)"},
		{"⏎  gf", "follow the link under the cursor"},
		{"⌃w f  ⌃w F  S s", "follow it in a vertical / horizontal split"},
		{"gx", "open the external link in the browser"},
		{"Tab ⇧Tab", "cursor to the next / previous link"},
		{"K", "link card (j/k scroll, ⏎ open, ⌃w f split)"},
		{"⏎  x on a task", "toggle the checkbox (saved)"},
		{"i  E", "edit (default editor / the other one)"},
		{"zv", "next view: reader → source → source+preview"},
		{"q", "close the pane (the last one: home)"},
		{"-", "floating explorer"},
		{"mouse", "click: cursor · on a link: follow (ctrl: split)"},
	}},
	{"Editor (built-in vim)", [][2]string{
		{"i a o I A O  R", "insert / replace"},
		{"v V ⌃v", "visual char / line / block (I A c r)"},
		{"d c y > < g~ gu gU", "operators + motions / text objects"},
		{"iw aw i\" a( ip …", "text objects"},
		{"\"a \"+ ⌃r", "registers (+ = clipboard, OSC 52)"},
		{".  u  ⌃r  q @", "repeat · undo / redo · macros"},
		{"/ ? n N * #", "search"},
		{":w :q :wq :x", "save (also autosaves) · leave"},
		{":s/a/b/g  :%s", "substitute with ranges"},
		{":edit!  :ext", "open in $EDITOR"},
		{":preview :reader", "views · :vs :sp :only"},
		{"jj (Settings)", "escape sequences · ~/.config/lumi/vimrc"},
	}},
	{"Panes and links", [][2]string{
		{"⌃w v  ⌃w s", "split vertical / horizontal"},
		{"⌃w q  ⌃w o  ⌃w =", "close · only · equalise"},
		{"⌃h ⌃j ⌃k ⌃l  ⌃w hjkl", "move between panes"},
		{"⌃w w  ⌃w p", "next / previous pane"},
		{"⌃w f  ⌃w F", "open link in a vertical / horizontal split"},
		{"⌃w z  :zoom", "zoom the pane / restore the layout"},
		{"⤢ · double-click header", "zoom with the mouse"},
		{"ctrl/alt-click", "open link in a split"},
		{"⌃l (source+preview)", "read the preview with a cursor (Esc back)"},
	}},
	{"Board and canvas", [][2]string{
		{"h l  j k", "column / card"},
		{"H L  J K", "move card"},
		{"a o  A", "new card · new column"},
		{"⏎ i  x  dd  /", "edit · done · delete · filter"},
		{"canvas: j k ⏎", "outline nodes · open file node"},
	}},
	{"App", [][2]string{
		{"space  :", "command palette (: = commands)"},
		{"[  ]", "previous / next book"},
		{"g1–9", "book by number"},
		{"gb", "books (in the explorer)"},
		{"gs", "sync details"},
		{"ga", "accounts and servers"},
		{",", "settings"},
		{"?", "this help"},
		{"q (home)", "quit"},
	}},
}

func helpLines(width int) []string {
	t := theme.Current
	head := lipgloss.NewStyle().Foreground(t.Primary).Bold(true)
	key := lipgloss.NewStyle().Foreground(t.Secondary).Bold(true)
	desc := lipgloss.NewStyle().Foreground(t.Text)
	var out []string
	for i, sec := range keymapHelp {
		if i > 0 {
			out = append(out, "")
		}
		out = append(out, head.Render(sec.title))
		for _, r := range sec.rows {
			out = append(out, "  "+key.Render(padRight(r[0], 22))+desc.Render(truncate(r[1], max(4, width-26))))
		}
	}
	return out
}

func (m Model) updateHelp(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	n := len(helpLines(60))
	switch msg.String() {
	case "q", "esc", "?", "ctrl+c":
		m.showHelp = false
	case "j", "down":
		m.helpScroll = clamp(m.helpScroll+1, 0, n-1)
	case "k", "up":
		m.helpScroll = clamp(m.helpScroll-1, 0, n-1)
	case "ctrl+d":
		m.helpScroll = clamp(m.helpScroll+10, 0, n-1)
	case "ctrl+u":
		m.helpScroll = clamp(m.helpScroll-10, 0, n-1)
	case "g":
		m.helpScroll = 0
	case "G":
		m.helpScroll = n - 1
	}
	return m, nil
}

func (m Model) renderHelp() string {
	t := theme.Current
	width := min(m.width-4, 64)
	lines := helpLines(width - 4)
	rows := max(4, m.contentHeight()-4)
	start := clamp(m.helpScroll, 0, max(0, len(lines)-rows))
	end := min(len(lines), start+rows)
	body := strings.Join(lines[start:end], "\n")
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(t.Border).
		Padding(0, 1).
		Width(width).
		Render(lipgloss.NewStyle().Foreground(t.TextDim).Render("keys") + "\n" + body)
}
