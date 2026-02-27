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

func ReadNote(path string) (*domain.Note, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	note := &domain.Note{Path: path}

	// Split frontmatter and content
	parts := bytes.SplitN(data, []byte("---"), 3)
	if len(parts) < 3 {
		// No frontmatter: auto-generate metadata from filename and file stat
		baseName := strings.TrimSuffix(filepath.Base(path), ".md")
		note.ID = generateID(baseName)
		note.Title = humanizeFilename(baseName)
		note.Tags = []string{}
		note.Content = string(bytes.TrimSpace(data))

		if info, err := os.Stat(path); err == nil {
			note.CreatedAt = info.ModTime()
			note.UpdatedAt = info.ModTime()
		}

		return note, nil
	}

	// Parse frontmatter
	if err := yaml.Unmarshal(parts[1], note); err != nil {
		return nil, fmt.Errorf("failed to parse frontmatter: %w", err)
	}

	// Store content
	note.Content = string(bytes.TrimSpace(parts[2]))

	return note, nil
}

func WriteNote(note *domain.Note) error {
	var buf bytes.Buffer

	buf.WriteString("---\n")

	encoder := yaml.NewEncoder(&buf)
	encoder.SetIndent(2)
	if err := encoder.Encode(note); err != nil {
		return fmt.Errorf("failed to encode frontmatter: %w", err)
	}
	encoder.Close()

	buf.WriteString("---\n\n")
	buf.WriteString(note.Content)

	return os.WriteFile(note.Path, buf.Bytes(), 0644)
}

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

		path := filepath.Join(dir, entry.Name())
		note, err := ReadNote(path)
		if err != nil {
			continue // Skip invalid notes
		}
		notes = append(notes, note)
	}

	return notes, nil
}

// humanizeFilename converts a filename like "my-note" or "my_note" to "My Note".
func humanizeFilename(name string) string {
	// Replace hyphens and underscores with spaces
	name = strings.NewReplacer("-", " ", "_", " ").Replace(name)
	// Title-case each word
	words := strings.Fields(name)
	for i, w := range words {
		runes := []rune(w)
		runes[0] = unicode.ToUpper(runes[0])
		words[i] = string(runes)
	}
	if len(words) == 0 {
		return name
	}
	return strings.Join(words, " ")
}

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
