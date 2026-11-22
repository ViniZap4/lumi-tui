// Package account manages signed-in lumi-server accounts and the HTTP
// surface required to obtain a session token. Accounts persist to
// `~/.config/lumi/accounts.yaml` at 0600 perms (directory 0700), written
// atomically (tmpfile + rename).
//
// Format v2 (SPEC-LIVROS §2.2):
//   - An account is the pair (server URL, username). Several accounts on
//     the same server are allowed; `Upsert`, `Find` and `Remove` key on
//     both fields.
//   - Server URLs are normalised (§2.1) on load and on upsert.
//   - A file without `version` is read as v1 (one row per server). v1 is
//     a strict subset of v2, so the upgrade is lossless; the next save
//     stamps `version: 2`.
//   - The YAML key stays `server` (not `server_url`) on purpose: a v1
//     binary reading a v2 file still sees every row's server, so it can't
//     silently strip the URL from every account when it rewrites the file.
//   - Tokens live in this file because Linux has no canonical keyring; the
//     Apple client keeps them in the Keychain. Tokens never go in
//     `books.yaml`, `vaults.yaml` or a notes folder.
package account

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
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
	Version  int       `yaml:"version"`
	Accounts []Account `yaml:"accounts"`
}

// FileVersion is the accounts.yaml format this build writes.
const FileVersion = 2

// ErrNoAccount is returned by Resolve when no account matches.
var ErrNoAccount = errors.New("no signed-in account")

// ErrAmbiguousAccount is returned by Resolve when a server has more than
// one account and the caller did not say which one to use.
var ErrAmbiguousAccount = errors.New("more than one account on this server; pass --account <username>")

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
	f.migrate()
	return &f, nil
}

// migrate upgrades an in-memory v1 (or hand-edited) file to v2 semantics:
// server URLs are normalised and exact (server, username) duplicates are
// collapsed, keeping the most recently added row. Nothing else is dropped.
func (f *File) migrate() {
	seen := make(map[string]int, len(f.Accounts))
	out := f.Accounts[:0]
	for _, a := range f.Accounts {
		a.Server = normalizeOrSelf(a.Server)
		key := accountKey(a.Server, a.Username)
		if i, ok := seen[key]; ok {
			if a.AddedAt.After(out[i].AddedAt) {
				out[i] = a
			}
			continue
		}
		seen[key] = len(out)
		out = append(out, a)
	}
	f.Accounts = out
}

func accountKey(server, username string) string {
	return normalizeOrSelf(server) + "#" + strings.ToLower(strings.TrimSpace(username))
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
	f.Version = FileVersion
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

// Upsert inserts or replaces an account keyed by (server, username).
// Re-logging in as the same user on the same server replaces the row;
// a different user on the same server gets a row of its own. Returns true
// if an existing row was overwritten.
func (f *File) Upsert(a Account) bool {
	if a.AddedAt.IsZero() {
		a.AddedAt = time.Now().UTC()
	}
	a.Server = normalizeOrSelf(a.Server)
	key := accountKey(a.Server, a.Username)
	for i, existing := range f.Accounts {
		if accountKey(existing.Server, existing.Username) == key {
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

// Remove drops the account (server, username). Returns true if a row was
// deleted.
func (f *File) Remove(server, username string) bool {
	key := accountKey(server, username)
	for i, a := range f.Accounts {
		if accountKey(a.Server, a.Username) == key {
			f.Accounts = append(f.Accounts[:i], f.Accounts[i+1:]...)
			return true
		}
	}
	return false
}

// Find returns the account (server, username), or nil if not present.
// Server URLs compare after normalisation; usernames case-insensitively.
func (f *File) Find(server, username string) *Account {
	key := accountKey(server, username)
	for i := range f.Accounts {
		if accountKey(f.Accounts[i].Server, f.Accounts[i].Username) == key {
			return &f.Accounts[i]
		}
	}
	return nil
}

// ForServer returns every account on `server`, most recent first.
func (f *File) ForServer(server string) []*Account {
	var out []*Account
	for i := range f.Accounts {
		if SameServer(f.Accounts[i].Server, server) {
			out = append(out, &f.Accounts[i])
		}
	}
	return out
}

// Resolve picks the account to use for `server`. With a username it is an
// exact lookup. Without one it succeeds only when the server has exactly
// one account — guessing between two identities would be a silent
// privilege mix-up.
func (f *File) Resolve(server, username string) (*Account, error) {
	if strings.TrimSpace(username) != "" {
		if a := f.Find(server, username); a != nil {
			return a, nil
		}
		return nil, ErrNoAccount
	}
	matches := f.ForServer(server)
	switch len(matches) {
	case 0:
		return nil, ErrNoAccount
	case 1:
		return matches[0], nil
	default:
		return nil, ErrAmbiguousAccount
	}
}
