// Package books implements the client-side book registry of SPEC-LIVROS
// §2.3: `~/.config/lumi/books.yaml`. A "book" (livro) is the UI name for a
// vault; the file is shared byte-for-byte with the Apple client, so the
// writer here is hand-rolled to a fixed shape rather than delegated to a
// YAML library:
//
//   - fixed key order (id, name, kind, server_url, account, vault_id, path,
//     sort, added_at, last_opened_at);
//   - books ordered by `sort`, then `id`;
//   - timestamps in UTC with seconds and `Z`;
//   - a literal `null` for absent values;
//   - free-text strings (name, server_url, account, path) in double
//     quotes; identifiers (UUIDs), the `kind` enum, integers and
//     timestamps bare — exactly as the spec's example shows;
//   - LF line endings and a single trailing newline.
//
// Reading goes through yaml.v3 (tolerant of hand edits). A file whose
// `version` is newer than this build understands is loaded read-only:
// Save refuses to write it, so an older binary can never strip fields it
// does not know about — the very failure that made the spec choose a new
// file instead of a vaults.yaml v2.
package books

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"gopkg.in/yaml.v3"
)

// FormatVersion is the books.yaml format this build reads and writes.
const FormatVersion = 1

// Header is the first line of every books.yaml written by a lumi client.
const Header = "# ~/.config/lumi/books.yaml — lumi book registry. Format: SPEC-LIVROS §2.3."

// SortStep is the gap between consecutive `sort` values.
const SortStep = 10

// Kind distinguishes a folder-only book from one bound to a server vault.
type Kind string

const (
	KindLocal Kind = "local"
	KindBound Kind = "bound"
)

// Book is one row of the registry.
type Book struct {
	ID           string // stable local UUID (lowercase); = vault_id when born from a clone
	Name         string
	Kind         Kind
	ServerURL    string // "" when local
	Account      string // username; "" when local
	VaultID      string // server id; "" when local or when a migrated binding is incomplete
	Path         string // as written (may be ~/-relative); "" when there is no mirror folder
	Sort         int
	AddedAt      time.Time
	LastOpenedAt time.Time // zero = never opened

	// Alternates are other servers where the same vault_id is reachable
	// through one of this client's accounts (§2.4). In memory only.
	Alternates []Alternate
}

// Alternate is another (server, account) through which a bound book's
// vault is also reachable (a federated copy).
type Alternate struct {
	ServerURL string
	Account   string
	Role      string // federation role on that server: "home", "follower" or ""
}

// IsBound reports whether the book is bound to a server vault.
func (b Book) IsBound() bool { return b.Kind == KindBound }

// IncompleteBinding is true for a bound book with no vault_id (a
// migrated vaults.yaml row whose folder had no `.lumi/vault.yaml`). Its
// sync state is "Erro: ligação incompleta" until relinked.
func (b Book) IncompleteBinding() bool { return b.IsBound() && b.VaultID == "" }

// AbsPath returns Path with a leading `~` expanded. "" when no folder.
func (b Book) AbsPath() string { return ExpandHome(b.Path) }

// Registry is the parsed books.yaml.
type Registry struct {
	Version int
	Active  string // TUI-only: id of the active book; "" = none recorded
	Books   []Book

	// ReadOnly is set when the file is newer than this build or has rows
	// this build cannot represent. Save refuses to overwrite it.
	ReadOnly bool
	// Problems lists human-readable reasons for ReadOnly.
	Problems []string
}

// ErrReadOnly is returned by Save on a registry that must not be
// rewritten by this build.
var ErrReadOnly = errors.New("books.yaml was written by a newer lumi or has rows this version cannot represent; refusing to overwrite it")

// DefaultPath resolves `~/.config/lumi/books.yaml`.
func DefaultPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("locate home dir: %w", err)
	}
	return filepath.Join(home, ".config", "lumi", "books.yaml"), nil
}

// --- reading ---------------------------------------------------------

type rawFile struct {
	Version *int      `yaml:"version"`
	Active  *string   `yaml:"active"`
	Books   []rawBook `yaml:"books"`
}

type rawBook struct {
	ID           *string `yaml:"id"`
	Name         *string `yaml:"name"`
	Kind         *string `yaml:"kind"`
	ServerURL    *string `yaml:"server_url"`
	Account      *string `yaml:"account"`
	VaultID      *string `yaml:"vault_id"`
	Path         *string `yaml:"path"`
	Sort         *int    `yaml:"sort"`
	AddedAt      *string `yaml:"added_at"`
	LastOpenedAt *string `yaml:"last_opened_at"`
}

// Parse decodes a books.yaml document.
func Parse(data []byte) (*Registry, error) {
	var raw rawFile
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parse books.yaml: %w", err)
	}
	r := &Registry{Version: FormatVersion}
	if raw.Version != nil {
		r.Version = *raw.Version
	}
	if r.Version > FormatVersion {
		r.ReadOnly = true
		r.Problems = append(r.Problems, fmt.Sprintf("format version %d is newer than %d", r.Version, FormatVersion))
	}
	r.Active = strings.ToLower(str(raw.Active))
	for i, rb := range raw.Books {
		b, err := bookFromRaw(rb)
		if err != nil {
			r.ReadOnly = true
			r.Problems = append(r.Problems, fmt.Sprintf("book #%d: %v", i+1, err))
			continue
		}
		r.Books = append(r.Books, b)
	}
	r.sortBooks()
	return r, nil
}

func bookFromRaw(rb rawBook) (Book, error) {
	b := Book{
		ID:        strings.ToLower(strings.TrimSpace(str(rb.ID))),
		Name:      str(rb.Name),
		Kind:      Kind(str(rb.Kind)),
		ServerURL: str(rb.ServerURL),
		Account:   str(rb.Account),
		VaultID:   strings.ToLower(strings.TrimSpace(str(rb.VaultID))),
		Path:      str(rb.Path),
	}
	if b.ID == "" {
		return Book{}, errors.New("missing id")
	}
	switch b.Kind {
	case KindLocal, KindBound:
	default:
		return Book{}, fmt.Errorf("unknown kind %q", b.Kind)
	}
	if rb.Sort != nil {
		b.Sort = *rb.Sort
	}
	var err error
	if b.AddedAt, err = parseTime(str(rb.AddedAt)); err != nil {
		return Book{}, fmt.Errorf("added_at: %w", err)
	}
	if b.LastOpenedAt, err = parseTime(str(rb.LastOpenedAt)); err != nil {
		return Book{}, fmt.Errorf("last_opened_at: %w", err)
	}
	return b, nil
}

func str(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func parseTime(s string) (time.Time, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}, nil
	}
	for _, layout := range []string{time.RFC3339, time.RFC3339Nano} {
		if t, err := time.Parse(layout, s); err == nil {
			return t.UTC().Truncate(time.Second), nil
		}
	}
	return time.Time{}, fmt.Errorf("bad timestamp %q", s)
}

// Load reads books.yaml at path. A missing file yields (nil, nil) so the
// caller can decide whether to migrate.
func Load(path string) (*Registry, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	return Parse(data)
}

// --- writing ---------------------------------------------------------

// Marshal renders the canonical byte form.
func (r *Registry) Marshal() []byte {
	r.sortBooks()
	var b strings.Builder
	b.WriteString(Header)
	b.WriteString("\n")
	b.WriteString("version: " + strconv.Itoa(FormatVersion) + "\n")
	b.WriteString("active: " + bare(r.Active) + "\n")
	if len(r.Books) == 0 {
		b.WriteString("books: []\n")
		return []byte(b.String())
	}
	b.WriteString("books:\n")
	for _, bk := range r.Books {
		b.WriteString("  - id: " + bare(bk.ID) + "\n")
		b.WriteString("    name: " + quoted(bk.Name) + "\n")
		b.WriteString("    kind: " + string(bk.Kind) + "\n")
		b.WriteString("    server_url: " + quotedOrNull(bk.ServerURL) + "\n")
		b.WriteString("    account: " + quotedOrNull(bk.Account) + "\n")
		b.WriteString("    vault_id: " + bare(bk.VaultID) + "\n")
		b.WriteString("    path: " + quotedOrNull(bk.Path) + "\n")
		b.WriteString("    sort: " + strconv.Itoa(bk.Sort) + "\n")
		b.WriteString("    added_at: " + stamp(bk.AddedAt) + "\n")
		b.WriteString("    last_opened_at: " + stamp(bk.LastOpenedAt) + "\n")
	}
	return []byte(b.String())
}

func bare(s string) string {
	if s == "" {
		return "null"
	}
	return s
}

func quotedOrNull(s string) string {
	if s == "" {
		return "null"
	}
	return quoted(s)
}

// quoted renders a YAML double-quoted scalar. Only `\` , `"` and control
// characters are escaped; everything else (including non-ASCII) is
// emitted as UTF-8.
func quoted(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '\\':
			b.WriteString(`\\`)
		case '"':
			b.WriteString(`\"`)
		case '\n':
			b.WriteString(`\n`)
		case '\t':
			b.WriteString(`\t`)
		case '\r':
			b.WriteString(`\r`)
		default:
			if r < 0x20 || r == 0x7f {
				fmt.Fprintf(&b, `\x%02x`, r)
			} else if r == utf8.RuneError {
				b.WriteString(`�`)
			} else {
				b.WriteRune(r)
			}
		}
	}
	b.WriteByte('"')
	return b.String()
}

func stamp(t time.Time) string {
	if t.IsZero() {
		return "null"
	}
	return t.UTC().Format("2006-01-02T15:04:05Z")
}

// Save writes the registry atomically (tmpfile + rename) at 0600, creating
// the parent directory at 0700. Refuses when ReadOnly.
func (r *Registry) Save(path string) error {
	if r.ReadOnly {
		return ErrReadOnly
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("mkdir %s: %w", dir, err)
	}
	tmp, err := os.CreateTemp(dir, ".books-*.yaml")
	if err != nil {
		return fmt.Errorf("create tmp: %w", err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath) // no-op after a successful rename
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return fmt.Errorf("chmod tmp: %w", err)
	}
	if _, err := tmp.Write(r.Marshal()); err != nil {
		tmp.Close()
		return fmt.Errorf("write tmp: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("sync tmp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close tmp: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("rename to %s: %w", path, err)
	}
	return os.Chmod(path, 0o600)
}

// --- queries & mutations --------------------------------------------

func (r *Registry) sortBooks() {
	sort.SliceStable(r.Books, func(i, j int) bool {
		if r.Books[i].Sort != r.Books[j].Sort {
			return r.Books[i].Sort < r.Books[j].Sort
		}
		return r.Books[i].ID < r.Books[j].ID
	})
}

// Get returns the book with id, or nil.
func (r *Registry) Get(id string) *Book {
	id = strings.ToLower(strings.TrimSpace(id))
	for i := range r.Books {
		if r.Books[i].ID == id {
			return &r.Books[i]
		}
	}
	return nil
}

// ByVaultID returns the bound book for a server vault id, or nil.
func (r *Registry) ByVaultID(vaultID string) *Book {
	vaultID = strings.ToLower(strings.TrimSpace(vaultID))
	if vaultID == "" {
		return nil
	}
	for i := range r.Books {
		if r.Books[i].IsBound() && r.Books[i].VaultID == vaultID {
			return &r.Books[i]
		}
	}
	return nil
}

// ByPath returns the book whose folder is absPath, or nil.
func (r *Registry) ByPath(absPath string) *Book {
	want := cleanPath(absPath)
	if want == "" {
		return nil
	}
	for i := range r.Books {
		if r.Books[i].Path != "" && cleanPath(r.Books[i].AbsPath()) == want {
			return &r.Books[i]
		}
	}
	return nil
}

// Find resolves a user-supplied reference: a book id, a 1-based position
// in picker order, a folder path, or a (case-insensitive) name. Returns
// nil when nothing or more than one book matches by name.
func (r *Registry) Find(ref string) *Book {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return nil
	}
	if b := r.Get(ref); b != nil {
		return b
	}
	if n, err := strconv.Atoi(ref); err == nil && n >= 1 && n <= len(r.Books) {
		r.sortBooks()
		return &r.Books[n-1]
	}
	if abs, err := filepath.Abs(ExpandHome(ref)); err == nil {
		if b := r.ByPath(abs); b != nil {
			return b
		}
	}
	var hit *Book
	for i := range r.Books {
		if strings.EqualFold(r.Books[i].Name, ref) {
			if hit != nil {
				return nil
			}
			hit = &r.Books[i]
		}
	}
	return hit
}

// NextSort returns the sort value for a book appended at the end.
func (r *Registry) NextSort() int {
	max := 0
	for _, b := range r.Books {
		if b.Sort > max {
			max = b.Sort
		}
	}
	return max + SortStep
}

// Add appends a book (assigning Sort when zero) and returns a pointer to
// it. It enforces the §2.4 invariant: at most one bound book per vault_id.
func (r *Registry) Add(b Book) (*Book, error) {
	b.ID = strings.ToLower(b.ID)
	b.VaultID = strings.ToLower(b.VaultID)
	if b.ID == "" {
		return nil, errors.New("book id is required")
	}
	if r.Get(b.ID) != nil {
		return nil, fmt.Errorf("book %s already exists", b.ID)
	}
	if b.IsBound() && b.VaultID != "" && r.ByVaultID(b.VaultID) != nil {
		return nil, fmt.Errorf("vault %s is already bound to another book", b.VaultID)
	}
	if b.Sort == 0 {
		b.Sort = r.NextSort()
	}
	if b.AddedAt.IsZero() {
		b.AddedAt = time.Now().UTC()
	}
	b.AddedAt = b.AddedAt.UTC().Truncate(time.Second)
	r.Books = append(r.Books, b)
	r.sortBooks()
	return r.Get(b.ID), nil
}

// Remove drops the book with id. Returns true when a row was removed.
// Clears Active when it pointed at the removed book.
func (r *Registry) Remove(id string) bool {
	id = strings.ToLower(id)
	for i := range r.Books {
		if r.Books[i].ID == id {
			r.Books = append(r.Books[:i], r.Books[i+1:]...)
			if r.Active == id {
				r.Active = ""
			}
			return true
		}
	}
	return false
}

// ActiveBook resolves the active book: the recorded one when it still
// exists, otherwise the one with the lowest sort (porvia 0033 §2). Nil
// when the registry is empty.
func (r *Registry) ActiveBook() *Book {
	if b := r.Get(r.Active); b != nil {
		return b
	}
	if len(r.Books) == 0 {
		return nil
	}
	r.sortBooks()
	return &r.Books[0]
}

// SetActive records id as the active book and bumps its last_opened_at.
func (r *Registry) SetActive(id string, now time.Time) error {
	b := r.Get(id)
	if b == nil {
		return fmt.Errorf("no book %q", id)
	}
	r.Active = b.ID
	b.LastOpenedAt = now.UTC().Truncate(time.Second)
	return nil
}

// Move shifts a book one position up (delta<0) or down (delta>0) in picker
// order and renumbers every book in SortStep steps.
func (r *Registry) Move(id string, delta int) bool {
	r.sortBooks()
	idx := -1
	for i := range r.Books {
		if r.Books[i].ID == strings.ToLower(id) {
			idx = i
			break
		}
	}
	to := idx + delta
	if idx < 0 || to < 0 || to >= len(r.Books) {
		return false
	}
	r.Books[idx], r.Books[to] = r.Books[to], r.Books[idx]
	for i := range r.Books {
		r.Books[i].Sort = (i + 1) * SortStep
	}
	return true
}

// Unbind turns a bound book into a local one (§5 "Desligar"). The server
// vault is never touched.
func (b *Book) Unbind() {
	b.Kind = KindLocal
	b.ServerURL = ""
	b.Account = ""
	b.VaultID = ""
	b.Alternates = nil
}

// ExpandHome replaces a leading `~` with the user's home directory.
func ExpandHome(p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, strings.TrimPrefix(p[1:], "/"))
		}
	}
	return p
}

// EncodeHome replaces a leading $HOME with `~` so stored paths are
// portable between machines with the same home layout.
func EncodeHome(abs string) string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return abs
	}
	home = filepath.Clean(home)
	clean := filepath.Clean(abs)
	if clean == home {
		return "~"
	}
	if strings.HasPrefix(clean, home+string(filepath.Separator)) {
		return "~/" + filepath.ToSlash(strings.TrimPrefix(clean, home+string(filepath.Separator)))
	}
	return abs
}

func cleanPath(p string) string {
	if p == "" {
		return ""
	}
	c := filepath.Clean(p)
	if r, err := filepath.EvalSymlinks(c); err == nil {
		return r
	}
	return c
}
