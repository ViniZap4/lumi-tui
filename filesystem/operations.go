package filesystem

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/vinizap/lumi/tui-client/domain"
)

func CreateNote(rootDir, title string) (*domain.Note, error) {
	id := generateID(title)
	path := filepath.Join(rootDir, id+".md")

	note := &domain.Note{
		ID:        id,
		Title:     title,
		Content:   "",
		Tags:      []string{},
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
		Path:      path,
	}

	content := fmt.Sprintf(`---
id: %s
title: %s
tags: []
created_at: %s
updated_at: %s
---

# %s

`, note.ID, note.Title, note.CreatedAt.Format(time.RFC3339), note.UpdatedAt.Format(time.RFC3339), note.Title)

	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		return nil, err
	}

	note.Content = content
	return note, nil
}

func RenameNote(note *domain.Note, newTitle string) error {
	note.Title = newTitle
	note.UpdatedAt = time.Now()
	return SaveNote(note)
}

func DeleteNote(note *domain.Note) error {
	return os.Remove(note.Path)
}

func DuplicateNote(note *domain.Note) (*domain.Note, error) {
	newID := generateID(note.Title + "-copy")
	newPath := filepath.Join(filepath.Dir(note.Path), newID+".md")

	newNote := &domain.Note{
		ID:        newID,
		Title:     note.Title + " (copy)",
		Content:   note.Content,
		Tags:      note.Tags,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
		Path:      newPath,
	}

	bodyStart := strings.Index(note.Content, "---\n\n")
	body := ""
	if bodyStart >= 0 {
		body = note.Content[bodyStart+5:]
	}

	content := fmt.Sprintf(`---
id: %s
title: %s
tags: %v
created_at: %s
updated_at: %s
---

%s`, newNote.ID, newNote.Title, newNote.Tags, newNote.CreatedAt.Format(time.RFC3339), newNote.UpdatedAt.Format(time.RFC3339), body)

	if err := os.WriteFile(newPath, []byte(content), 0644); err != nil {
		return nil, err
	}

	newNote.Content = content
	return newNote, nil
}

func MoveFolder(oldPath, destDir string) error {
	newPath := filepath.Join(destDir, filepath.Base(oldPath))
	// Prevent circular move (moving folder into itself or a subfolder)
	if strings.HasPrefix(newPath+string(os.PathSeparator), oldPath+string(os.PathSeparator)) {
		return fmt.Errorf("cannot move folder into itself")
	}
	return os.Rename(oldPath, newPath)
}

func MoveNote(note *domain.Note, destDir string) error {
	newPath := filepath.Join(destDir, filepath.Base(note.Path))
	if err := os.Rename(note.Path, newPath); err != nil {
		return err
	}
	note.Path = newPath
	return nil
}

func SaveNote(note *domain.Note) error {
	content := fmt.Sprintf(`---
id: %s
title: %s
tags: %v
created_at: %s
updated_at: %s
---

%s`, note.ID, note.Title, note.Tags, note.CreatedAt.Format(time.RFC3339), note.UpdatedAt.Format(time.RFC3339), note.Content)

	return os.WriteFile(note.Path, []byte(content), 0644)
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

func generateID(title string) string {
	id := strings.ToLower(title)
	id = strings.ReplaceAll(id, " ", "-")
	id = strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
			return r
		}
		return -1
	}, id)
	return id
}
