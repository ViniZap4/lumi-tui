package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/vinizap/lumi/tui-client/account"
	"github.com/vinizap/lumi/tui-client/domain"
	"github.com/vinizap/lumi/tui-client/filesystem"
)

// syncOpts captures parsed flags + positional args for `lumi vault sync`.
type syncOpts struct {
	Server string
	Slug   string
	Path   string // optional; defaults to the registered vault path or ./<slug>
	DryRun bool
}

const syncUsage = `lumi vault sync — push local edits and pull remote changes for a server vault

Usage:
  lumi vault sync <server-url>/<slug> [<path>] [--dry-run]
  lumi vault sync --server <url> --slug <slug> [<path>] [--dry-run]

Behaviour:
  * Push: every local *.md file whose frontmatter has an 'id:' is sent
    via POST /api/vaults/:vault/notes/:id/diff. The server CRDT merges
    the diff against any concurrent edits, returns the post-merge body,
    and we mirror it back to disk (frontmatter preserved).
  * Pull: every remote note whose path isn't on disk is fetched via
    GET .../snapshot and written locally with synthesized frontmatter.
  * Files without an 'id:' frontmatter are skipped — sync identifies
    by id, not path. Use 'lumi vault clone' to seed a vault for the
    first time.
  * No path is provided: the vault's registered path in vaults.yaml is
    used. Falls back to ./<slug>.

Flags:
  --server <url>    Server URL (when not embedded in the first arg).
  --slug <slug>     Vault slug (when not embedded in the first arg).
  --dry-run         Print the planned push/pull/skip set without writing
                    anything (local OR remote).

Notes:
  * Sync does NOT delete files locally or remotely. Use 'rm' + a future
    'lumi note delete' for that.
  * Sync does NOT create new notes server-side. Files without 'id:' are
    skipped (write the id via your editor or 'lumi note create').
`

var errSyncHelp = errors.New("vault sync: help requested")

// parseSyncArgs parses the CLI surface. Tested directly without I/O.
func parseSyncArgs(args []string) (syncOpts, error) {
	var opts syncOpts
	positional := []string{}
	i := 0
	for i < len(args) {
		a := args[i]
		switch a {
		case "--help", "-h":
			return syncOpts{}, errSyncHelp
		case "--server":
			if i+1 >= len(args) {
				return opts, fmt.Errorf("--server requires a value")
			}
			opts.Server = args[i+1]
			i += 2
			continue
		case "--slug":
			if i+1 >= len(args) {
				return opts, fmt.Errorf("--slug requires a value")
			}
			opts.Slug = args[i+1]
			i += 2
			continue
		case "--dry-run":
			opts.DryRun = true
			i++
			continue
		default:
			if strings.HasPrefix(a, "-") {
				return opts, fmt.Errorf("unknown flag %q", a)
			}
			positional = append(positional, a)
			i++
		}
	}

	switch len(positional) {
	case 0:
		// --server + --slug must both be set.
	case 1:
		if opts.Server == "" || opts.Slug == "" {
			s, sl, err := splitServerAndSlug(positional[0])
			if err != nil {
				return opts, err
			}
			opts.Server = s
			opts.Slug = sl
		} else {
			opts.Path = positional[0]
		}
	case 2:
		s, sl, err := splitServerAndSlug(positional[0])
		if err != nil {
			return opts, err
		}
		if opts.Server != "" && opts.Server != s {
			return opts, fmt.Errorf("--server %q conflicts with positional %q", opts.Server, s)
		}
		if opts.Slug != "" && opts.Slug != sl {
			return opts, fmt.Errorf("--slug %q conflicts with positional %q", opts.Slug, sl)
		}
		opts.Server = s
		opts.Slug = sl
		opts.Path = positional[1]
	default:
		return opts, fmt.Errorf("unexpected extra arguments: %v", positional[2:])
	}

	if opts.Server == "" {
		return opts, fmt.Errorf("server URL is required (positional <url>/<slug> or --server)")
	}
	if opts.Slug == "" {
		return opts, fmt.Errorf("vault slug is required (positional <url>/<slug> or --slug)")
	}
	return opts, nil
}

// runVaultSyncCmd implements `lumi vault sync`.
func runVaultSyncCmd(args []string, stdout, stderr io.Writer) int {
	opts, err := parseSyncArgs(args)
	if err != nil {
		if err == errSyncHelp {
			fmt.Fprint(stdout, syncUsage)
			return 0
		}
		fmt.Fprintf(stderr, "Error: %v\n\n", err)
		fmt.Fprint(stderr, syncUsage)
		return 64
	}

	file, err := account.Load()
	if err != nil {
		fmt.Fprintf(stderr, "Error reading accounts.yaml: %v\n", err)
		return 1
	}
	var acc *account.Account
	for i, a := range file.Accounts {
		if strings.EqualFold(a.Server, opts.Server) {
			acc = &file.Accounts[i]
			break
		}
	}
	if acc == nil {
		fmt.Fprintf(stderr,
			"Error: no signed-in account for %s. Run 'lumi login %s' first.\n",
			opts.Server, opts.Server)
		return 1
	}
	if acc.IsExpired() {
		fmt.Fprintf(stderr,
			"Error: account token for %s is expired. Run 'lumi login %s' to refresh.\n",
			opts.Server, opts.Server)
		return 1
	}

	client, err := account.NewClient(opts.Server)
	if err != nil {
		fmt.Fprintf(stderr, "Error: %v\n", err)
		return 1
	}
	client.SetToken(acc.Token)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	// Resolve slug → vault.
	vaults, err := client.ListVaults(ctx)
	if err != nil {
		fmt.Fprintf(stderr, "Error: list vaults: %v\n", err)
		return 1
	}
	var vault *account.RemoteVault
	for i, v := range vaults {
		if v.Slug == opts.Slug {
			vault = &vaults[i]
			break
		}
	}
	if vault == nil {
		fmt.Fprintf(stderr, "Error: vault slug %q not found at %s (you have %d vault(s) visible).\n",
			opts.Slug, opts.Server, len(vaults))
		return 1
	}

	absTarget, err := resolveSyncPath(opts, vault.ID)
	if err != nil {
		fmt.Fprintf(stderr, "Error: %v\n", err)
		return 1
	}
	if err := os.MkdirAll(absTarget, 0o755); err != nil {
		fmt.Fprintf(stderr, "Error: create %q: %v\n", absTarget, err)
		return 1
	}

	notes, err := client.ListAllNotes(ctx, vault.ID)
	if err != nil {
		fmt.Fprintf(stderr, "Error: list notes: %v\n", err)
		return 1
	}
	remoteByID := make(map[string]account.RemoteNote, len(notes))
	for _, n := range notes {
		remoteByID[n.ID] = n
	}

	localByID, skipped, err := scanLocalNotes(absTarget)
	if err != nil {
		fmt.Fprintf(stderr, "Error: scan local notes: %v\n", err)
		return 1
	}

	stats := syncStats{}

	// Push local files that the server knows about (have matching id).
	// Files whose id is unknown to the server are skipped — sync does
	// not create. The user can use 'lumi note create' later.
	for id, local := range localByID {
		if _, ok := remoteByID[id]; !ok {
			stats.PushSkippedUnknownID++
			fmt.Fprintf(stdout, "  skip-push (id not on server)  %s\n", relTo(absTarget, local.Path))
			continue
		}
		if opts.DryRun {
			stats.PushPlanned++
			fmt.Fprintf(stdout, "  push (planned)                %s\n", relTo(absTarget, local.Path))
			continue
		}
		merged, err := client.ApplyNoteDiff(ctx, vault.ID, id, "", local.Content, "tui-diff")
		if err != nil {
			fmt.Fprintf(stderr, "Error: push %s: %v\n", relTo(absTarget, local.Path), err)
			stats.PushFailed++
			continue
		}
		// Mirror the merged body back to disk, preserving the existing
		// frontmatter. The server may have woven in concurrent edits.
		local.Content = merged.Text
		if err := filesystem.WriteNote(local); err != nil {
			fmt.Fprintf(stderr, "Error: write merged body %s: %v\n", local.Path, err)
			stats.PushFailed++
			continue
		}
		stats.Pushed++
		fmt.Fprintf(stdout, "  pushed                        %s\n", relTo(absTarget, local.Path))
	}

	// Pull remote notes that aren't on disk locally. Existing files are
	// not overwritten on pull — the push pass already merged their
	// content via the CRDT.
	for _, n := range notes {
		if _, ok := localByID[n.ID]; ok {
			continue
		}
		if err := assertSafeRelPath(n.Path); err != nil {
			fmt.Fprintf(stderr, "Error: refusing to pull %s (id %s): %v\n", n.Path, n.ID, err)
			stats.PullFailed++
			continue
		}
		dest := filepath.Join(absTarget, filepath.Clean(n.Path))
		if opts.DryRun {
			stats.PullPlanned++
			fmt.Fprintf(stdout, "  pull (planned)                %s\n", relTo(absTarget, dest))
			continue
		}
		snap, err := client.GetNoteSnapshot(ctx, vault.ID, n.ID)
		if err != nil {
			fmt.Fprintf(stderr, "Error: pull %s: %v\n", n.Path, err)
			stats.PullFailed++
			continue
		}
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			fmt.Fprintf(stderr, "Error: mkdir %s: %v\n", filepath.Dir(dest), err)
			stats.PullFailed++
			continue
		}
		note := buildNoteFromRemote(n, snap, dest)
		if err := filesystem.WriteNote(note); err != nil {
			fmt.Fprintf(stderr, "Error: write %s: %v\n", dest, err)
			stats.PullFailed++
			continue
		}
		stats.Pulled++
		fmt.Fprintf(stdout, "  pulled                        %s\n", relTo(absTarget, dest))
	}

	if skipped > 0 {
		fmt.Fprintf(stdout,
			"  (%d local file(s) skipped — no 'id:' frontmatter; use 'lumi note create' to push as new)\n",
			skipped)
	}

	stats.SkippedNoID = skipped
	fmt.Fprint(stdout, stats.Summary(opts.DryRun))
	if stats.PushFailed+stats.PullFailed > 0 {
		return 2
	}
	return 0
}

// resolveSyncPath picks the on-disk location for sync. Precedence:
//
//  1. Explicit positional <path>.
//  2. Registered vault row in vaults.yaml (matched by id).
//  3. Fallback to ./<slug> in CWD.
func resolveSyncPath(opts syncOpts, vaultID string) (string, error) {
	if opts.Path != "" {
		abs, err := filepath.Abs(opts.Path)
		if err != nil {
			return "", fmt.Errorf("resolve path %q: %w", opts.Path, err)
		}
		return abs, nil
	}
	if entries, err := account.LoadVaults(); err == nil {
		for _, e := range entries {
			if strings.EqualFold(e.ID, vaultID) && e.Path != "" {
				return e.Path, nil
			}
		}
	}
	// Last resort — assume the user wants ./<slug>.
	return filepath.Abs(opts.Slug)
}

// scanLocalNotes walks `root` for *.md files, parses each one's
// frontmatter, and returns the map of id→note for files that carry an
// id. Files without an id are counted in `skipped` so the user knows
// how many were ignored. Hidden directories (.git, .lumi, etc.) are
// skipped wholesale.
func scanLocalNotes(root string) (map[string]*domain.Note, int, error) {
	out := make(map[string]*domain.Note)
	skipped := 0
	err := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			name := d.Name()
			if name != "." && strings.HasPrefix(name, ".") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(strings.ToLower(d.Name()), ".md") {
			return nil
		}
		note, readErr := filesystem.ReadNote(p)
		if readErr != nil && note == nil {
			return fmt.Errorf("read %s: %w", p, readErr)
		}
		if note == nil || note.ID == "" {
			skipped++
			return nil
		}
		out[note.ID] = note
		return nil
	})
	if err != nil {
		return nil, 0, err
	}
	return out, skipped, nil
}

// buildNoteFromRemote constructs a fresh domain.Note ready for a first
// write to disk. Frontmatter is synthesized from the list-row metadata
// (we have id/title/timestamps) plus the snapshot body.
func buildNoteFromRemote(meta account.RemoteNote, snap *account.NoteSnapshot, dest string) *domain.Note {
	note := &domain.Note{
		ID:             meta.ID,
		Title:          meta.Title,
		Path:           dest,
		Content:        snap.Text,
		HadFrontmatter: true,
	}
	if t, err := time.Parse(time.RFC3339, meta.CreatedAt); err == nil {
		note.CreatedAt = t
	}
	if t, err := time.Parse(time.RFC3339, meta.UpdatedAt); err == nil {
		note.UpdatedAt = t
	}
	return note
}

// assertSafeRelPath rejects server-supplied paths that would escape the
// vault root (..) or be absolute. Mirrors the defensive check inside
// cmd_vault_clone.go's downloadNote — the server should never emit
// such paths, but don't trust the wire.
func assertSafeRelPath(p string) error {
	if p == "" {
		return fmt.Errorf("empty path")
	}
	clean := filepath.Clean(p)
	if filepath.IsAbs(clean) {
		return fmt.Errorf("absolute path %q rejected", p)
	}
	if clean == ".." || strings.HasPrefix(clean, "../") || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return fmt.Errorf("traversal path %q rejected", p)
	}
	return nil
}

// relTo returns the path of `target` relative to `root` for display
// purposes. Falls back to the full path when the relative computation
// fails (cross-volume on weird filesystems).
func relTo(root, target string) string {
	if rel, err := filepath.Rel(root, target); err == nil {
		return rel
	}
	return target
}

// syncStats accumulates push/pull counters for the end-of-run summary.
type syncStats struct {
	Pushed               int
	PushPlanned          int
	PushSkippedUnknownID int
	PushFailed           int
	Pulled               int
	PullPlanned          int
	PullFailed           int
	SkippedNoID          int
}

func (s syncStats) Summary(dry bool) string {
	if dry {
		return fmt.Sprintf("Dry-run: %d push, %d pull, %d skip (unknown id), %d skip (no id).\n",
			s.PushPlanned, s.PullPlanned, s.PushSkippedUnknownID, s.SkippedNoID)
	}
	return fmt.Sprintf("Pushed %d; pulled %d; skipped %d (no id), %d (unknown id); %d push errors, %d pull errors.\n",
		s.Pushed, s.Pulled, s.SkippedNoID, s.PushSkippedUnknownID, s.PushFailed, s.PullFailed)
}
