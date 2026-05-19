package ui

import (
	"os"
	"path/filepath"
	"sort"
	"testing"
)

// L3: performSearch must return results in a stable order so the list
// doesn't visibly reshuffle on identical queries. We write notes in
// non-alphabetical order to make sure we don't depend on
// filepath.Walk's per-directory ordering for the final shape.
func TestPerformSearch_SortsByName(t *testing.T) {
	dir := t.TempDir()
	// Mix bare files and a subdir, in deliberately not-already-sorted
	// order — note that filepath.Walk visits "sub" before "z.md"
	// because directories sort lexically too.
	if err := os.WriteFile(filepath.Join(dir, "z.md"), []byte("# Z\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "a.md"), []byte("# A\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "sub"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "sub", "m.md"), []byte("# M\n"), 0644); err != nil {
		t.Fatal(err)
	}

	m := Model{rootDir: dir, searchType: "filename"}
	msg := m.performSearch().(searchResultsMsg)

	names := make([]string, len(msg.results))
	for i, r := range msg.results {
		names[i] = r.Name
	}
	if !sort.StringsAreSorted(names) {
		t.Errorf("results not sorted by Name: %v", names)
	}
	if len(names) != 3 {
		t.Errorf("expected 3 results, got %d (%v)", len(names), names)
	}
}

// L4: cancelEmptyInput closes the modal, clears the value, and emits a
// toast (so the user knows the modal didn't silently accept their empty
// input). Previously the modal closed without any feedback.
func TestCancelEmptyInput_DismissesAndToasts(t *testing.T) {
	m := Model{
		showInput:  true,
		inputValue: "",
		inputMode:  "create",
	}
	cmd := m.cancelEmptyInput("title")
	if m.showInput {
		t.Error("expected showInput to be cleared")
	}
	if m.inputValue != "" {
		t.Errorf("expected inputValue cleared, got %q", m.inputValue)
	}
	if m.toastMsg == "" {
		t.Error("expected a toast message to be set")
	}
	if cmd == nil {
		t.Fatal("expected a non-nil dismiss Cmd from showToast")
	}
	// The Cmd returns a toastDismissMsg after the timeout fires. We
	// don't run it (would actually wait 2s) — just confirm the type
	// shape stays consistent if the Cmd is invoked.
	_ = cmd
}
