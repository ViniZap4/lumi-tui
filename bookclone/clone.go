// Package bookclone is the one code path that pulls a server vault into a
// local folder and registers it as a book: `lumi vault clone` (CLI) and the
// TUI's clone-in-place (books level, `c` / ⏎ on a book without a folder)
// both call Run, so vaults.yaml, books.yaml and the sync bases come out
// byte-for-byte the same either way.
package bookclone

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/vinizap/lumi/tui-client/account"
	"github.com/vinizap/lumi/tui-client/books"
	"github.com/vinizap/lumi/tui-client/feed"
	"github.com/vinizap/lumi/tui-client/filesystem"
)

// Options drives one clone.
type Options struct {
	Client  *account.Client // token already set
	Server  string          // server URL as the caller names it (written to vaults.yaml / books.yaml)
	Account string          // username the token belongs to
	Slug    string          // vault to clone: by slug…
	VaultID string          // …or by id (the TUI knows the book's vault_id)
	Path    string          // absolute target folder
	Name    string          // display name; "" = the server's vault name
	Force   bool            // overwrite existing files

	// OnStart fires once the note list is known.
	OnStart func(v account.RemoteVault, total int)
	// OnNote fires after each note (wrote=false: skipped, already there).
	OnNote func(done, total int, rel string, wrote bool)
	// UpdateBooks applies fn to the book registry and saves it. The CLI
	// opens books.yaml; the TUI goes through its feed manager so the
	// in-memory registry and the file never disagree. nil: skipped.
	UpdateBooks func(fn func(reg *books.Registry) bool) error
	// NewSyncer opens the per-book sync state for recording the clone's
	// bases. nil: DefaultSyncer.
	NewSyncer func(api feed.NoteAPI, server, vaultID, root string) (*feed.Syncer, func())
}

// Result reports what a clone did (also on error, as far as it got).
type Result struct {
	Vault        account.RemoteVault
	Visible      int // vaults visible to the account (for "not found")
	Path         string
	Name         string
	Total        int
	Written      int
	Skipped      int
	WrittenPaths []string // absolute paths written by this run
	CreatedRoot  bool     // Path did not exist before the clone
	BaseMisses   int      // notes without a recorded sync base
	BooksErr     error    // books.yaml update failed (files and vaults.yaml are fine)
}

// Stage names where a clone failed.
type Stage string

const (
	StageListVaults Stage = "list vaults"
	StageNotFound   Stage = "not found"
	StageMkdir      Stage = "create"
	StageListNotes  Stage = "list notes"
	StageDownload   Stage = "download"
	StageVaultsYAML Stage = "vaults.yaml"
)

// Error is a clone failure with its stage.
type Error struct {
	Stage Stage
	Err   error
}

func (e *Error) Error() string {
	if e.Stage == StageDownload || e.Err == nil {
		if e.Err == nil {
			return string(e.Stage)
		}
		return e.Err.Error()
	}
	return string(e.Stage) + ": " + e.Err.Error()
}

func (e *Error) Unwrap() error { return e.Err }

// ErrNotFound is wrapped when the vault is not visible to the account.
var ErrNotFound = errors.New("vault not found")

// StageOf returns the stage of a clone error ("" when it is not one).
func StageOf(err error) Stage {
	var ce *Error
	if errors.As(err, &ce) {
		return ce.Stage
	}
	return ""
}

// Run resolves the vault, downloads every note into opts.Path, registers
// the folder in vaults.yaml and books.yaml and records the sync bases.
// ctx cancellation stops between notes.
func Run(ctx context.Context, opts Options) (Result, error) {
	res := Result{Path: opts.Path}
	vaults, err := opts.Client.ListVaults(ctx)
	if err != nil {
		return res, &Error{StageListVaults, err}
	}
	res.Visible = len(vaults)
	var vault *account.RemoteVault
	for i, v := range vaults {
		if (opts.Slug != "" && v.Slug == opts.Slug) || (opts.VaultID != "" && strings.EqualFold(v.ID, opts.VaultID)) {
			vault = &vaults[i]
			break
		}
	}
	if vault == nil {
		return res, &Error{StageNotFound, ErrNotFound}
	}
	res.Vault = *vault

	if _, err := os.Stat(opts.Path); os.IsNotExist(err) {
		res.CreatedRoot = true
	}
	if err := os.MkdirAll(opts.Path, 0o755); err != nil {
		return res, &Error{StageMkdir, err}
	}

	notes, err := opts.Client.ListAllNotes(ctx, vault.ID)
	if err != nil {
		return res, &Error{StageListNotes, err}
	}
	res.Total = len(notes)
	if opts.OnStart != nil {
		opts.OnStart(*vault, len(notes))
	}
	wroteIDs := map[string]string{}
	for i, n := range notes {
		if err := ctx.Err(); err != nil {
			return res, &Error{StageDownload, err}
		}
		dest, wrote, err := DownloadNote(ctx, opts.Client, vault.ID, n, opts.Path, opts.Force)
		if err != nil {
			return res, &Error{StageDownload, err}
		}
		if wrote {
			res.Written++
			res.WrittenPaths = append(res.WrittenPaths, dest)
			wroteIDs[n.ID] = dest
		} else {
			res.Skipped++
		}
		if opts.OnNote != nil {
			opts.OnNote(i+1, len(notes), filepath.Clean(n.Path), wrote)
		}
	}

	res.Name = opts.Name
	if res.Name == "" {
		res.Name = vault.Name
	}
	entry := account.VaultEntry{
		ID:      vault.ID,
		Name:    res.Name,
		Path:    opts.Path,
		RawPath: account.EncodeHomeRelative(opts.Path),
		Server:  opts.Server,
		Account: opts.Account,
		AddedAt: time.Now().UTC().Truncate(time.Second),
	}
	if _, err := account.UpsertVault(entry); err != nil {
		return res, &Error{StageVaultsYAML, err}
	}
	if opts.UpdateBooks != nil {
		res.BooksErr = opts.UpdateBooks(func(reg *books.Registry) bool {
			return RegisterBook(reg, opts.Server, opts.Account, vault.ID, res.Name, opts.Path)
		})
	}
	newSyncer := opts.NewSyncer
	if newSyncer == nil {
		newSyncer = DefaultSyncer
	}
	res.BaseMisses = RecordBases(ctx, opts.Client, newSyncer, opts.Server, vault.ID, opts.Path, wroteIDs)
	return res, nil
}

// DownloadNote writes one note at root/<note.Path>. An existing file is
// left alone unless force (wrote=false). Traversal paths are rejected.
func DownloadNote(ctx context.Context, client *account.Client, vaultID string, note account.RemoteNote, root string, force bool) (string, bool, error) {
	if note.Path == "" {
		return "", false, fmt.Errorf("note %q has empty path", note.ID)
	}
	rel := filepath.Clean(note.Path)
	if strings.HasPrefix(rel, "..") || filepath.IsAbs(rel) {
		// Defensive — the server shouldn't emit traversal paths, but
		// don't trust the wire.
		return "", false, fmt.Errorf("rejecting unsafe note path %q", note.Path)
	}
	dest := filepath.Join(root, rel)
	if _, err := os.Stat(dest); err == nil && !force {
		return dest, false, nil
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return "", false, fmt.Errorf("mkdir %s: %w", filepath.Dir(dest), err)
	}
	content, err := client.GetNoteContent(ctx, vaultID, note.ID)
	if err != nil {
		return "", false, fmt.Errorf("fetch %s: %w", note.ID, err)
	}
	if err := os.WriteFile(dest, []byte(content.Body), 0o644); err != nil {
		return "", false, fmt.Errorf("write %s: %w", dest, err)
	}
	return dest, true, nil
}

// RecordBases stores the base (snapshot text + state) of every note the
// clone wrote, so the first edit can be pushed 3-way
// (CRDT-CLIENT-PROTOCOL §7.1 step 1). Best effort: returns how many
// notes got no base (their first push re-derives one).
func RecordBases(ctx context.Context, client *account.Client, newSyncer func(feed.NoteAPI, string, string, string) (*feed.Syncer, func()), server, vaultID, root string, wrote map[string]string) int {
	if len(wrote) == 0 {
		return 0
	}
	syncer, save := newSyncer(client, server, vaultID, root)
	defer save()
	missed := 0
	for id, dest := range wrote {
		local, _ := filesystem.ReadNote(dest)
		snap, err := client.GetNoteSnapshot(ctx, vaultID, id)
		if local == nil || err != nil || strings.TrimSpace(snap.Text) != strings.TrimSpace(local.Content) {
			missed++
			continue
		}
		if err := syncer.Record(id, dest, snap); err != nil {
			missed++
		}
	}
	return missed
}

// SyncerFor opens the per-book sync state (cache + bases under
// ~/.cache/lumi) for vaultID, keyed by its book in reg when there is one.
// save persists the cache.
func SyncerFor(api feed.NoteAPI, server, vaultID, root string, reg *books.Registry) (*feed.Syncer, func() error) {
	bookID, bookServer := strings.ToLower(vaultID), strings.TrimRight(server, "/")
	if n, err := account.NormalizeServerURL(bookServer); err == nil {
		bookServer = n
	}
	if reg != nil {
		if b := reg.ByVaultID(vaultID); b != nil {
			bookID, bookServer = b.ID, b.ServerURL
		}
	}
	dir, err := feed.CacheDir()
	if err != nil {
		dir = ""
	}
	cache := feed.LoadCache(dir, bookID, strings.ToLower(vaultID), bookServer)
	s := &feed.Syncer{API: api, VaultID: vaultID, Root: root, Cache: cache, Bases: feed.BasesFor(dir, bookID)}
	return s, func() error { return cache.Save(dir, bookID) }
}

// DefaultSyncer is SyncerFor over books.yaml as it is on disk.
func DefaultSyncer(api feed.NoteAPI, server, vaultID, root string) (*feed.Syncer, func()) {
	var reg *books.Registry
	if st, err := books.Open(); err == nil {
		reg = st.Registry
	}
	s, save := SyncerFor(api, server, vaultID, root, reg)
	return s, func() { _ = save() }
}

// RegisterBook records a freshly cloned vault folder as a bound book
// (id = vault_id, per SPEC-LIVROS §2.3). Returns whether reg changed.
func RegisterBook(reg *books.Registry, server, username, vaultID, name, abs string) bool {
	server = strings.TrimRight(server, "/")
	if n, err := account.NormalizeServerURL(server); err == nil {
		server = n
	}
	if b := reg.ByVaultID(vaultID); b != nil {
		if b.Path == "" || !books.SameDir(b.AbsPath(), abs) {
			b.Path = books.EncodeHome(abs)
		}
		return true
	}
	if b := reg.ByPath(abs); b != nil {
		b.Kind, b.ServerURL, b.Account, b.VaultID = books.KindBound, server, username, strings.ToLower(vaultID)
		return true
	}
	id := strings.ToLower(vaultID)
	if reg.Get(id) != nil {
		id = NewID()
	}
	_, err := reg.Add(books.Book{ID: id, Name: name, Kind: books.KindBound, ServerURL: server,
		Account: username, VaultID: strings.ToLower(vaultID), Path: books.EncodeHome(abs)})
	return err == nil
}

// NewID is a lowercase v4 UUID.
func NewID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// Cleanup removes what a failed or cancelled clone wrote: exactly the
// files in res.WrittenPaths, then the folders left empty under the root
// (and the root itself when the clone created it). Never removes a file
// it did not write, and never a non-empty folder.
func Cleanup(res Result) {
	root := filepath.Clean(res.Path)
	if root == "" || root == "." || root == string(filepath.Separator) {
		return
	}
	dirs := map[string]bool{}
	for _, p := range res.WrittenPaths {
		if !within(root, p) {
			continue
		}
		_ = os.Remove(p)
		for d := filepath.Dir(p); within(root, d) && d != root; d = filepath.Dir(d) {
			dirs[d] = true
		}
	}
	if res.CreatedRoot {
		// the clone made every folder under its new root
		_ = filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
			if err == nil && d.IsDir() && p != root {
				dirs[p] = true
			}
			return nil
		})
	}
	// deepest first
	for len(dirs) > 0 {
		deep := ""
		for d := range dirs {
			if len(d) > len(deep) {
				deep = d
			}
		}
		_ = os.Remove(deep) // fails (and is ignored) when not empty
		delete(dirs, deep)
	}
	if res.CreatedRoot {
		_ = os.Remove(root)
	}
}

func within(root, p string) bool {
	rel, err := filepath.Rel(root, p)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}
