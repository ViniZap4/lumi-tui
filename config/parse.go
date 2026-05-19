package config

import "strings"

// unquoteValue strips a single layer of matched surrounding quotes
// (either single or double) from a config value, after trimming
// whitespace. The hand-rolled YAML-ish parsers used here previously
// preserved the literal quotes, so a config like
//
//	editor: "vim -p"
//
// would land as the value `"vim -p"` (including quotes) — exec.Command
// then ran an executable literally named `"vim`, which failed silently.
// Real YAML strips the quotes; this helper closes the gap for the
// hand-rolled path without pulling in a YAML dependency for every key.
func unquoteValue(s string) string {
	s = strings.TrimSpace(s)
	if len(s) < 2 {
		return s
	}
	first, last := s[0], s[len(s)-1]
	if (first == '"' && last == '"') || (first == '\'' && last == '\'') {
		return s[1 : len(s)-1]
	}
	return s
}
