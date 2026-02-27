package ui

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/vinizap/lumi/tui-client/theme"
)

func (m Model) View() string {
	if m.width == 0 {
		return "Loading..."
	}

	var content string

	// Confirm modal overlay (highest priority)
	if m.showConfirm {
		content = m.renderWithConfirmModal(m.renderBase())
	} else if m.showInput {
		content = m.renderWithInputModal(m.renderBase())
	} else if m.showSearch && !m.inFileSearch {
		// Global search modal overlay
		content = m.renderWithSearchModal(m.renderBase())
	} else {
		switch m.viewMode {
		case ViewHome:
			content = m.renderHome()
		case ViewConfig:
			content = m.renderConfig()
		case ViewFullNote:
			if m.showSearch && m.inFileSearch {
				content = m.renderWithInFileSearch()
			} else if m.showNav {
				content = m.renderWithNavModal(m.renderFullNote())
			} else if m.splitMode != "" && m.splitNote != nil {
				content = m.renderSplitView()
			} else {
				content = m.renderFullNote()
			}
		default:
			if m.showNav {
				content = m.renderWithNavModal(m.renderTree())
			} else {
				content = m.renderTree()
			}
		}
	}

	// Append persistent command bar (except for home view)
	if cmdBar := m.renderCommandBar(); cmdBar != "" {
		lines := strings.Split(content, "\n")
		maxContent := m.height - 1
		if len(lines) > maxContent {
			lines = lines[:maxContent]
		}
		content = strings.Join(lines, "\n") + "\n" + cmdBar
	}

	content = m.overlayToast(content)

	return m.fillBg(content)
}

// fillBg paints the entire terminal viewport with the theme background color.
//
// Strategy:
//  1. After every SGR full-reset in the content, re-emit the background colour
//     escape so it is never accidentally cleared by a foreground-only style.
//  2. For each line, measure its *visual* width (ANSI stripped), then pad with
//     background-coloured spaces to m.width — without using lipgloss Width()
//     rendering, which would truncate lines that are slightly wider than m.width.
//  3. Fill remaining rows to m.height so the bottom of the screen is covered.
func (m Model) fillBg(content string) string {
	bg := theme.Current.Background
	bgANSI := colorToAnsiBg(bg)
	if bgANSI == "" {
		// No true-color / 256-color bg available; nothing to do.
		return content
	}

	const sgr0 = "\x1b[0m"
	const sgrM = "\x1b[m"

	// Step 1 – make background sticky after every SGR reset.
	content = strings.ReplaceAll(content, sgr0, sgr0+bgANSI)
	content = strings.ReplaceAll(content, sgrM, sgrM+bgANSI)

	// Step 2 – pad each line to m.width (never truncate).
	reset := sgrM
	empty := bgANSI + strings.Repeat(" ", m.width) + reset

	lines := strings.Split(content, "\n")
	for i, l := range lines {
		vw := lipgloss.Width(l) // visual width, ANSI-stripped
		pad := m.width - vw
		if pad < 0 {
			pad = 0
		}
		lines[i] = bgANSI + l + strings.Repeat(" ", pad) + reset
	}

	// Step 3 – fill remaining height.
	for len(lines) < m.height {
		lines = append(lines, empty)
	}
	return strings.Join(lines[:m.height], "\n")
}

// colorToAnsiBg converts a lipgloss.Color to an ANSI SGR background escape.
// Supports hex (#rrggbb) and 256-colour palette (numeric string).
func colorToAnsiBg(c lipgloss.Color) string {
	s := string(c)
	if strings.HasPrefix(s, "#") && len(s) == 7 {
		r, _ := strconv.ParseInt(s[1:3], 16, 64)
		g, _ := strconv.ParseInt(s[3:5], 16, 64)
		b, _ := strconv.ParseInt(s[5:7], 16, 64)
		return fmt.Sprintf("\x1b[48;2;%d;%d;%dm", r, g, b)
	}
	n, err := strconv.Atoi(s)
	if err == nil {
		return fmt.Sprintf("\x1b[48;5;%dm", n)
	}
	return ""
}

// renderCommandBar returns a context-aware keybind bar for the bottom of the screen.
func (m Model) renderCommandBar() string {
	var keys []struct{ key, desc string }

	switch {
	case m.showConfirm:
		keys = []struct{ key, desc string }{
			{"y/enter", "confirm"},
			{"n/esc", "cancel"},
		}
	case m.showInput:
		keys = []struct{ key, desc string }{
			{"enter", "confirm"},
			{"esc", "cancel"},
		}
	case m.showSearch && !m.inFileSearch:
		keys = []struct{ key, desc string }{
			{"ctrl+f", "toggle"},
			{"up/dn", "navigate"},
			{"enter", "open"},
			{"esc", "close"},
		}
	case m.showNav:
		if m.pendingMoveNote != nil || m.pendingMoveFolder != "" {
			keys = []struct{ key, desc string }{
				{"hjkl", "navigate"},
				{"enter", "move here"},
				{"esc", "cancel"},
			}
		} else {
			keys = []struct{ key, desc string }{
				{"hjkl", "navigate"},
				{"enter", "open"},
				{"s/S", "split"},
				{"esc", "close"},
			}
		}
	default:
		switch m.viewMode {
		case ViewTree:
			keys = []struct{ key, desc string }{
				{"j/k", "move"},
				{"l/enter", "open"},
				{"h", "back"},
				{"n", "new note"},
				{"N", "new folder"},
				{"r", "rename"},
				{"d", "delete"},
				{"m", "move"},
				{"/", "search"},
				{"q", "quit"},
			}
		case ViewFullNote:
			keys = []struct{ key, desc string }{
				{"j/k", "move"},
				{"w/b", "word"},
				{"v/V", "visual"},
				{"y", "yank"},
				{"e", "edit"},
				{"enter", "follow"},
				{"L", "link"},
				{"t", "tree"},
				{"/", "search"},
				{"esc", "back"},
			}
		case ViewConfig:
			keys = []struct{ key, desc string }{
				{"j/k", "move"},
				{"h/l", "change"},
				{"enter", "select"},
				{"esc", "back"},
			}
		default:
			return ""
		}
	}

	var parts []string
	for _, k := range keys {
		key := lipgloss.NewStyle().Foreground(secondaryColor).Bold(true).Render(k.key)
		desc := lipgloss.NewStyle().Foreground(mutedColor).Render(" " + k.desc)
		parts = append(parts, key+desc)
	}

	return lipgloss.NewStyle().Padding(0, 1).Render(strings.Join(parts, "  "))
}

func (m Model) renderBase() string {
	switch m.viewMode {
	case ViewHome:
		return m.renderHome()
	case ViewConfig:
		return m.renderConfig()
	case ViewFullNote:
		return m.renderFullNote()
	default:
		return m.renderTree()
	}
}

// overlayToast renders a toast notification at the top-right corner of the terminal.
func (m Model) overlayToast(content string) string {
	if m.toastMsg == "" {
		return content
	}

	var style lipgloss.Style
	switch m.toastLevel {
	case ToastSuccess:
		style = ToastSuccessStyle
	case ToastError:
		style = ToastErrorStyle
	case ToastInfo:
		style = ToastInfoStyle
	default:
		style = ToastInfoStyle
	}

	toast := style.Render(m.toastMsg)
	toastW := lipgloss.Width(toast)

	if toastW+2 > m.width {
		return content
	}

	lines := strings.Split(content, "\n")
	if len(lines) < 2 {
		return content
	}

	// Place toast on the second line, right-aligned with 1-col margin
	row := 1
	lineRunes := []rune(stripAnsi(lines[row]))
	lineW := len(lineRunes)

	startCol := m.width - toastW - 1
	if startCol < 0 {
		startCol = 0
	}

	// Pad the line if it's shorter than the toast start position
	if lineW < startCol {
		lines[row] = lines[row] + strings.Repeat(" ", startCol-lineW)
	}

	// Overlay the toast onto the line
	prefix := truncateToVisualWidth(lines[row], startCol)
	lines[row] = prefix + toast

	return strings.Join(lines, "\n")
}

// stripAnsi removes ANSI escape sequences from a string.
func stripAnsi(s string) string {
	var out []rune
	inEsc := false
	for _, r := range s {
		if r == '\x1b' {
			inEsc = true
			continue
		}
		if inEsc {
			if (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') {
				inEsc = false
			}
			continue
		}
		out = append(out, r)
	}
	return string(out)
}

// truncateToVisualWidth returns the prefix of an ANSI-containing string
// that occupies exactly w visual columns.
func truncateToVisualWidth(s string, w int) string {
	var out strings.Builder
	col := 0
	inEsc := false
	for _, r := range s {
		if col >= w && !inEsc {
			break
		}
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
		out.WriteRune(r)
		col++
	}
	return out.String()
}
