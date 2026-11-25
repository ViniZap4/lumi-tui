package ui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vinizap/lumi/tui-client/canvas"
)

// `a name.canvas` creates an empty JSON Canvas with the same bytes as
// the web and the Mac, and never overwrites one.
func TestExplorerCreatesEmptyCanvas(t *testing.T) {
	dir := notesVault(t)
	m := newTreeModel(t, dir)
	p := planCreate("", "ideias/quadro.canvas")
	got, sel, err := m.applyCreate(p)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "ideias", "quadro.canvas")
	if sel != path || got != filepath.Dir(path) {
		t.Fatalf("select %q in %q", sel, got)
	}
	b, err := os.ReadFile(path)
	if err != nil || string(b) != canvas.EmptyJSON {
		t.Fatalf("canvas bytes %q %v", b, err)
	}
	if _, _, err := m.applyCreate(p); err == nil || !strings.Contains(err.Error(), "já existe") {
		t.Fatalf("second create must refuse: %v", err)
	}
	if b2, _ := os.ReadFile(path); string(b2) != canvas.EmptyJSON {
		t.Fatal("existing canvas untouched")
	}
}
