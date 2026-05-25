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
)

// cloneOpts captures parsed flags + positional args for `lumi vault clone`.
type cloneOpts struct {
	Server string // accept either explicit --server or <url>/<slug>
	Slug   string
	Path   string // optional; defaults to ./<slug>
	Name   string // optional display name; defaults to vault's server-side name
	Force  bool   // overwrite existing files in <path>
}

const cloneUsage = `lumi vault clone — pull a server-hosted vault to a local directory

Usage:
  lumi vault clone <server-url>/<slug> [<path>] [--name <name>] [--force]
  lumi vault clone --server <url> --slug <slug> [<path>] [--name <name>] [--force]

Behaviour:
  * Reads ~/.config/lumi/accounts.yaml to find a signed-in account for
    the target server. Run 'lumi login <server-url>' first.
  * Lists every note in the vault (paginated) and downloads each via
    GET /api/vaults/:vault/notes/:id/content. Files land at <path>/<note.path>.
  * Skips existing on-disk files unless --force is passed (so re-running
    a partial clone is safe).
  * Registers the resulting directory in ~/.config/lumi/vaults.yaml so
    it shows up in the picker and 'lumi vaults'.

Flags:
  --server <url>    Server URL (when not embedded in the first arg).
  --slug <slug>     Vault slug (when not embedded in the first arg).
  --name <name>     Display name for the local registry row.
                    Defaults to the server's vault name.
  --force           Overwrite existing files in <path>.
`

// parseCloneArgs interprets the CLI surface as either:
//
//	lumi vault clone <server-url>/<slug> [<path>]
//	lumi vault clone --server <url> --slug <slug> [<path>]
//
// Tested directly without I/O.
func parseCloneArgs(args []string) (cloneOpts, error) {
	var opts cloneOpts
	positional := []string{}
	i := 0
	for i < len(args) {
		a := args[i]
		switch a {
		case "--help", "-h":
			return cloneOpts{}, errCloneHelp
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
		case "--name":
			if i+1 >= len(args) {
				return opts, fmt.Errorf("--name requires a value")
			}
			opts.Name = args[i+1]
			i += 2
			continue
		case "--force":
			opts.Force = true
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

	// First positional may be `<server-url>/<slug>` or, when --server/--slug
	// are set, the optional <path>.
	switch len(positional) {
	case 0:
		// No positionals — --server + --slug must both be set.
	case 1:
		if opts.Server == "" || opts.Slug == "" {
			// Treat as `<server-url>/<slug>` shorthand.
			s, sl, err := splitServerAndSlug(positional[0])
			if err != nil {
				return opts, err
			}
			opts.Server = s
			opts.Slug = sl
		} else {
			// --server + --slug set; positional is the path.
			opts.Path = positional[0]
		}
	case 2:
		// First positional is `<server-url>/<slug>`, second is <path>.
		s, sl, err := splitServerAndSlug(positional[0])
		if err != nil {
			return opts, err
		}
		// Allow --server/--slug to override the embedded form if they
		// disagree — we'd rather fail than silently pick.
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

var errCloneHelp = errors.New("vault clone: help requested")

// splitServerAndSlug breaks `https://host/path/slug` into
// (`https://host/path`, `slug`). The slug is the last path segment;
// everything before is the server URL. Required: a non-empty scheme + host.
func splitServerAndSlug(combined string) (string, string, error) {
	idx := strings.LastIndex(combined, "/")
	if idx < 0 {
		return "", "", fmt.Errorf("expected `<server-url>/<slug>`, got %q", combined)
	}
	server := combined[:idx]
	slug := combined[idx+1:]
	if !strings.HasPrefix(server, "http://") && !strings.HasPrefix(server, "https://") {
		return "", "", fmt.Errorf("server URL must start with http:// or https://, got %q", combined)
	}
	if slug == "" {
		return "", "", fmt.Errorf("slug is empty in %q", combined)
	}
	return server, slug, nil
}

// runVaultCloneCmd implements `lumi vault clone`.
func runVaultCloneCmd(args []string, stdout, stderr io.Writer) int {
	opts, err := parseCloneArgs(args)
	if err != nil {
		if err == errCloneHelp {
			fmt.Fprint(stdout, cloneUsage)
			return 0
		}
		fmt.Fprintf(stderr, "Error: %v\n\n", err)
		fmt.Fprint(stderr, cloneUsage)
		return 64
	}

	// Find an account for the server. Without one we can't authenticate.
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
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
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

	// Decide on-disk location.
	targetPath := opts.Path
	if targetPath == "" {
		targetPath = opts.Slug
	}
	absTarget, err := filepath.Abs(targetPath)
	if err != nil {
		fmt.Fprintf(stderr, "Error: resolve %q: %v\n", targetPath, err)
		return 1
	}
	if err := os.MkdirAll(absTarget, 0o755); err != nil {
		fmt.Fprintf(stderr, "Error: create %q: %v\n", absTarget, err)
		return 1
	}

	// Download every note.
	notes, err := client.ListAllNotes(ctx, vault.ID)
	if err != nil {
		fmt.Fprintf(stderr, "Error: list notes: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "Cloning %d note(s) from %s/%s into %s\n",
		len(notes), opts.Server, opts.Slug, absTarget)

	skipped, written := 0, 0
	for _, n := range notes {
		if err := downloadNote(ctx, client, vault.ID, n, absTarget, opts.Force, &skipped, &written, stdout); err != nil {
			fmt.Fprintf(stderr, "Error: %v\n", err)
			return 1
		}
	}
	fmt.Fprintf(stdout, "Wrote %d file(s); skipped %d existing.\n", written, skipped)

	// Register the new vault locally.
	displayName := opts.Name
	if displayName == "" {
		displayName = vault.Name
	}
	entry := account.VaultEntry{
		ID:      vault.ID,
		Name:    displayName,
		Path:    absTarget,
		RawPath: account.EncodeHomeRelative(absTarget),
		Server:  opts.Server,
		Account: acc.Username,
		AddedAt: time.Now().UTC().Truncate(time.Second),
	}
	if _, err := account.UpsertVault(entry); err != nil {
		// Files downloaded fine; registry write failure is reported but
		// doesn't roll back the disk state — the user can rerun
		// `lumi vault link` to fix it.
		fmt.Fprintf(stderr, "Warning: vault registry write failed: %v\n", err)
		fmt.Fprintf(stderr, "Re-register with: lumi vault link %s --server %s --account %s\n",
			absTarget, opts.Server, acc.Username)
		return 2
	}
	fmt.Fprintf(stdout, "Registered %q at %s.\n", displayName, absTarget)
	return 0
}

// downloadNote reads one note's content via the API and writes it to
// disk, preserving `note.Path` as the relative target. Existing files
// are skipped unless `force` is set. Parents are created on demand.
func downloadNote(
	ctx context.Context,
	client *account.Client,
	vaultID string,
	note account.RemoteNote,
	root string,
	force bool,
	skipped, written *int,
	stdout io.Writer,
) error {
	if note.Path == "" {
		return fmt.Errorf("note %q has empty path", note.ID)
	}
	rel := filepath.Clean(note.Path)
	if strings.HasPrefix(rel, "..") || filepath.IsAbs(rel) {
		// Defensive — the server shouldn't emit traversal paths, but
		// don't trust the wire.
		return fmt.Errorf("rejecting unsafe note path %q", note.Path)
	}
	dest := filepath.Join(root, rel)
	if _, err := os.Stat(dest); err == nil && !force {
		*skipped++
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", filepath.Dir(dest), err)
	}
	content, err := client.GetNoteContent(ctx, vaultID, note.ID)
	if err != nil {
		return fmt.Errorf("fetch %s: %w", note.ID, err)
	}
	body := []byte(content.Body)
	if err := os.WriteFile(dest, body, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", dest, err)
	}
	*written++
	if stdout != nil {
		fmt.Fprintf(stdout, "  %s\n", rel)
	}
	return nil
}
