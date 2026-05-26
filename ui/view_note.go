package ui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/vinizap/lumi/tui-client/image"
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

func (m Model) renderFullNote() string {
	if m.fullNote == nil {
		return "No note loaded"
	}

	var s strings.Builder

	// --- Header: title + tags (left) + date (right) + separator ---
	titleStyled := lipgloss.NewStyle().
		Bold(true).
		Foreground(primaryColor).
		Render(" " + m.fullNote.Title)

	var tagStr string
	if len(m.fullNote.Tags) > 0 {
		var tagParts []string
		for _, tag := range m.fullNote.Tags {
			tagParts = append(tagParts, lipgloss.NewStyle().
				Foreground(theme.Current.Accent).
				Render("#"+tag))
		}
		tagStr = "  " + strings.Join(tagParts, " ")
	}

	dateStyled := lipgloss.NewStyle().
		Foreground(mutedColor).
		Render(m.fullNote.UpdatedAt.Format("Jan 2, 2006") + " ")
	tw := lipgloss.Width(titleStyled) + lipgloss.Width(tagStr)
	dw := lipgloss.Width(dateStyled)
	gap := m.width - tw - dw
	if gap < 1 {
		gap = 1
	}
	s.WriteString(titleStyled + tagStr + strings.Repeat(" ", gap) + dateStyled)
	s.WriteString("\n")
	s.WriteString(lipgloss.NewStyle().
		Foreground(theme.Current.Separator).
		Render(strings.Repeat("─", m.width)))
	s.WriteString("\n")

	// --- Content ---
	rawLines := m.contentLines
	displayLines := make([]string, 0, len(rawLines))
	rawToDisplay := make([]int, len(rawLines))

	for i, line := range rawLines {
		rawToDisplay[i] = len(displayLines)
		if image.HasImage(line) {
			src := image.ExtractImagePath(line)

			// Embed URLs (YouTube, Vimeo)
			if image.IsEmbed(src) {
				displayLines = append(displayLines, lipgloss.NewStyle().
					Foreground(theme.Current.Accent).
					Render(fmt.Sprintf("[Embed: %s]", src)))
				continue
			}

			// Video files (by extension)
			if image.IsVideo(src) {
				videoPath := image.GetImagePath(line, m.fullNote.Path)
				if videoPath != "" {
					if _, err := os.Stat(videoPath); err == nil {
						rendered := image.RenderVideo(videoPath, m.width-6)
						for _, rl := range strings.Split(rendered, "\n") {
							displayLines = append(displayLines, imageLinePrefix+rl)
						}
						continue
					}
				}
				displayLines = append(displayLines, lipgloss.NewStyle().
					Foreground(theme.Current.Accent).
					Render(fmt.Sprintf("[Video: %s]", filepath.Base(src))))
				continue
			}

			// PDF files
			if image.IsPdf(src) {
				displayLines = append(displayLines, lipgloss.NewStyle().
					Foreground(theme.Current.Accent).
					Render(fmt.Sprintf("[PDF: %s]", filepath.Base(src))))
				continue
			}

			// Image rendering
			imgPath := image.GetImagePath(line, m.fullNote.Path)
			if imgPath != "" {
				if _, err := os.Stat(imgPath); err == nil {
					rendered := image.Render(imgPath, m.width-6)
					for _, rl := range strings.Split(rendered, "\n") {
						displayLines = append(displayLines, imageLinePrefix+rl)
					}
					continue
				}
			}
			displayLines = append(displayLines, lipgloss.NewStyle().
				Foreground(theme.Current.Error).
				Render(fmt.Sprintf("[Image not found: %s]", filepath.Base(src))))
		} else {
			displayLines = append(displayLines, prettifyForDisplay(line))
		}
	}

	codeLines := codeBlockLines(rawLines)
	tableCtx := buildTableLineCtx(rawLines)

	maxLines := m.viewportHeight()
	totalLines := len(displayLines)

	displayCursor := 0
	if m.lineCursor >= 0 && m.lineCursor < len(rawToDisplay) {
		displayCursor = rawToDisplay[m.lineCursor]
	}

	start := displayCursor - maxLines/2
	if start < 0 {
		start = 0
	}
	if start > totalLines-maxLines {
		start = max(0, totalLines-maxLines)
	}
	end := min(start+maxLines, totalLines)

	displayToRaw := func(d int) int {
		raw := 0
		for r, disp := range rawToDisplay {
			if disp <= d {
				raw = r
			}
		}
		return raw
	}

	// Render visible lines
	for i := start; i < end; i++ {
		line := ""
		if i < len(displayLines) {
			line = displayLines[i]
		}

		// Image lines contain pre-rendered ANSI art or native protocol sequences
		// that must bypass all lipgloss styling to avoid corruption.
		if strings.HasPrefix(line, imageLinePrefix) {
			s.WriteString("  " + strings.TrimPrefix(line, imageLinePrefix))
			s.WriteString("\n")
			continue
		}

		rawIdx := displayToRaw(i)
		inCode := codeLines[rawIdx]
		style := mdLineStyle(line, inCode)
		vr := m.visualRangeForLine(rawIdx)
		isCursorLine := (i == displayCursor)

		// Merge visual selection and yank flash ranges
		yr := m.yankRangeForLine(rawIdx)
		activeRange := vr
		selBg := visualSelBg
		if yr.active {
			activeRange = yr
			selBg = yankFlashBg
		}

		var inlineCls []int
		if shouldClassifyInline(line, inCode) {
			if tctx, ok := tableCtx[rawIdx]; ok {
				inlineCls = classifyInlineWithCtx(line, tctx)
			} else {
				inlineCls = classifyInline(line)
			}
		}
		styledLine := m.renderContentLine(line, style, inlineCls, activeRange, selBg, isCursorLine)

		// Pad code block lines with the subtle code-block background
		// to full width — same blended tint mdLineStyle uses, so the
		// padding tail is byte-identical to the content's background
		// and the right edge of the block reads as one rectangle.
		if inCode && !isCursorLine && !activeRange.active {
			visWidth := lipgloss.Width(styledLine)
			pad := m.width - 2 - visWidth
			if pad > 0 {
				styledLine += lipgloss.NewStyle().Background(codeBlockBg).Render(strings.Repeat(" ", pad))
			}
		}

		// Pad visual-selected lines to full width so the highlight spans the entire row.
		if activeRange.active && activeRange.full && !isCursorLine {
			visWidth := lipgloss.Width(styledLine)
			pad := m.width - 2 - visWidth // 2 for prefix
			if pad > 0 {
				styledLine += lipgloss.NewStyle().Background(selBg).Render(strings.Repeat(" ", pad))
			}
		}

		if isCursorLine {
			prefix := lipgloss.NewStyle().
				Foreground(accentColor).
				Bold(true).
				Render("> ")
			line = prefix + styledLine
		} else if activeRange.active && activeRange.full {
			prefix := lipgloss.NewStyle().Background(selBg).Render("  ")
			line = prefix + styledLine
		} else {
			line = "  " + styledLine
		}

		s.WriteString(line)
		s.WriteString("\n")
	}

	// Pad content area so footer is always at the bottom
	rendered := end - start
	for i := rendered; i < maxLines; i++ {
		s.WriteString("\n")
	}

	// --- Footer: separator + status bar ---
	s.WriteString(lipgloss.NewStyle().
		Foreground(theme.Current.Separator).
		Render(strings.Repeat("─", m.width)))
	s.WriteString("\n")

	if m.statusMsg != "" {
		s.WriteString(StatusBarStyle.Width(m.width).Render(" " + m.statusMsg))
	} else {
		// Right-aligned position + mode pill; left-aligned note id so
		// the user always knows which file they're looking at. The
		// space between scales with terminal width.
		noteID := ""
		if m.fullNote != nil {
			noteID = m.fullNote.Path
			if noteID == "" {
				noteID = m.fullNote.ID
			}
		}
		left := lipgloss.NewStyle().Foreground(theme.Current.TextDim).Render(" " + noteID)
		right := lipgloss.NewStyle().Foreground(theme.Current.TextDim).
			Render(fmt.Sprintf("Ln %d  Col %d%s ", m.lineCursor+1, m.colCursor+1, m.modeIndicator()))
		lw := lipgloss.Width(left)
		rw := lipgloss.Width(right)
		gap := m.width - lw - rw
		if gap < 1 {
			// Truncate the left side rather than the position pill on
			// narrow windows.
			left = lipgloss.NewStyle().Foreground(theme.Current.TextDim).
				Render(" " + truncateForBar(noteID, max(0, m.width-rw-2)))
			lw = lipgloss.Width(left)
			gap = max(1, m.width-lw-rw)
		}
		s.WriteString(StatusBarStyle.Width(m.width).Render(left + strings.Repeat(" ", gap) + right))
	}

	return s.String()
}

// --- Helpers ---

// prettifyForDisplay rewrites the raw line into a more readable form
// for the read view. Substitutions are 1-rune for 1-rune so the column
// math used by the cursor + visual selection still lines up — the
// underlying contentLines stays standard markdown for file
// persistence and copy-out.
//
// Today this only handles the blockquote prefix; more substitutions
// can be added here as the visual style evolves.
func prettifyForDisplay(line string) string {
	// `> ` blockquote → `│ ` (U+2502 BOX DRAWINGS LIGHT VERTICAL).
	// Both are single-cell width.
	trimmed := strings.TrimLeft(line, " \t")
	pad := line[:len(line)-len(trimmed)]
	if strings.HasPrefix(trimmed, "> ") {
		return pad + "│" + trimmed[1:]
	}
	if trimmed == ">" {
		return pad + "│"
	}
	return line
}

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

// visualRangeForLine computes the column range selected on a given raw line.
func (m Model) visualRangeForLine(rawLine int) visualRange {
	if m.visualMode == VisualNone {
		return visualRange{}
	}

	sLine, sCol := m.visualStart, m.visualStartCol
	eLine, eCol := m.visualEnd, m.visualEndCol

	// Normalize so s is before e.
	if sLine > eLine || (sLine == eLine && sCol > eCol) {
		sLine, eLine = eLine, sLine
		sCol, eCol = eCol, sCol
	}

	if rawLine < sLine || rawLine > eLine {
		return visualRange{}
	}

	if m.visualMode == VisualLine {
		return visualRange{active: true, full: true, startCol: 0, endCol: -1}
	}

	// VisualChar
	if sLine == eLine {
		return visualRange{active: true, startCol: sCol, endCol: eCol}
	}
	if rawLine == sLine {
		return visualRange{active: true, startCol: sCol, endCol: -1}
	}
	if rawLine == eLine {
		return visualRange{active: true, startCol: 0, endCol: eCol}
	}
	// Middle line: fully selected.
	return visualRange{active: true, full: true, startCol: 0, endCol: -1}
}

// yankRangeForLine computes the column range for the yank flash highlight on a given raw line.
func (m Model) yankRangeForLine(rawLine int) visualRange {
	if !m.yankHighlight {
		return visualRange{}
	}

	sLine, sCol := m.yankStartLine, m.yankStartCol
	eLine, eCol := m.yankEndLine, m.yankEndCol

	if sLine > eLine || (sLine == eLine && sCol > eCol) {
		sLine, eLine = eLine, sLine
		sCol, eCol = eCol, sCol
	}

	if rawLine < sLine || rawLine > eLine {
		return visualRange{}
	}

	if m.yankMode == VisualLine {
		return visualRange{active: true, full: true, startCol: 0, endCol: -1}
	}

	// VisualChar
	if sLine == eLine {
		return visualRange{active: true, startCol: sCol, endCol: eCol}
	}
	if rawLine == sLine {
		return visualRange{active: true, startCol: sCol, endCol: -1}
	}
	if rawLine == eLine {
		return visualRange{active: true, startCol: 0, endCol: eCol}
	}
	return visualRange{active: true, full: true, startCol: 0, endCol: -1}
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
	col := m.colCursor
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

// truncateForBar shortens s to at most width runes, appending an
// ellipsis when truncated. width<=0 yields the empty string.
func truncateForBar(s string, width int) string {
	if width <= 0 {
		return ""
	}
	if len(s) <= width {
		return s
	}
	if width <= 1 {
		return "…"
	}
	return "…" + s[len(s)-width+1:]
}

// modeIndicator returns a string showing the current mode.
func (m Model) modeIndicator() string {
	parts := []string{}
	switch m.visualMode {
	case VisualChar:
		parts = append(parts, " [VISUAL]")
	case VisualLine:
		parts = append(parts, " [V-LINE]")
	}
	if m.splitMode != "" {
		parts = append(parts, " [SPLIT]")
	}
	return strings.Join(parts, "")
}
