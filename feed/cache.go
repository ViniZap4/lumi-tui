package feed

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// BookCache is the per-book sync state that must NOT live in books.yaml
// (SPEC-LIVROS §4.2): the cursor, last_synced_at, and the hash of every
// note body this client last wrote into the mirror folder — the basis of
// the "never overwrite a dirty note" rule.
//
// It lives in the user cache dir (`<UserCacheDir>/lumi/books/<id>.json`,
// 0600), not in the notes folder, so it never travels through iCloud /
// Syncthing / Git with the notes.
type BookCache struct {
	VaultID      string               `json:"vault_id"`
	ServerURL    string               `json:"server_url"` // cursor is per server (§2.4.5)
	Cursor       int64                `json:"cursor"`
	LastSyncedAt time.Time            `json:"last_synced_at,omitempty"`
	Notes        map[string]NoteEntry `json:"notes"`
}

// NoteEntry records what the mirror last wrote for a note.
type NoteEntry struct {
	Path string `json:"path"` // relative to the book folder
	Hash string `json:"hash"` // sha256 of the body (frontmatter excluded)
}

// BodyHash hashes a note body, normalised the way filesystem.ReadNote
// returns it (surrounding whitespace trimmed), so a hash taken from
// server text matches the same body read back from disk.
func BodyHash(body string) string {
	h := sha256.Sum256([]byte(strings.TrimSpace(body)))
	return hex.EncodeToString(h[:])
}

// CacheDir returns `<UserCacheDir>/lumi/books`.
func CacheDir() (string, error) {
	d, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(d, "lumi", "books"), nil
}

// LoadCache reads a book's cache. A missing or unreadable file yields a
// fresh cache (cursor 0 = full reconcile), never an error: the cache is
// derived data.
func LoadCache(dir, bookID, vaultID, serverURL string) *BookCache {
	c := &BookCache{VaultID: vaultID, ServerURL: serverURL, Notes: map[string]NoteEntry{}}
	if dir == "" {
		return c
	}
	data, err := os.ReadFile(filepath.Join(dir, bookID+".json"))
	if err != nil {
		return c
	}
	var disk BookCache
	if json.Unmarshal(data, &disk) != nil {
		return c
	}
	if disk.VaultID != vaultID || disk.ServerURL != serverURL {
		// Rebound or switched server: seq is per server, start over, but
		// keep note hashes (the files on disk did not change).
		c.Notes = disk.Notes
		if c.Notes == nil {
			c.Notes = map[string]NoteEntry{}
		}
		return c
	}
	if disk.Notes == nil {
		disk.Notes = map[string]NoteEntry{}
	}
	return &disk
}

// Save writes the cache atomically at 0600.
func (c *BookCache) Save(dir, bookID string) error {
	if dir == "" {
		return nil
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".cache-*.json")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp.Name(), filepath.Join(dir, bookID+".json")); err != nil {
		return fmt.Errorf("save cache: %w", err)
	}
	return nil
}
