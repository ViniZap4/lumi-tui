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
	{"Note", [][2]string{
		{"⏎  l", "read"},
		{"i  e", "edit in $EDITOR"},
		{"q  Esc", "back to explorer"},
		{"j k  ⌃d ⌃u", "move / half page"},
		{"gg  G", "top / bottom"},
		{"v  V  y", "visual / yank"},
		{"⏎", "follow link / toggle task"},
		{"s  S", "split with a note"},
	}},
	{"App", [][2]string{
		{"space  :", "command palette (: = commands)"},
		{"[  ]", "previous / next book"},
		{"g1–9", "book by number"},
		{"gb", "all books"},
		{"gs", "sync details"},
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
