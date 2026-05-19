// tui-client/filesystem/parser.go
package filesystem

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/vinizap/lumi/tui-client/domain"
	"gopkg.in/yaml.v3"
)

// frontmatterFence is the literal three-dash marker. To count as a
// frontmatter fence we require the three dashes to occupy a whole line
// — i.e. either at byte 0 or immediately after a `\n`, followed by
// `\n` or `\r\n`. A horizontal rule embedded mid-document (`\n\n---\n\n`)
// must NOT be mistaken for a frontmatter close.
const frontmatterFence = "---"

// ReadNote loads a markdown file. Three branches:
//
//  1. The file starts with a frontmatter fence and we find a matching
//     closing fence on its own line: parse the YAML in between and use
//     the rest as the body. Note.HadFrontmatter = true.
//  2. The file does not start with a fence (or has no closing fence):
//     treat it as plain markdown. Synthesise ID/Title from the filename
//     and stat the file for timestamps. Note.HadFrontmatter = false.
//  3. The file looks like it has frontmatter but the YAML inside is
//     unparseable: surface a warning *and* fall back to plain-markdown
//     metadata so the note still loads. We trade a strict round-trip
//     for not silently dropping the user's note. ListNotes used to skip
//     these entirely, which the user perceived as "lumi can't see my
//     notes".
//
// In every case ReadNote returns a non-nil Note when the file is
// readable. A nil-Note error means we couldn't even read bytes (perm,
// gone) — that's the only condition a caller should treat as a hard
// failure.
func ReadNote(path string) (*domain.Note, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	note := &domain.Note{Path: path}

	yamlBytes, body, hasFrontmatter := splitFrontmatter(data)
	if !hasFrontmatter {
		fillAutoMetadata(note, path, data)
		return note, nil
	}

	// Try to parse the frontmatter. If it fails, fall back to plain
	// metadata so the note still appears in lists — but stash the
	// original YAML bytes on the note so WriteNote can re-emit them
	// verbatim. Previously a parse failure silently destroyed the
	// frontmatter on the next save; the user lost tags, custom
	// fields, and timestamps the moment they edited body text.
	if err := yaml.Unmarshal(yamlBytes, note); err != nil {
		fillAutoMetadata(note, path, body)
		note.HadFrontmatter = true
		note.RawFrontmatter = append([]byte(nil), bytes.TrimRight(yamlBytes, "\n")...)
		note.Content = string(bytes.TrimSpace(body))
		// Return the note plus a soft error so callers (e.g. a future
		// "show warnings" mode) can surface the parse problem. The
		// caller's contract: a non-nil note + non-nil error means
		// "loaded with degraded metadata".
		return note, fmt.Errorf("frontmatter parse failed for %s: %w", path, err)
	}

	note.Content = string(bytes.TrimSpace(body))
	note.HadFrontmatter = true
	return note, nil
}

// splitFrontmatter inspects data for a leading YAML frontmatter block
// and returns (yaml, body, true) when it finds one. A frontmatter block
// must:
//
//   - Start at byte 0 with the literal "---".
//   - Be followed by a newline.
//   - Have a matching "---" line later in the file (also at start of
//     line and followed by a newline or EOF).
//
// Any deviation -> (nil, data, false).
//
// Crucially this rejects the case where the file simply contains a
// markdown horizontal rule mid-document: SplitN-on-"---" used to match
// that and emit garbage to the YAML parser.
func splitFrontmatter(data []byte) ([]byte, []byte, bool) {
	if !startsWithFence(data) {
		return nil, data, false
	}
	// Skip the opening fence line (3 chars + newline; account for CRLF).
	rest := skipLine(data, len(frontmatterFence))
	if rest == nil {
		return nil, data, false
	}

	// Scan rest for a closing fence at the start of a line.
	// We walk by lines so we don't get confused by `---` embedded
	// in a body paragraph.
	closeOff := findFenceLine(rest)
	if closeOff < 0 {
		// No closing fence → not frontmatter; treat as plain markdown.
		return nil, data, false
	}

	yamlBytes := rest[:closeOff]
	// Skip the closing fence line itself.
	body := skipLine(rest[closeOff:], len(frontmatterFence))
	if body == nil {
		body = []byte{}
	}
	return yamlBytes, body, true
}

// startsWithFence reports whether data begins with the literal "---"
// followed by a newline (or CRLF). Anchored at byte 0 only.
func startsWithFence(data []byte) bool {
	if !bytes.HasPrefix(data, []byte(frontmatterFence)) {
		return false
	}
	rest := data[len(frontmatterFence):]
	if len(rest) == 0 {
		return false
	}
	if rest[0] == '\n' {
		return true
	}
	if len(rest) >= 2 && rest[0] == '\r' && rest[1] == '\n' {
		return true
	}
	return false
}

// skipLine returns data with the first `prefixLen` bytes plus the
// following line terminator stripped. Returns nil if there's no line
// terminator (caller should treat as no-frontmatter or no-close).
func skipLine(data []byte, prefixLen int) []byte {
	if len(data) < prefixLen {
		return nil
	}
	rest := data[prefixLen:]
	switch {
	case len(rest) >= 2 && rest[0] == '\r' && rest[1] == '\n':
		return rest[2:]
	case len(rest) >= 1 && rest[0] == '\n':
		return rest[1:]
	default:
		return nil
	}
}

// findFenceLine scans data line-by-line and returns the byte offset
// where a "---"-only line starts (i.e. where the closing fence begins).
// Returns -1 if no such line is found.
//
// A line is the closing fence iff its content (excluding the line
// terminator) is exactly "---". Lines that begin with "---" but contain
// further characters do NOT close.
func findFenceLine(data []byte) int {
	off := 0
	for off < len(data) {
		// Find the next newline.
		nl := bytes.IndexByte(data[off:], '\n')
		var line []byte
		var lineLen int
		if nl < 0 {
			line = data[off:]
			lineLen = len(line)
		} else {
			line = data[off : off+nl]
			lineLen = nl + 1
		}
		// Strip an optional trailing CR.
		if len(line) > 0 && line[len(line)-1] == '\r' {
			line = line[:len(line)-1]
		}
		if string(line) == frontmatterFence {
			return off
		}
		off += lineLen
		if nl < 0 {
			break
		}
	}
	return -1
}

// fillAutoMetadata populates a Note from filename + content for files
// that have no (or unparseable) frontmatter. ID is generated from the
// stem; Title humanises the same. Tags are empty. Timestamps come from
// the filesystem. HadFrontmatter is left false so writers preserve the
// plain-markdown format.
func fillAutoMetadata(note *domain.Note, path string, data []byte) {
	baseName := strings.TrimSuffix(filepath.Base(path), ".md")
	note.ID = generateIDFromStem(baseName)
	note.Title = humanizeFilename(baseName)
	note.Tags = []string{}
	note.Content = string(bytes.TrimSpace(data))

	if info, err := os.Stat(path); err == nil {
		note.CreatedAt = info.ModTime()
		note.UpdatedAt = info.ModTime()
	}
}

// WriteNote serialises a note to disk. Behaviour depends on
// note.HadFrontmatter:
//
//   - true: emit a fresh frontmatter block via yaml.Marshal (safe for
//     punctuation, tags, accents — replaces the old fmt.Sprintf-based
//     writer that corrupted YAML on edge cases).
//   - false: write only note.Content. The user's plain markdown stays
//     plain markdown.
//
// In both branches the write is atomic: temp file in the same
// directory + os.Rename, so a crash mid-write never leaves a
// partially-written note on disk.
func WriteNote(note *domain.Note) error {
	var payload []byte
	if note.HadFrontmatter {
		// Two paths into the FM branch:
		//   1. Parsed cleanly on read → re-marshal from struct fields
		//      so any UI edits to Title/Tags/etc. round-trip.
		//   2. Parse failed on read → emit RawFrontmatter verbatim so
		//      custom fields / malformed YAML / comments survive.
		var fmBytes []byte
		if len(note.RawFrontmatter) > 0 {
			fmBytes = note.RawFrontmatter
		} else {
			var fmBuf bytes.Buffer
			enc := yaml.NewEncoder(&fmBuf)
			enc.SetIndent(2)
			if err := enc.Encode(note); err != nil {
				_ = enc.Close()
				return fmt.Errorf("encode frontmatter: %w", err)
			}
			if err := enc.Close(); err != nil {
				return fmt.Errorf("close encoder: %w", err)
			}
			fmBytes = bytes.TrimRight(fmBuf.Bytes(), "\n")
		}

		var buf bytes.Buffer
		buf.WriteString("---\n")
		buf.Write(fmBytes)
		if !bytes.HasSuffix(fmBytes, []byte{'\n'}) {
			buf.WriteByte('\n')
		}
		buf.WriteString("---\n\n")
		buf.WriteString(note.Content)
		payload = buf.Bytes()
	} else {
		// Plain markdown: write content as-is, but ensure exactly one
		// trailing newline (POSIX text-file convention; most editors,
		// linters, and `cat`-into-pipeline workflows expect it).
		payload = []byte(note.Content)
		if len(payload) == 0 || payload[len(payload)-1] != '\n' {
			payload = append(payload, '\n')
		}
	}
	return atomicWriteFile(note.Path, payload, 0644)
}

// atomicWriteFile writes data to path via temp + rename, so readers
// never see a partially-written file. The temp file lives in the same
// directory as the target so the rename is on the same filesystem and
// therefore atomic on POSIX.
func atomicWriteFile(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".lumi-tmp-*")
	if err != nil {
		return fmt.Errorf("create temp: %w", err)
	}
	tmpPath := tmp.Name()
	cleanup := func() {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
	}
	if _, err := tmp.Write(data); err != nil {
		cleanup()
		return fmt.Errorf("write temp: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		cleanup()
		return fmt.Errorf("sync temp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("close temp: %w", err)
	}
	if err := os.Chmod(tmpPath, perm); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("chmod temp: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("rename: %w", err)
	}
	return nil
}

// ListNotes enumerates *.md files at dir (non-recursive). Files that
// can't be read are skipped (they wouldn't be openable from the UI
// anyway), but parse warnings are not fatal — see ReadNote for the
// graceful-fallback contract.
func ListNotes(dir string) ([]*domain.Note, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}

	var notes []*domain.Note
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".md") {
			continue
		}
		// Skip dot-prefixed files so .DS_Store-style noise doesn't
		// surface as a "note". (.md prefixed with a dot is unusual but
		// possible on hidden-by-convention notes.)
		if strings.HasPrefix(entry.Name(), ".") {
			continue
		}

		path := filepath.Join(dir, entry.Name())
		note, _ := ReadNote(path) // soft errors carry a Note; only nil
		// note means the file was unreadable.
		if note == nil {
			continue
		}
		notes = append(notes, note)
	}
	return notes, nil
}

// humanizeFilename converts a filename like "my-note" or "my_note" to
// "My Note".
func humanizeFilename(name string) string {
	name = strings.NewReplacer("-", " ", "_", " ").Replace(name)
	words := strings.Fields(name)
	for i, w := range words {
		runes := []rune(w)
		if len(runes) == 0 {
			continue
		}
		runes[0] = unicode.ToUpper(runes[0])
		words[i] = string(runes)
	}
	if len(words) == 0 {
		return name
	}
	return strings.Join(words, " ")
}

// generateIDFromStem normalises a filename stem to a slug used as
// Note.ID for plain-markdown files. Permits ASCII alnum and hyphen;
// folds whitespace/underscore to hyphen; lowercases. Unicode is folded
// to '-' (lossy by design — IDs are filesystem-friendly identifiers,
// not display names).
func generateIDFromStem(stem string) string {
	stem = strings.ToLower(strings.TrimSpace(stem))
	var b strings.Builder
	prevHyphen := true
	for _, r := range stem {
		switch {
		case (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'):
			b.WriteRune(r)
			prevHyphen = false
		default:
			if !prevHyphen {
				b.WriteByte('-')
				prevHyphen = true
			}
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		out = "note"
	}
	return out
}

// ListFolders enumerates immediate subdirectories at root. Hidden dirs
// (".lumi", ".git", etc.) are skipped.
func ListFolders(root string) ([]*domain.Folder, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}

	var folders []*domain.Folder
	for _, entry := range entries {
		if !entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
			continue
		}

		folders = append(folders, &domain.Folder{
			Name: entry.Name(),
			Path: filepath.Join(root, entry.Name()),
		})
	}

	return folders, nil
}
