package config

import "testing"

func TestUnquoteValue(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		// The motivating bug: `editor: "vim -p"` must yield `vim -p`,
		// not `"vim -p"` (which exec.Command treats as a literal name).
		{`"vim -p"`, "vim -p"},
		{`'vim -p'`, "vim -p"},

		// Whitespace outside quotes is trimmed (matches strings.TrimSpace
		// behaviour on the un-quoted equivalent).
		{`  "spaced"  `, "spaced"},

		// Bare values are passed through untouched.
		{`nvim`, "nvim"},
		{`vim -p`, "vim -p"},
		{`   bare value   `, "bare value"},

		// Mismatched quotes are NOT stripped — we don't guess the user's
		// intent. The hand-rolled writer never emits mismatched quotes
		// so this case only arises from human edits, and silently
		// fixing them would hide the bug.
		{`"missing-close`, `"missing-close`},
		{`stray"`, `stray"`},
		{`"single quote'`, `"single quote'`},

		// Empty / short strings stay untouched (no quote layer to peel).
		{``, ``},
		{`"`, `"`},
		{`""`, ``},
		{`''`, ``},

		// Embedded quotes inside the value survive unmolested.
		{`"he said \"hi\""`, `he said \"hi\"`},

		// Mixed quote types not stripped.
		{`"a'b"`, `a'b`},
	}
	for _, c := range cases {
		got := unquoteValue(c.in)
		if got != c.want {
			t.Errorf("unquoteValue(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
