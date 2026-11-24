package ui

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode"

	"github.com/vinizap/lumi/tui-client/filesystem"
)

// Pane is a focusable explorer column (Ctrl-h / Ctrl-l).
type Pane int

const (
	PaneParent Pane = iota
	PaneList
	PanePreview
)

func (p Pane) String() string {
	return [...]string{"parent", "list", "preview"}[p]
}

// listDir lists a folder for the explorer: folders first, then notes,
// each sorted by file name. Dot entries only when showHidden.
func listDir(dir string, showHidden bool) []Item {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	var folders, notes []Item
	for _, e := range entries {
		name := e.Name()
		if strings.HasPrefix(name, ".") && !showHidden {
			continue
		}
		full := filepath.Join(dir, name)
		if e.IsDir() {
			folders = append(folders, Item{Name: name, IsFolder: true, Path: full})
			continue
		}
		if !filesystem.IsNoteFile(name) {
			continue
		}
		n, _ := filesystem.ReadNote(full)
		if n == nil {
			continue
		}
		notes = append(notes, Item{Name: n.Title, Path: full, Note: n})
	}
	return append(folders, notes...)
}

// fuzzyScore scores query against s (case-insensitive subsequence).
// -1 means no match; higher is better: consecutive runs, word starts
// and an early first match score more. Empty query matches everything.
func fuzzyScore(query, s string) int {
	if query == "" {
		return 0
	}
	q := []rune(strings.ToLower(query))
	t := []rune(strings.ToLower(s))
	score, qi, prev, first := 0, 0, -2, -1
	for i, r := range t {
		if qi >= len(q) {
			break
		}
		if r != q[qi] {
			continue
		}
		if first < 0 {
			first = i
		}
		score += 10
		if prev == i-1 {
			score += 15
		}
		if i == 0 || !unicode.IsLetter(t[i-1]) && !unicode.IsDigit(t[i-1]) {
			score += 10
		}
		prev = i
		qi++
	}
	if qi < len(q) {
		return -1
	}
	score -= first
	score -= len(t) / 8
	return score
}

// applyFilter narrows allItems to the filter (fuzzy on the name),
// keeping listing order, and keeps the cursor on the same path when
// it survives.
func (m *Model) applyFilter() {
	keep := ""
	if m.cursor < len(m.items) {
		keep = m.items[m.cursor].Path
	}
	if m.filter == "" {
		m.items = m.allItems
	} else {
		var out []Item
		for _, it := range m.allItems {
			if fuzzyScore(m.filter, it.Name) >= 0 {
				out = append(out, it)
			}
		}
		m.items = out
	}
	m.cursor = 0
	for i, it := range m.items {
		if it.Path == keep {
			m.cursor = i
		}
	}
}

// clearFilter drops the filter and restores the full listing.
func (m *Model) clearFilter() {
	m.filter = ""
	m.filterTyping = false
	m.applyFilter()
}

// selectPath puts the cursor on path when it is listed.
func (m *Model) selectPath(path string) {
	for i, it := range m.items {
		if it.Path == path {
			m.cursor = i
			return
		}
	}
}

func clamp(v, lo, hi int) int {
	if hi < lo {
		return lo
	}
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
