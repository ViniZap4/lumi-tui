package ui

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-runewidth"
	"github.com/vinizap/lumi/tui-client/theme"
)

// Screen chrome shared by every view:
//
//	┌ body (contentHeight rows) ─────────────────────────────┐
//	│ legend: context keys / which-key / prompt / confirm    │  (optional)
//	│ status: mode · path · position        book state · n   │
//	└────────────────────────────────────────────────────────┘

// legendVisible: the legend row is shown when enabled, and always while
// a prompt, a confirmation or the filter needs the line.
func (m Model) legendVisible() bool {
	if m.viewMode == ViewHome && !m.showInput && !m.showConfirm {
		return false
	}
	return m.legendOn || m.showInput || m.showConfirm || m.filterTyping
}

func (m Model) statusVisible() bool { return m.viewMode != ViewHome }

// bottomRows is how many rows the chrome takes.
func (m Model) bottomRows() int {
	n := 0
	if m.legendVisible() {
		n++
	}
	if m.statusVisible() {
		n++
	}
	return n
}

// contentHeight is the body height.
func (m Model) contentHeight() int { return max(1, m.height-m.bottomRows()) }

// legendContext resolves where the user is for the legend.
func (m Model) legendContext() LegendContext {
	switch {
	case m.showConfirm:
		return CtxConfirm
	case m.showInput:
		return CtxPrompt
	case m.showPalette:
		return CtxPalette
	case m.showHelp:
		return CtxHelp
	case m.showBooks:
		return CtxBooks
	case m.showBookDetails:
		return CtxDetails
	case m.filterTyping:
		return CtxFilter
	}
	switch m.viewMode {
	case ViewHome:
		return CtxHome
	case ViewConfig:
		return CtxSettings
	case ViewFullNote:
		if m.visualMode != VisualNone {
			return CtxVisual
		}
		return CtxReader
	}
	switch m.pane {
	case PaneParent:
		return CtxParent
	case PanePreview:
		return CtxPreview
	}
	return CtxExplorer
}

func promptLabel(mode string) string {
	switch mode {
	case "create":
		return "new"
	case "rename":
		return "rename"
	case "rename_folder":
		return "rename folder"
	case "config_editor":
		return "editor command"
	}
	return mode
}

// renderLegendRow is the legend line, or the prompt / confirmation /
// filter input when one is open.
func (m Model) renderLegendRow() string {
	t := theme.Current
	caret := lipgloss.NewStyle().Foreground(t.Accent).Render("▏")
	switch {
	case m.showConfirm:
		q := lipgloss.NewStyle().Foreground(t.Warning).Bold(true).Render(" " + m.confirmMsg)
		hint := lipgloss.NewStyle().Foreground(mutedColor).Render("  y confirm · n cancel")
		return padVisual(q+hint, m.width)
	case m.showInput:
		label := lipgloss.NewStyle().Foreground(t.Primary).Bold(true).Render(" " + promptLabel(m.inputMode) + " › ")
		val := lipgloss.NewStyle().Foreground(t.Text).Render(m.inputValue)
		hint := ""
		if m.inputMode == "create" {
			hint = lipgloss.NewStyle().Foreground(mutedColor).Render("   a/b/name · dir/ · ⏎ ok · Esc cancel")
		}
		return padVisual(label+val+caret+hint, m.width)
	case m.filterTyping:
		label := lipgloss.NewStyle().Foreground(t.Primary).Bold(true).Render(" / ")
		val := lipgloss.NewStyle().Foreground(t.Text).Render(m.filter)
		n := lipgloss.NewStyle().Foreground(mutedColor).Render(fmt.Sprintf("   %d/%d · ⏎ keep · Esc clear", len(m.items), len(m.allItems)))
		return padVisual(label+val+caret+n, m.width)
	}
	return renderLegendLine(legendFor(m.legendContext(), m.pendingInput()), m.width)
}

func (m Model) modeName() string {
	switch m.legendContext() {
	case CtxFilter:
		return "FILTER"
	case CtxPrompt:
		return "INPUT"
	case CtxConfirm:
		return "CONFIRM"
	case CtxPalette:
		return "PALETTE"
	case CtxHelp:
		return "HELP"
	case CtxBooks, CtxDetails:
		return "BOOKS"
	case CtxSettings:
		return "SETTINGS"
	case CtxReader:
		return "READ"
	case CtxVisual:
		if m.visualMode == VisualLine {
			return "V-LINE"
		}
		return "VISUAL"
	case CtxParent:
		return "PARENT"
	case CtxPreview:
		return "PREVIEW"
	}
	return "EXPLORE"
}

// renderStatusLine: mode pill, location, position, flags on the left;
// pending input, book sync state and pending pushes on the right.
func (m Model) renderStatusLine() string {
	t := theme.Current
	pill := lipgloss.NewStyle().Foreground(t.Background).Background(t.Primary).Bold(true).Render(" " + m.modeName() + " ")
	dim := lipgloss.NewStyle().Foreground(t.TextDim)
	var left []string
	switch m.viewMode {
	case ViewFullNote:
		if m.fullNote != nil {
			left = append(left, dim.Render(m.displayRel(m.fullNote.Path)))
			left = append(left, dim.Render(fmt.Sprintf("Ln %d Col %d", m.lineCursor+1, m.colCursor+1)))
		}
		if m.splitMode != "" {
			left = append(left, dim.Render("split"))
		}
	case ViewTree:
		left = append(left, dim.Render(m.displayPath()))
		if len(m.items) > 0 {
			left = append(left, dim.Render(fmt.Sprintf("%d/%d", m.cursor+1, len(m.items))))
		}
		if m.filter != "" && !m.filterTyping {
			left = append(left, lipgloss.NewStyle().Foreground(t.Accent).Render("/"+m.filter))
		}
		if m.showHidden {
			left = append(left, dim.Render("hidden"))
		}
		if m.yanked != nil {
			left = append(left, dim.Render("yanked: "+truncate(m.yanked.Name, 20)))
		}
	}
	if m.statusMsg != "" {
		left = append(left, lipgloss.NewStyle().Foreground(t.Info).Render(m.statusMsg))
	}
	l := pill + " " + strings.Join(left, dim.Render(" · "))

	var right []string
	if p := m.pendingInput(); p != "" {
		right = append(right, lipgloss.NewStyle().Foreground(t.Warning).Bold(true).Render(p))
	}
	if seg := m.renderBookSegment(); seg != "" {
		right = append(right, seg)
	} else {
		right = append(right, dim.Render("local"))
	}
	r := strings.Join(right, "  ") + " "
	gap := m.width - lipgloss.Width(l) - lipgloss.Width(r)
	if gap < 1 {
		l = truncateToVisualWidth(l, max(0, m.width-lipgloss.Width(r)-1))
		gap = max(1, m.width-lipgloss.Width(l)-lipgloss.Width(r))
	}
	return padVisual(l+strings.Repeat(" ", gap)+r, m.width)
}

// displayRel shows a path relative to the book root as "~/…".
func (m Model) displayRel(p string) string {
	r, err := filepath.Rel(m.rootDir, p)
	if err != nil || strings.HasPrefix(r, "..") {
		return p
	}
	return "~/" + filepath.ToSlash(r)
}

// fitHeight pads or cuts s to exactly h lines.
func fitHeight(s string, h int) string {
	lines := strings.Split(s, "\n")
	if len(lines) > h {
		lines = lines[:h]
	}
	for len(lines) < h {
		lines = append(lines, "")
	}
	return strings.Join(lines, "\n")
}

// cutVisual returns the part of an ANSI string between visual columns
// [from, to). Escape sequences before `from` are kept so styles carry
// over; wide runes that straddle an edge are replaced by spaces.
func cutVisual(s string, from, to int) string {
	var out strings.Builder
	col := 0
	inEsc := false
	for _, r := range s {
		if r == '\x1b' {
			inEsc = true
			out.WriteRune(r)
			continue
		}
		if inEsc {
			out.WriteRune(r)
			if (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') {
				inEsc = false
			}
			continue
		}
		w := runewidth.RuneWidth(r)
		switch {
		case col >= to:
		case col >= from && col+w <= to:
			out.WriteRune(r)
		case col+w > from && col < to:
			// straddles an edge
			for i := max(col, from); i < min(col+w, to); i++ {
				out.WriteByte(' ')
			}
		}
		col += w
	}
	return out.String()
}

// overlayBox draws box over base with its top-left at (x, y).
func overlayBox(base, box string, x, y, width int) string {
	lines := strings.Split(base, "\n")
	for i, bl := range strings.Split(box, "\n") {
		row := y + i
		if row < 0 || row >= len(lines) {
			continue
		}
		bw := lipgloss.Width(bl)
		line := padVisual(lines[row], width)
		lines[row] = cutVisual(line, 0, x) + "\x1b[0m" + bl + "\x1b[0m" + cutVisual(line, x+bw, width)
	}
	return strings.Join(lines, "\n")
}

// overlayCenter centres box horizontally, near the top third.
func (m Model) overlayCenter(base, box string, top bool) string {
	bw, bh := lipgloss.Width(box), lipgloss.Height(box)
	x := max(0, (m.width-bw)/2)
	y := max(0, (m.contentHeight()-bh)/2)
	if top {
		y = min(y, 2)
	}
	return overlayBox(base, box, x, y, m.width)
}
