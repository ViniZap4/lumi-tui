package filesystem

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Fuzz targets for the frontmatter splitter and the body writer (Phase 3
// quality harness, docs/phase3/research/09 Q6):
//
//	go test ./filesystem -run '^$' -fuzz FuzzSplitFrontmatter -fuzztime 2m

func seedNotes(f *testing.F) {
	f.Add([]byte("---\nid: a\ntitle: A\ntags:\n  - x\n---\n\n# Body\n"))
	f.Add([]byte("---\r\nid: a\r\n---\r\nbody\r\n"))
	f.Add([]byte("# no frontmatter\n\n---\n\nrule\n"))
	f.Add([]byte("---\n"))
	f.Add([]byte("---\n---"))
	f.Add([]byte("---\nkanban-plugin: board\nupdated_at: 2026-01-01T00:00:00Z\n---\n\n\n## A\n"))
	f.Add([]byte("---\n: : [\n---\nbroken yaml\n"))
	f.Add([]byte(""))
}

// FuzzSplitFrontmatter: the split is a pure partition of the input, so
// nothing is ever lost or invented between head, YAML and body.
func FuzzSplitFrontmatter(f *testing.F) {
	seedNotes(f)
	f.Fuzz(func(t *testing.T, data []byte) {
		y, body, ok := splitFrontmatter(data)
		if !ok {
			if !bytes.Equal(body, data) || y != nil {
				t.Fatalf("no frontmatter must return the input as body")
			}
		} else {
			if !bytes.HasPrefix(data, []byte("---")) || !bytes.HasSuffix(data, body) {
				t.Fatalf("body is not a suffix of the input")
			}
			if !bytes.Contains(data[:len(data)-len(body)], y) {
				t.Fatalf("yaml is not inside the head")
			}
		}
		head, rest := splitHead(data)
		if head+string(rest) != string(data) {
			t.Fatalf("splitHead lost bytes: %q + %q != %q", head, rest, data)
		}
		if ok != (head != "") {
			t.Fatalf("splitHead disagrees with splitFrontmatter (ok=%v head=%q)", ok, head)
		}
	})
}

// FuzzReadNoteWriteBody: reading any file never panics (yaml included),
// and WriteBody keeps the frontmatter head byte for byte when bump is
// off, so unknown keys survive an edit.
func FuzzReadNoteWriteBody(f *testing.F) {
	seedNotes(f)
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > 1<<16 {
			return
		}
		dir := t.TempDir()
		p := filepath.Join(dir, "n.md")
		if err := os.WriteFile(p, data, 0o644); err != nil {
			t.Fatal(err)
		}
		if n, err := ReadNote(p); n == nil {
			t.Fatalf("ReadNote returned nil note (err %v)", err)
		}
		head, _, err := ReadRaw(p)
		if err != nil {
			t.Fatal(err)
		}
		if err := WriteBody(p, "novo corpo", false, time.Unix(0, 0)); err != nil {
			t.Fatal(err)
		}
		out, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		want := head
		if want != "" && want[len(want)-1] != '\n' {
			want += "\n"
		}
		if !bytes.HasPrefix(out, []byte(want)) {
			t.Fatalf("WriteBody changed the head:\nwas %q\nnow %q", head, out)
		}
	})
}
