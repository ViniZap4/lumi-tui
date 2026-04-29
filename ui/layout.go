package ui

import (
	"strings"

	"github.com/mattn/go-runewidth"
)

// Layout helpers shared by every view. All operate on visual columns
// (i.e. east-asian wide glyphs count as 2). Plain rune count is wrong
// for any layout that has to align with a terminal grid.

// truncate returns s shortened to fit within `width` visual columns,
// using a single-character ellipsis (`…`) to mark truncation. width
// values <= 0 return the empty string.
func truncate(s string, width int) string {
	if width <= 0 {
		return ""
	}
	if runewidth.StringWidth(s) <= width {
		return s
	}
	if width == 1 {
		return "…"
	}
	// Reserve one column for the ellipsis itself.
	return runewidth.Truncate(s, width-1, "") + "…"
}

// padRight pads s with spaces to reach exactly `width` visual columns.
// If s is wider than width, it is truncated (with ellipsis) so the
// returned string still fits the target width.
func padRight(s string, width int) string {
	if width <= 0 {
		return ""
	}
	w := runewidth.StringWidth(s)
	if w >= width {
		return truncate(s, width)
	}
	return s + strings.Repeat(" ", width-w)
}

// wrapWords wraps s at word boundaries so each emitted line is at most
// `width` visual columns. A word longer than width is truncated rather
// than split mid-character. Whitespace inside s is normalised (multiple
// spaces collapse to one).
func wrapWords(s string, width int) []string {
	if width <= 0 {
		return nil
	}
	words := strings.Fields(s)
	if len(words) == 0 {
		return nil
	}
	var lines []string
	var line strings.Builder
	lineW := 0
	for _, w := range words {
		ww := runewidth.StringWidth(w)
		if ww > width {
			// A single word exceeds the target width — flush the line
			// then emit the (truncated) word on its own line.
			if lineW > 0 {
				lines = append(lines, line.String())
				line.Reset()
				lineW = 0
			}
			lines = append(lines, truncate(w, width))
			continue
		}
		sep := 0
		if lineW > 0 {
			sep = 1
		}
		if lineW+sep+ww > width {
			lines = append(lines, line.String())
			line.Reset()
			line.WriteString(w)
			lineW = ww
			continue
		}
		if sep > 0 {
			line.WriteByte(' ')
			lineW++
		}
		line.WriteString(w)
		lineW += ww
	}
	if line.Len() > 0 {
		lines = append(lines, line.String())
	}
	return lines
}
