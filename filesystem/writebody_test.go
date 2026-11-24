package filesystem

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestWriteBodyKeepsFrontmatterBytes(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "board.md")
	src := "---\nkanban-plugin: board\nid: b\nupdated_at: 2026-01-01T00:00:00Z\ncustom: [1, 2]  # comment\n---\n\n## A\n\n- [ ] x\n"
	os.WriteFile(p, []byte(src), 0o600)
	head, body, err := ReadRaw(p)
	if err != nil || body != "## A\n\n- [ ] x" || head != src[:len(src)-len("## A\n\n- [ ] x\n")] {
		t.Fatalf("head %q body %q %v", head, body, err)
	}
	if err := WriteBody(p, "## A\n\n- [x] x", false, time.Time{}); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(p)
	if string(got) != "---\nkanban-plugin: board\nid: b\nupdated_at: 2026-01-01T00:00:00Z\ncustom: [1, 2]  # comment\n---\n\n## A\n\n- [x] x\n" {
		t.Fatalf("%q", got)
	}
	if info, _ := os.Stat(p); info.Mode().Perm() != 0o600 {
		t.Fatal("perm changed")
	}
	now := time.Date(2026, 11, 24, 10, 0, 0, 0, time.UTC)
	WriteBody(p, "y", true, now)
	got, _ = os.ReadFile(p)
	if string(got) != "---\nkanban-plugin: board\nid: b\nupdated_at: 2026-11-24T10:00:00Z\ncustom: [1, 2]  # comment\n---\n\ny\n" {
		t.Fatalf("%q", got)
	}
}

func TestWriteBodyPlain(t *testing.T) {
	p := filepath.Join(t.TempDir(), "plain.md")
	os.WriteFile(p, []byte("# hi\n"), 0o644)
	WriteBody(p, "# hi\nação", true, time.Now())
	got, _ := os.ReadFile(p)
	if string(got) != "# hi\nação\n" {
		t.Fatalf("%q", got)
	}
}
