package ui

import (
	"os"
	"path/filepath"
	"testing"
)

// dispatchSearch bumps the generation counter and the captured snapshot
// stamps that generation on the result. Without this stamping the gen
// guard in Update can't tell stale results from fresh ones.
func TestDispatchSearch_StampsGen(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "n.md"), []byte("# hi\n"), 0644); err != nil {
		t.Fatal(err)
	}

	m := Model{rootDir: dir, searchType: "filename"}

	cmd1 := m.dispatchSearch()
	if m.searchGen != 1 {
		t.Fatalf("after dispatch #1: gen = %d, want 1", m.searchGen)
	}
	cmd2 := m.dispatchSearch()
	if m.searchGen != 2 {
		t.Fatalf("after dispatch #2: gen = %d, want 2", m.searchGen)
	}

	msg1, ok := cmd1().(searchResultsMsg)
	if !ok {
		t.Fatalf("cmd1 returned %T, want searchResultsMsg", cmd1())
	}
	msg2, ok := cmd2().(searchResultsMsg)
	if !ok {
		t.Fatalf("cmd2 returned %T, want searchResultsMsg", cmd2())
	}
	if msg1.gen != 1 {
		t.Errorf("msg1.gen = %d, want 1 (the gen captured at dispatch)", msg1.gen)
	}
	if msg2.gen != 2 {
		t.Errorf("msg2.gen = %d, want 2", msg2.gen)
	}
}

// Update drops stale results: a slow walk for an earlier query that
// returns AFTER the user has typed more characters must not overwrite
// the current results. This is the user-visible bug the gen counter
// closes.
func TestUpdate_DropsStaleSearchResults(t *testing.T) {
	dir := t.TempDir()

	m := Model{rootDir: dir, searchGen: 5}
	m.searchResults = []Item{{Name: "fresh"}}

	stale := searchResultsMsg{
		gen:     3,
		results: []Item{{Name: "stale"}},
	}
	out, _ := m.Update(stale)
	if got := out.(Model).searchResults[0].Name; got != "fresh" {
		t.Errorf("stale msg overwrote results: got %q, want fresh", got)
	}

	// A msg with matching gen IS accepted.
	fresh := searchResultsMsg{
		gen:     5,
		results: []Item{{Name: "newer"}},
	}
	out, _ = m.Update(fresh)
	if got := out.(Model).searchResults[0].Name; got != "newer" {
		t.Errorf("fresh msg dropped: got %q, want newer", got)
	}
}
