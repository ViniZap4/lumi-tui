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
		{"Esc", "clear filter / back to list"},
		{"q", "quit"},
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
	{"Note (reader)", [][2]string{
		{"⏎  l", "read"},
		{"i  e", "edit with the default editor (Settings)"},
		{"E", "edit with the other one (built-in ↔ $EDITOR)"},
		{"j k  d u  f b", "scroll"},
		{"gg  G", "top / bottom"},
		{"Tab ⇧Tab", "focus next / previous link"},
		{"⏎  gf", "follow link · gx open URL"},
		{"K", "link card (j/k scroll, ⏎ open, ⌃w f split)"},
		{"zv", "next view: reader → source → source+preview"},
		{"y  /  n N", "copy text · find"},
		{"q  Esc", "back to explorer"},
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
		{"-  <leader>e", "floating explorer (⏎ here, ⌃v / ⌃s split)"},
		{"ctrl/alt-click", "open link in a split"},
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
		{"gb", "all books"},
		{"gs", "sync details"},
		{"ga", "accounts and servers"},
		{",", "settings"},
		{"?", "this help"},
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
			out = append(out, "  "+key.Render(padRight(r[0], 18))+desc.Render(truncate(r[1], max(4, width-22))))
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
