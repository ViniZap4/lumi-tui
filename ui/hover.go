package ui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/vinizap/lumi/tui-client/domain"
	"github.com/vinizap/lumi/tui-client/theme"
)

// hoverCard is the `K` link card (SPEC-PARTE2 §4): the rendered note
// (first ~40 lines, scrollable), title, folder and updated time; or a
// URL card for external links (no fetch).
type hoverCard struct {
	note   *domain.Note
	url    string
	from   *notePane
	scroll int
	win    bool // Ctrl-w pressed inside the card
}

const hoverMaxLines = 40

func (m Model) openHover(p *notePane, l noteLink) (Model, tea.Cmd) {
	if l.external {
		m.hover = &hoverCard{url: l.target, from: p}
		return m, m.startOverlayAnim()
	}
	n := m.resolveLink(p.buf.path, l.target)
	if n == nil {
		m.statusMsg = "nota não encontrada: " + l.target
		return m, nil
	}
	m.hover = &hoverCard{note: n, from: p}
	return m, m.startOverlayAnim()
}

func (m Model) updateHover(key string) (tea.Model, tea.Cmd) {
	h := m.hover
	if h.win {
		h.win = false
		switch key {
		case "f", "enter", "ctrl+f":
			return m.hoverOpen("vsplit")
		case "F":
			return m.hoverOpen("hsplit")
		}
		return m, nil
	}
	switch key {
	case "esc", "q", "K":
		m.hover = nil
	case "j", "down":
		h.scroll++
	case "k", "up":
		h.scroll = max(0, h.scroll-1)
	case "enter":
		return m.hoverOpen("replace")
	case "ctrl+w":
		h.win = true
	case "ctrl+c":
		return m, tea.Quit
	}
	return m, nil
}

func (m Model) hoverOpen(how string) (tea.Model, tea.Cmd) {
	h := m.hover
	m.hover = nil
	if h.url != "" {
		openURLFn(h.url)
		m.statusMsg = "aberto no navegador"
		return m, nil
	}
	if h.from != nil && m.ws != nil && m.ws.nodeOf(h.from) != nil {
		m.setFocus(h.from)
	}
	m.syncFocusOut()
	return m, m.openNoteIn(h.note, how)
}

// renderHover draws the card centred over the notes.
func (m Model) renderHover(base string) string {
	h := m.hover
	t := theme.Current
	w := min(72, m.width-6)
	if f := m.overlayFrame; f > 0 {
		w -= f * 4
	}
	w = max(30, w)
	dim := lipgloss.NewStyle().Foreground(t.TextDim)
	var b strings.Builder
	if h.url != "" {
		b.WriteString(lipgloss.NewStyle().Bold(true).Foreground(t.Primary).Render("link externo"))
		b.WriteString("\n\n")
		b.WriteString(lipgloss.NewStyle().Foreground(t.Accent).Render(wrapHard(h.url, w-4)))
		b.WriteString("\n\n")
		b.WriteString(dim.Render("⏎ abrir no navegador · Esc fechar"))
	} else {
		b.WriteString(lipgloss.NewStyle().Bold(true).Foreground(t.Primary).Render(truncate(h.note.Title, w-4)))
		b.WriteString("\n")
		b.WriteString(dim.Render(truncate(m.noteMeta(h.note), w-4)))
		b.WriteString("\n")
		b.WriteString(lipgloss.NewStyle().Foreground(t.Separator).Render(strings.Repeat("─", w-4)))
		b.WriteString("\n")
		body, _ := readBody(h.note.Path)
		src := strings.Split(body, "\n")
		if len(src) > hoverMaxLines {
			src = src[:hoverMaxLines]
		}
		lines := strings.Split(strings.TrimRight(m.prose.render(strings.Join(src, "\n"), w-4), "\n"), "\n")
		rows := min(16, max(4, m.contentHeight()-10))
		h.scroll = clamp(h.scroll, 0, max(0, len(lines)-rows))
		end := min(len(lines), h.scroll+rows)
		b.WriteString(strings.Join(lines[h.scroll:end], "\n"))
		for i := end - h.scroll; i < rows; i++ {
			b.WriteString("\n")
		}
		b.WriteString("\n")
		b.WriteString(dim.Render("⏎ abrir · ⌃w f dividir · j/k rolar · Esc fechar"))
	}
	box := lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(t.Accent).
		Background(t.Background).Padding(0, 1).Width(w).Render(b.String())
	return m.overlayCenter(base, box, false)
}

func wrapHard(s string, w int) string {
	if w < 1 {
		return s
	}
	r := []rune(s)
	var out []string
	for len(r) > w {
		out = append(out, string(r[:w]))
		r = r[w:]
	}
	return strings.Join(append(out, string(r)), "\n")
}
