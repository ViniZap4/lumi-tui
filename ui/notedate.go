package ui

import "time"

// noteDate formats a note timestamp for display. Notes without a date
// (plain markdown with no frontmatter, or a server that omitted it) carry
// the zero time, which would otherwise render as "Jan 1, 0001".
func noteDate(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Format("Jan 2, 2006")
}
