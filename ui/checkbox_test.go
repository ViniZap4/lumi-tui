package ui

import "testing"

func TestLineHasCheckbox(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"- [ ] todo", true},
		{"- [x] done", true},
		{"- [X] done", true},
		{"+ [ ] plus marker", true},
		{"* [x] star marker", true},
		{"  - [ ] indented", true},
		{"\t- [ ] tab indented", true},
		{"plain text", false},
		{"- not a checkbox", false},
		{"- [no closing", false},
		{"- [?] unsupported char", false},
		{"-[ ] no space after marker", false},
		{"", false},
		{"   ", false},
	}
	for _, tc := range cases {
		got := lineHasCheckbox(tc.in)
		if got != tc.want {
			t.Errorf("lineHasCheckbox(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

func TestNextPrevCheckboxLine(t *testing.T) {
	m := &Model{contentLines: []string{
		"# Heading",
		"some prose",
		"- [ ] first",
		"more prose",
		"- [x] second",
		"final line",
		"- [ ] third",
	}}

	// Forward hops.
	if got := m.nextCheckboxLine(-1); got != 2 {
		t.Errorf("nextCheckboxLine(-1) = %d, want 2", got)
	}
	if got := m.nextCheckboxLine(2); got != 4 {
		t.Errorf("nextCheckboxLine(2) = %d, want 4", got)
	}
	if got := m.nextCheckboxLine(4); got != 6 {
		t.Errorf("nextCheckboxLine(4) = %d, want 6", got)
	}
	if got := m.nextCheckboxLine(6); got != -1 {
		t.Errorf("nextCheckboxLine(6) = %d, want -1", got)
	}

	// Backward hops.
	if got := m.prevCheckboxLine(6); got != 4 {
		t.Errorf("prevCheckboxLine(6) = %d, want 4", got)
	}
	if got := m.prevCheckboxLine(4); got != 2 {
		t.Errorf("prevCheckboxLine(4) = %d, want 2", got)
	}
	if got := m.prevCheckboxLine(2); got != -1 {
		t.Errorf("prevCheckboxLine(2) = %d, want -1", got)
	}
}

func TestPrettifyForDisplayBlockquote(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"> hello", "│ hello"},
		{"  > nested", "  │ nested"},
		{">", "│"},
		{"plain line", "plain line"},
		{"prose > with arrow", "prose > with arrow"},
		{"", ""},
	}
	for _, tc := range cases {
		got := prettifyForDisplay(tc.in)
		if got != tc.want {
			t.Errorf("prettifyForDisplay(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
