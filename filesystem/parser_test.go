package filesystem

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Sanity: a well-formed note round-trips through Read+Write unchanged in
// structure (timestamps stay, tags stay, body stays).
func TestReadWriteNote_RoundTrip_ValidFrontmatter(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "good.md")
	in := `---
id: good
title: Good Note
created_at: 2026-01-01T00:00:00Z
updated_at: 2026-01-01T00:00:00Z
tags:
  - alpha
  - beta
---

# Hello

Body content.
`
	if err := os.WriteFile(path, []byte(in), 0644); err != nil {
		t.Fatal(err)
	}

	note, err := ReadNote(path)
	if err != nil {
		t.Fatalf("ReadNote: %v", err)
	}
	if !note.HadFrontmatter {
		t.Errorf("HadFrontmatter = false, want true")
	}
	if len(note.RawFrontmatter) != 0 {
		t.Errorf("RawFrontmatter unexpectedly set on clean parse: %q", note.RawFrontmatter)
	}
	if note.Title != "Good Note" || note.ID != "good" {
		t.Errorf("metadata mismatch: %+v", note)
	}
	if !strings.Contains(note.Content, "Body content") {
		t.Errorf("body lost: %q", note.Content)
	}

	if err := WriteNote(note); err != nil {
		t.Fatalf("WriteNote: %v", err)
	}
	out, _ := os.ReadFile(path)
	if !strings.Contains(string(out), "tags:") || !strings.Contains(string(out), "alpha") {
		t.Errorf("tags lost on round-trip: %s", out)
	}
}

// The core M1 fix: broken frontmatter must survive a Read+Write round-
// trip byte-for-byte instead of being silently dropped.
func TestReadWriteNote_PreservesBrokenFrontmatter(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "broken.md")
	// "custom_field" + the unindented "this is not valid" line make
	// this YAML unparseable into a domain.Note. The user might also
	// have inserted a comment they want preserved.
	in := `---
id: broken
title: Broken Note
custom_field: this is not valid: because of the embedded colon
# user-added comment we must not destroy
tags:
  - keep-me
---

# Body still readable.
`
	if err := os.WriteFile(path, []byte(in), 0644); err != nil {
		t.Fatal(err)
	}

	note, err := ReadNote(path)
	// We expect an error AND a non-nil note — the contract for
	// "loaded with degraded metadata".
	if err == nil {
		t.Fatalf("expected soft parse error, got nil")
	}
	if note == nil {
		t.Fatalf("expected non-nil note on soft parse failure")
	}
	if !note.HadFrontmatter {
		t.Errorf("HadFrontmatter = false, want true on broken FM")
	}
	if len(note.RawFrontmatter) == 0 {
		t.Fatalf("RawFrontmatter not preserved")
	}
	if !strings.Contains(string(note.RawFrontmatter), "user-added comment") {
		t.Errorf("comment lost from raw frontmatter: %q", note.RawFrontmatter)
	}
	if !strings.Contains(note.Content, "Body still readable") {
		t.Errorf("body content lost: %q", note.Content)
	}
	// Critical: the body must NOT include the frontmatter — previously
	// fillAutoMetadata was called with the full file, leaking the FM
	// into the rendered body.
	if strings.Contains(note.Content, "custom_field") {
		t.Errorf("frontmatter leaked into body: %q", note.Content)
	}

	// Now write and re-read; the raw FM must survive verbatim.
	if err := WriteNote(note); err != nil {
		t.Fatalf("WriteNote: %v", err)
	}
	out, _ := os.ReadFile(path)
	for _, want := range []string{"custom_field: this is not valid: because of the embedded colon", "user-added comment we must not destroy", "keep-me"} {
		if !strings.Contains(string(out), want) {
			t.Errorf("write dropped fragment %q; file:\n%s", want, out)
		}
	}

	// Re-read; RawFrontmatter must still be populated and identical.
	again, err := ReadNote(path)
	if err == nil {
		t.Fatalf("expected soft parse error on re-read, got nil")
	}
	if string(again.RawFrontmatter) != string(note.RawFrontmatter) {
		t.Errorf("RawFrontmatter drifted across round-trip\nbefore: %q\nafter:  %q", note.RawFrontmatter, again.RawFrontmatter)
	}
}

// A note with no frontmatter at all stays as plain markdown — the
// new code path must NOT accidentally invent a frontmatter block.
func TestReadWriteNote_PlainMarkdown(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "plain.md")
	in := "# Just markdown.\n\nNo frontmatter here.\n"
	if err := os.WriteFile(path, []byte(in), 0644); err != nil {
		t.Fatal(err)
	}

	note, err := ReadNote(path)
	if err != nil {
		t.Fatalf("unexpected error on plain markdown: %v", err)
	}
	if note.HadFrontmatter {
		t.Errorf("HadFrontmatter = true on plain markdown")
	}
	if len(note.RawFrontmatter) != 0 {
		t.Errorf("RawFrontmatter set on plain markdown: %q", note.RawFrontmatter)
	}

	if err := WriteNote(note); err != nil {
		t.Fatalf("WriteNote: %v", err)
	}
	out, _ := os.ReadFile(path)
	if strings.HasPrefix(string(out), "---") {
		t.Errorf("plain markdown got a frontmatter block on write:\n%s", out)
	}
}

// Editing the body of a broken-FM note must keep the FM intact (this
// is the scenario the audit highlighted — user edits body, the next
// WriteNote silently destroys the FM).
func TestWriteNote_EditedBodyKeepsBrokenFrontmatter(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "edit.md")
	in := `---
id: edit
title: Edit Me
unparseable_field: nested: colon: confuses the parser
tags:
  - surprise
---

original body
`
	if err := os.WriteFile(path, []byte(in), 0644); err != nil {
		t.Fatal(err)
	}

	note, err := ReadNote(path)
	if err == nil {
		t.Fatal("expected soft parse error")
	}
	note.Content = "edited body\n"

	if err := WriteNote(note); err != nil {
		t.Fatal(err)
	}
	out, _ := os.ReadFile(path)
	if !strings.Contains(string(out), "edited body") {
		t.Errorf("body edit lost: %s", out)
	}
	if !strings.Contains(string(out), "unparseable_field: nested: colon") {
		t.Errorf("broken FM destroyed on body edit: %s", out)
	}
}
