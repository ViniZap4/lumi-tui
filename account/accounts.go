// Package account manages signed-in lumi-server accounts and the HTTP
// surface required to obtain a session token. Accounts persist to
// `~/.config/lumi/accounts.yaml` at 0600 perms — siblings of the existing
// `config.yaml` and the Apple-client-written `vaults.yaml`.
//
// Phase 5 design notes:
//   - One account row per (server URL) — re-logging in to the same server
//     overwrites the existing row rather than appending.
//   - Tokens live in this file because Linux has no canonical keyring; the
//     Apple client puts them in the macOS Keychain instead. Cross-client
//     portability happens via `vaults.yaml` (which doesn't contain secrets),
//     never via `accounts.yaml`.
//   - File mode is forced to 0600 on every write, including overwrite, so
//     a previously-broad permission can't linger.
package account

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"gopkg.in/yaml.v3"
)

// Account binds one server URL to one signed-in user. Persisted to
// `~/.config/lumi/accounts.yaml`.
type Account struct {
	Server      string    `yaml:"server"`
	Username    string    `yaml:"username"`
	UserID      string    `yaml:"user_id,omitempty"`
	DisplayName string    `yaml:"display_name,omitempty"`
	Token       string    `yaml:"token"`
	ExpiresAt   time.Time `yaml:"expires_at,omitempty"`
	AddedAt     time.Time `yaml:"added_at"`
}

// IsExpired returns true when the account's `expires_at` is in the past.
// Accounts with a zero `expires_at` (server didn't return one) are never
// considered expired by this method.
func (a Account) IsExpired() bool {
	if a.ExpiresAt.IsZero() {
		return false
	}
	return time.Now().After(a.ExpiresAt)
}

// File is the on-disk representation. We expose it for testing; callers
// typically use `Load` / `Save` directly.
type File struct {
	Accounts []Account `yaml:"accounts"`
}

// Path resolves to `~/.config/lumi/accounts.yaml`. Returns an error when
// the user's home directory cannot be determined.
func Path() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("locate home dir: %w", err)
	}
	return filepath.Join(home, ".config", "lumi", "accounts.yaml"), nil
}

// Load reads `accounts.yaml`. Missing file returns an empty File, not an
// error — that's the right starting state for a fresh user. Malformed YAML
// returns the error so the user knows something is off.
func Load() (*File, error) {
	path, err := Path()
	if err != nil {
		return nil, err
	}
	return LoadFrom(path)
}

// LoadFrom reads the accounts file at an explicit path. Used by tests and
// by callers that need to point at a non-default location.
func LoadFrom(path string) (*File, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return &File{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	var f File
	if err := yaml.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return &f, nil
}

// Save writes `accounts.yaml` at 0600 perms. The parent directory is
// created with 0700 if missing. Existing perms on the file are forced
// back to 0600 even on overwrite — guards against a previous broader mode
// lingering.
func (f *File) Save() error {
	path, err := Path()
	if err != nil {
		return err
	}
	return f.SaveTo(path)
}

// SaveTo writes the accounts file to an explicit path. Used by tests.
func (f *File) SaveTo(path string) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("mkdir %s: %w", dir, err)
	}
	out, err := yaml.Marshal(f)
	if err != nil {
		return fmt.Errorf("marshal accounts: %w", err)
	}
	header := []byte("# lumi account credentials. DO NOT share. Tokens grant full access\n" +
		"# to the listed servers. lumi-apple stores tokens in Keychain instead\n" +
		"# of in a file; this layout is TUI/Linux-specific.\n")
	body := append(header, out...)
	// Write to a temp file in the same directory, then atomic rename. This
	// keeps the file readable to outsiders for zero time — the temp is
	// 0600 from creation.
	tmp, err := os.CreateTemp(dir, "accounts-*.yaml.tmp")
	if err != nil {
		return fmt.Errorf("temp file: %w", err)
	}
	tmpPath := tmp.Name()
	defer func() {
		_ = os.Remove(tmpPath) // no-op if rename succeeded
	}()
	if err := tmp.Chmod(0600); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("chmod temp: %w", err)
	}
	if _, err := tmp.Write(body); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write temp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("rename %s -> %s: %w", tmpPath, path, err)
	}
	// Force-fix perms on the destination too — atomic rename on some
	// filesystems can drop O_TMPFILE-style perms.
	if err := os.Chmod(path, 0600); err != nil {
		return fmt.Errorf("chmod %s: %w", path, err)
	}
	return nil
}

// Upsert inserts or replaces an account by `server`. Two accounts on the
// same server URL collapse to one — the most recent login wins. Returns
// true if an existing row was overwritten.
func (f *File) Upsert(a Account) bool {
	if a.AddedAt.IsZero() {
		a.AddedAt = time.Now().UTC()
	}
	for i, existing := range f.Accounts {
		if existing.Server == a.Server {
			f.Accounts[i] = a
			return true
		}
	}
	f.Accounts = append(f.Accounts, a)
	// Most-recent first: newer AddedAt sorts before older.
	sort.SliceStable(f.Accounts, func(i, j int) bool {
		return f.Accounts[i].AddedAt.After(f.Accounts[j].AddedAt)
	})
	return false
}

// Remove drops the account for `server`. Returns true if a row was deleted.
func (f *File) Remove(server string) bool {
	for i, a := range f.Accounts {
		if a.Server == server {
			f.Accounts = append(f.Accounts[:i], f.Accounts[i+1:]...)
			return true
		}
	}
	return false
}

// Find returns the account for the given server URL, or nil if not present.
func (f *File) Find(server string) *Account {
	for i := range f.Accounts {
		if f.Accounts[i].Server == server {
			return &f.Accounts[i]
		}
	}
	return nil
}
