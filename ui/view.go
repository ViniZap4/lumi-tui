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

	h := m.contentHeight()
	var body string
	switch {
	case m.acct != nil:
		body = m.renderAccounts()
	case m.showBookDetails && m.bookMgr != nil:
		body = m.renderBookDetails()
	default:
		switch m.viewMode {
		case ViewConfig:
			body = m.renderConfig()
		case ViewFullNote:
			body = m.renderWorkspace()
		default:
			body = m.renderHome()
		}
		body = fitHeight(body, h)
		if m.viewMode != ViewConfig {
			// the overlay draws while open and while it shrinks away
			body = m.renderFloatExplorer(body)
		}
		if m.showNav {
			body = m.renderWithNavModal(body)
		}
		if m.showSearch {
			body = m.renderWithSearchModal(body)
		}
	}
	body = fitHeight(body, h)
	if m.hover != nil && m.viewMode == ViewFullNote {
		body = fitHeight(m.renderHover(body), h)
	}
	if m.showPalette {
		body = m.overlayCenter(body, m.renderPalette(), true)
	} else if m.showHelp {
		body = m.overlayCenter(body, m.renderHelp(), false)
	}

	content := body
	if m.legendVisible() {
		content += "\n" + m.renderLegendRow()
	}
	if m.statusVisible() {
		content += "\n" + m.renderStatusLine()
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
	// slides in from the right edge
	slide := 0
	if a, _ := m.mo.get(animToast); a.active {
		slide = int((1 - a.value(0, 1.2)) * float64(toastW+2))
		if slide > 0 {
			toast = truncateToVisualWidth(toast, max(0, toastW-slide))
		}
	}

	lines := strings.Split(content, "\n")
	if len(lines) < 2 {
		return content
	}

	// Place toast on the second line, right-aligned with 1-col margin
	row := 1
	lineRunes := []rune(stripAnsi(lines[row]))
	lineW := len(lineRunes)

	startCol := m.width - toastW - 1 + max(0, slide)
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
