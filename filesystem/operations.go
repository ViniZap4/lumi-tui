package filesystem

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/vinizap/lumi/tui-client/domain"
)

// CreateNote creates a new lumi-managed note. New notes always carry
// frontmatter (id/title/tags/timestamps) so they integrate with the
// rest of the lumi workflow.
//
// Conflict guard: if the derived path already exists, append `-2`,
// `-3`, ... until a free filename is found. Empty/punctuation-only
// titles produce a non-empty fallback ID so we never write
// `<dir>/.md`.
func CreateNote(rootDir, title string) (*domain.Note, error) {
	id := generateIDFromStem(title)
	path, finalID, err := nextAvailablePath(rootDir, id)
	if err != nil {
		return nil, err
	}

	now := time.Now()
	note := &domain.Note{
		ID:             finalID,
		Title:          title,
		Content:        "# " + title + "\n\n",
		Tags:           []string{},
		CreatedAt:      now,
		UpdatedAt:      now,
		Path:           path,
		HadFrontmatter: true,
	}
	if err := WriteNote(note); err != nil {
		return nil, err
	}
	return note, nil
}

// nextAvailablePath returns a path that does not yet exist, by
// appending `-N` to id when needed. Returns the final path and the
// final id (without the `.md` suffix). N caps at 999 to avoid
// runaway loops on hostile filesystems.
func nextAvailablePath(dir, id string) (string, string, error) {
	candidate := id
	for i := 1; i < 1000; i++ {
		path := filepath.Join(dir, candidate+".md")
		if _, err := os.Stat(path); os.IsNotExist(err) {
			return path, candidate, nil
		} else if err != nil {
			return "", "", fmt.Errorf("stat %s: %w", path, err)
		}
		candidate = fmt.Sprintf("%s-%d", id, i+1)
	}
	return "", "", fmt.Errorf("could not find available filename for %q", id)
}

// RenameNote updates the title and timestamp of an existing note and
// writes it back. For plain-markdown notes (HadFrontmatter == false)
// the title lives in the filename and there's no metadata to mutate;
// in that case we rename the file on disk to track the new title.
func RenameNote(note *domain.Note, newTitle string) error {
	if note.HadFrontmatter {
		note.Title = newTitle
		note.UpdatedAt = time.Now()
		return WriteNote(note)
	}
	// Plain markdown: there's no in-file title to update. Rename the
	// file on disk so listing reflects the new name. Title in memory
	// is recomputed at next read via humanizeFilename.
	newID := generateIDFromStem(newTitle)
	dir := filepath.Dir(note.Path)
	newPath, finalID, err := nextAvailablePath(dir, newID)
	if err != nil {
		return err
	}
	if err := os.Rename(note.Path, newPath); err != nil {
		return fmt.Errorf("rename file: %w", err)
	}
	note.Path = newPath
	note.ID = finalID
	note.Title = newTitle
	return nil
}

// DeleteNote removes the file. The caller is expected to confirm.
func DeleteNote(note *domain.Note) error {
	return os.Remove(note.Path)
}

// DuplicateNote writes a copy of an existing note next to it. The
// duplicate inherits the frontmatter mode of the original (a plain
// markdown duplicate stays plain).
func DuplicateNote(note *domain.Note) (*domain.Note, error) {
	dir := filepath.Dir(note.Path)
	baseID := generateIDFromStem(note.Title + " copy")
	newPath, finalID, err := nextAvailablePath(dir, baseID)
	if err != nil {
		return nil, err
	}

	now := time.Now()
	newNote := &domain.Note{
		ID:             finalID,
		Title:          note.Title + " (copy)",
		Content:        note.Content,
		Tags:           append([]string(nil), note.Tags...),
		CreatedAt:      now,
		UpdatedAt:      now,
		Path:           newPath,
		HadFrontmatter: note.HadFrontmatter,
	}
	if err := WriteNote(newNote); err != nil {
		return nil, err
	}
	return newNote, nil
}

// MoveFolder moves a directory tree to a new parent. Refuses
// circular moves (folder into itself or a subfolder).
func MoveFolder(oldPath, destDir string) error {
	newPath := filepath.Join(destDir, filepath.Base(oldPath))
	if strings.HasPrefix(newPath+string(os.PathSeparator), oldPath+string(os.PathSeparator)) {
		return fmt.Errorf("cannot move folder into itself")
	}
	return os.Rename(oldPath, newPath)
}

// MoveNote moves a note file to a new directory; the in-memory
// note.Path is updated on success.
func MoveNote(note *domain.Note, destDir string) error {
	newPath := filepath.Join(destDir, filepath.Base(note.Path))
	if err := os.Rename(note.Path, newPath); err != nil {
		return err
	}
	note.Path = newPath
	return nil
}

// SaveNote writes the current state of a note. Routes through
// WriteNote so the HadFrontmatter branch is honoured (lumi-managed
// notes get fresh YAML; plain-markdown notes are preserved verbatim
// to avoid silent file mutation).
//
// UpdatedAt is bumped automatically for lumi-managed notes; plain
// notes don't carry an UpdatedAt anyway.
func SaveNote(note *domain.Note) error {
	if note.HadFrontmatter {
		note.UpdatedAt = time.Now()
	}
	return WriteNote(note)
}

func CreateFolder(parentDir, name string) error {
	path := filepath.Join(parentDir, name)
	return os.MkdirAll(path, 0755)
}

func RenameFolder(oldPath, newName string) error {
	newPath := filepath.Join(filepath.Dir(oldPath), newName)
	return os.Rename(oldPath, newPath)
}

func DeleteFolder(path string) error {
	return os.RemoveAll(path)
}

func IsFolderEmpty(path string) (bool, error) {
	entries, err := os.ReadDir(path)
	if err != nil {
		return false, err
	}
	for _, entry := range entries {
		if !strings.HasPrefix(entry.Name(), ".") {
			return false, nil
		}
	}
	return true, nil
}

// generateID is kept as a back-compat alias for callers that still
// reference it. New code should use generateIDFromStem (parser.go) or
// nextAvailablePath above.
func generateID(title string) string {
	return generateIDFromStem(title)
}
