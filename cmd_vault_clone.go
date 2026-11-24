package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"time"

	"github.com/vinizap/lumi/tui-client/account"
	"github.com/vinizap/lumi/tui-client/bookclone"
	"github.com/vinizap/lumi/tui-client/books"
	"github.com/vinizap/lumi/tui-client/feed"
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
	args, accountFlag, aerr := splitAccountFlag(args)
	if aerr != nil {
		fmt.Fprintf(stderr, "Error: %v\n", aerr)
		return 64
	}
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
	acc, code := lookupAccount(opts.Server, accountFlag, stderr)
	if code != 0 {
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

	// The shared clone path (bookclone.Run) — the TUI's clone-in-place
	// runs exactly the same steps.
	res, err := bookclone.Run(ctx, bookclone.Options{
		Client: client, Server: opts.Server, Account: acc.Username,
		Slug: opts.Slug, Path: absTarget, Name: opts.Name, Force: opts.Force,
		OnStart: func(_ account.RemoteVault, total int) {
			fmt.Fprintf(stdout, "Cloning %d note(s) from %s/%s into %s\n",
				total, opts.Server, opts.Slug, absTarget)
		},
		OnNote: func(_, _ int, rel string, wrote bool) {
			if wrote {
				fmt.Fprintf(stdout, "  %s\n", rel)
			}
		},
		UpdateBooks: func(fn func(reg *books.Registry) bool) error {
			updateBooks(stderr, fn)
			return nil
		},
		NewSyncer: func(api feed.NoteAPI, server, vaultID, root string) (*feed.Syncer, func()) {
			s, save := newBookSyncer(api, server, vaultID, root)
			return s, func() { save(stderr) }
		},
	})
	switch bookclone.StageOf(err) {
	case "":
		if err != nil {
			fmt.Fprintf(stderr, "Error: %v\n", err)
			return 1
		}
	case bookclone.StageNotFound:
		fmt.Fprintf(stderr, "Error: vault slug %q not found at %s (you have %d vault(s) visible).\n",
			opts.Slug, opts.Server, res.Visible)
		return 1
	case bookclone.StageMkdir:
		fmt.Fprintf(stderr, "Error: create %q: %v\n", absTarget, errors.Unwrap(err))
		return 1
	case bookclone.StageVaultsYAML:
		fmt.Fprintf(stdout, "Wrote %d file(s); skipped %d existing.\n", res.Written, res.Skipped)
		// Files downloaded fine; registry write failure is reported but
		// doesn't roll back the disk state — the user can rerun
		// `lumi vault link` to fix it.
		fmt.Fprintf(stderr, "Warning: vault registry write failed: %v\n", errors.Unwrap(err))
		fmt.Fprintf(stderr, "Re-register with: lumi vault link %s --server %s --account %s\n",
			absTarget, opts.Server, acc.Username)
		return 2
	default:
		fmt.Fprintf(stderr, "Error: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "Wrote %d file(s); skipped %d existing.\n", res.Written, res.Skipped)
	fmt.Fprintf(stdout, "Registered %q at %s.\n", res.Name, absTarget)
	if res.BaseMisses > 0 {
		fmt.Fprintf(stderr, "Warning: no sync base recorded for %d note(s); their first push will compare with the server copy.\n", res.BaseMisses)
	}
	return 0
}
