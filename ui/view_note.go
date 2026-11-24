package ui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/vinizap/lumi/tui-client/theme"
)

// imageLinePrefix marks display lines that contain pre-rendered image output
// (ANSI art from external tools or native protocol escape sequences).
// These lines must bypass the content styling pipeline entirely.
const imageLinePrefix = "\x00IMG\x00"

// visualRange describes which columns of a line fall inside the visual selection.
type visualRange struct {
	active   bool
	full     bool // entire line is selected (VisualLine or middle line in VisualChar)
	startCol int  // first selected column (0-based)
	endCol   int  // last selected column, or -1 for "to end of line"
}

// --- Helpers ---

// codeBlockLines returns which raw line indices are inside fenced code blocks.
func codeBlockLines(lines []string) map[int]bool {
	result := map[int]bool{}
	inside := false
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") {
			result[i] = true
			inside = !inside
			continue
		}
		if inside {
			result[i] = true
		}
	}
	return result
}

// mdLineStyle returns the theme-aware style for a markdown line.
func mdLineStyle(line string, inCodeBlock bool) lipgloss.Style {
	t := theme.Current
	if inCodeBlock {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") {
			// Fence lines: dim with code-block background tint.
			return lipgloss.NewStyle().Foreground(t.Muted).Background(codeBlockBg)
		}
		// Code content: accent color on the same subtle tint so the
		// fenced range reads as one continuous block instead of two
		// hard-edged bands (fence + body) with different shades.
		return lipgloss.NewStyle().Foreground(t.Accent).Background(codeBlockBg)
	}
	trimmed := strings.TrimSpace(line)
	switch {
	case strings.HasPrefix(trimmed, "# "):
		return lipgloss.NewStyle().Bold(true).Foreground(primaryColor)
	case strings.HasPrefix(trimmed, "## "):
		return lipgloss.NewStyle().Bold(true).Foreground(secondaryColor)
	case strings.HasPrefix(trimmed, "### "),
		strings.HasPrefix(trimmed, "#### "),
		strings.HasPrefix(trimmed, "##### "),
		strings.HasPrefix(trimmed, "###### "):
		return lipgloss.NewStyle().Bold(true).Foreground(accentColor)
	case strings.HasPrefix(trimmed, "> "):
		return lipgloss.NewStyle().Italic(true).Foreground(mutedColor)
	case trimmed == "---" || trimmed == "***" || trimmed == "___":
		return lipgloss.NewStyle().Foreground(t.Separator)
	case isTableLine(trimmed):
		return lipgloss.NewStyle().Foreground(t.Text)
	default:
		return lipgloss.NewStyle().Foreground(t.Text)
	}
}

// isTableLine detects markdown table rows (e.g. "| a | b |" or "| --- | --- |").
func isTableLine(trimmed string) bool {
	return strings.HasPrefix(trimmed, "|") && strings.HasSuffix(trimmed, "|") && len(trimmed) > 2
}

// renderContentLine renders a single content line with the correct combination
// of inline markdown highlighting, visual-selection background, and block cursor.
// selBg is the background color for selected/highlighted regions.
// It batches consecutive characters that share the same (zone, inlineClass) pair
// into segments so the output stays compact.
func (m Model) renderContentLine(line string, baseStyle lipgloss.Style, inlineCls []int, vr visualRange, selBg lipgloss.Color, isCursorLine bool) string {
	runes := []rune(line)

	// Empty line with cursor: show a visible block.
	if len(runes) == 0 {
		if isCursorLine {
			return lipgloss.NewStyle().
				Background(primaryColor).
				Foreground(theme.Current.Background).
				Render(" ")
		}
		return ""
	}

	// Check if any inline class is non-normal.
	hasInline := false
	if inlineCls != nil {
		for _, c := range inlineCls {
			if c != clsNormal {
				hasInline = true
				break
			}
		}
	}

	// Fast path: no visual, no cursor, no inline → plain styled line.
	if !vr.active && !isCursorLine && !hasInline {
		return baseStyle.Render(line)
	}

	// Fast path: full-line visual, no cursor, no inline → style with selection bg.
	if vr.active && vr.full && !isCursorLine && !hasInline {
		return baseStyle.Background(selBg).Render(line)
	}

	// --- Segment-based rendering ---
	col := 0 // the settings preview has no cursor
	if col < 0 {
		col = 0
	}
	if col >= len(runes) {
		col = len(runes) - 1
	}

	sc, ec := -1, -1
	if vr.active {
		sc = vr.startCol
		if sc < 0 {
			sc = 0
		}
		ec = vr.endCol
		if ec < 0 || ec >= len(runes) {
			ec = len(runes) - 1
		}
	}

	cursorStyle := lipgloss.NewStyle().
		Background(primaryColor).
		Foreground(theme.Current.Background)

	// zone: 0=normal  1=selected  2=cursor
	type seg struct {
		text string
		zone int
		cls  int
	}
	var segs []seg
	for i, r := range runes {
		zone := 0
		if isCursorLine && i == col {
			zone = 2
		} else if vr.active && i >= sc && i <= ec {
			zone = 1
		}
		c := clsNormal
		if hasInline && i < len(inlineCls) {
			c = inlineCls[i]
		}
		ch := string(r)
		if len(segs) > 0 && segs[len(segs)-1].zone == zone && segs[len(segs)-1].cls == c {
			segs[len(segs)-1].text += ch
		} else {
			segs = append(segs, seg{text: ch, zone: zone, cls: c})
		}
	}

	// Focus highlight: when the cursor is on a line that has a
	// checkbox, render the box's inner glyph against a subtle accent
	// background so the user sees "this row is toggleable; press
	// Space to flip." Applied to ALL checkbox classes on that line —
	// the brackets too, so the affordance reads as one unit.
	focusCheckbox := false
	if isCursorLine && hasInline {
		for _, c := range inlineCls {
			if c == clsCheckbox || c == clsCheckboxChecked {
				focusCheckbox = true
				break
			}
		}
	}

	var result strings.Builder
	for _, sg := range segs {
		st := resolveInlineStyle(sg.cls, baseStyle)
		isCheckboxCls := sg.cls == clsCheckbox || sg.cls == clsCheckboxChecked || sg.cls == clsCheckboxBracket
		switch sg.zone {
		case 2:
			result.WriteString(cursorStyle.Render(sg.text))
		case 1:
			result.WriteString(st.Background(selBg).Render(sg.text))
		default:
			if focusCheckbox && isCheckboxCls {
				st = st.Background(theme.Current.SelectedBg)
			}
			result.WriteString(st.Render(sg.text))
		}
	}
	return result.String()
}
