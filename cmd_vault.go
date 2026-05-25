package main

import (
	"crypto/rand"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/vinizap/lumi/tui-client/account"
)

const vaultUsage = `lumi vault — manage vault registry entries

Usage:
  lumi vault link <path> [--name <name>] [--server <url>] [--account <username>]
      Register a vault directory in ~/.config/lumi/vaults.yaml so it
      shows up in the startup picker and in 'lumi vaults'. Does NOT
      touch the server — server-side create / clone is a later slice.

  lumi vault unlink <id-or-path>
      Drop a row from vaults.yaml. <id-or-path> may be a UUID (matched
      against the row's id) or a directory path (matched against the
      stored Path after filepath.Clean). The vault files on disk are
      NOT touched — unlink is purely a registry operation.

  lumi vault clone <server-url>/<slug> [<path>] [--name <name>] [--force]
      Download every note from a server-hosted vault to a local
      directory and register it. Requires 'lumi login <server-url>'
      first. Re-running on an existing directory skips files that are
      already there unless --force is passed.

Flags (link):
  --name <name>       Display name (default: basename of <path>).
  --server <url>      Bind to a server URL. Must match a row in
                      ~/.config/lumi/accounts.yaml unless --account is
                      explicitly passed.
  --account <user>    Account username on <server>. Implies --server-
                      bound vault.

Notes:
  * link: <path> must be an existing directory.
  * link: Re-running with a new id overwrites no existing rows; pass
    the existing id (via vaults.yaml or 'lumi vaults') to upsert.
  * unlink: never deletes the actual files. Use 'rm -rf' yourself if
    you want the vault gone from disk.
`

// runVaultCmd dispatches `lumi vault <subcommand>`. Slice 4 ships only
// `link`; `clone`, `unlink`, and `export` are queued for later slices.
func runVaultCmd(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, vaultUsage)
		return 64
	}
	switch args[0] {
	case "--help", "-h", "help":
		fmt.Fprint(stdout, vaultUsage)
		return 0
	case "link":
		return runVaultLinkCmd(args[1:], stdout, stderr)
	case "unlink":
		return runVaultUnlinkCmd(args[1:], stdout, stderr)
	case "clone":
		return runVaultCloneCmd(args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "Error: unknown vault subcommand %q\n\n", args[0])
		fmt.Fprint(stderr, vaultUsage)
		return 64
	}
}

// vaultLinkOpts holds parsed flags for `lumi vault link`. Extracted so
// the test can drive the planner without a tempfile dance.
type vaultLinkOpts struct {
	Path    string
	Name    string
	Server  string
	Account string
}

// parseVaultLinkArgs is the pure CLI parser. Returns an opts struct or
// an error suitable for the user. Tested directly; the I/O happens in
// the caller.
func parseVaultLinkArgs(args []string) (vaultLinkOpts, error) {
	var opts vaultLinkOpts
	i := 0
	for i < len(args) {
		a := args[i]
		switch a {
		case "--help", "-h":
			return vaultLinkOpts{}, errVaultLinkHelp
		case "--name":
			if i+1 >= len(args) {
				return opts, fmt.Errorf("--name requires a value")
			}
			opts.Name = args[i+1]
			i += 2
			continue
		case "--server":
			if i+1 >= len(args) {
				return opts, fmt.Errorf("--server requires a value")
			}
			opts.Server = args[i+1]
			i += 2
			continue
		case "--account":
			if i+1 >= len(args) {
				return opts, fmt.Errorf("--account requires a value")
			}
			opts.Account = args[i+1]
			i += 2
			continue
		default:
			if strings.HasPrefix(a, "-") {
				return opts, fmt.Errorf("unknown flag %q", a)
			}
			if opts.Path != "" {
				return opts, fmt.Errorf("unexpected extra argument %q", a)
			}
			opts.Path = a
			i++
		}
	}
	if opts.Path == "" {
		return opts, fmt.Errorf("path is required")
	}
	// If --account is set without --server, that's an underspec —
	// reject rather than guessing.
	if opts.Account != "" && opts.Server == "" {
		return opts, fmt.Errorf("--account requires --server")
	}
	return opts, nil
}

// errVaultLinkHelp is a sentinel returned by parseVaultLinkArgs when
// the user passed --help. The caller renders usage and exits 0.
var errVaultLinkHelp = fmt.Errorf("vault link: help requested")

func runVaultLinkCmd(args []string, stdout, stderr io.Writer) int {
	opts, err := parseVaultLinkArgs(args)
	if err != nil {
		if err == errVaultLinkHelp {
			fmt.Fprint(stdout, vaultUsage)
			return 0
		}
		fmt.Fprintf(stderr, "Error: %v\n\n", err)
		fmt.Fprint(stderr, vaultUsage)
		return 64
	}

	abs, err := filepath.Abs(opts.Path)
	if err != nil {
		fmt.Fprintf(stderr, "Error: resolve path %q: %v\n", opts.Path, err)
		return 1
	}
	info, err := os.Stat(abs)
	if err != nil {
		fmt.Fprintf(stderr, "Error: cannot stat %q: %v\n", abs, err)
		return 1
	}
	if !info.IsDir() {
		fmt.Fprintf(stderr, "Error: %q is not a directory\n", abs)
		return 1
	}

	// Optional cross-check: when --server is given, prefer it to also
	// be a server the user has signed in to. This catches typos like
	// `https://lumi.work.com/` vs `https://lumi.work.com`. Pure warning
	// — the registry entry still writes, since the user might be
	// linking ahead of a future `lumi login`.
	if opts.Server != "" {
		if file, err := account.Load(); err == nil && file != nil {
			matched := false
			for _, a := range file.Accounts {
				if strings.EqualFold(a.Server, opts.Server) {
					matched = true
					if opts.Account != "" && !strings.EqualFold(a.Username, opts.Account) {
						continue
					}
					break
				}
			}
			if !matched {
				fmt.Fprintf(stderr,
					"Warning: %q has no matching account in ~/.config/lumi/accounts.yaml. Run `lumi login %s` later if you intend to sync.\n",
					opts.Server, opts.Server)
			}
		}
	}

	name := opts.Name
	if name == "" {
		name = filepath.Base(abs)
		if name == "" || name == "." || name == "/" {
			fmt.Fprintf(stderr, "Error: cannot derive a name from %q; pass --name explicitly\n", abs)
			return 1
		}
	}

	id, err := newVaultID()
	if err != nil {
		fmt.Fprintf(stderr, "Error: generate id: %v\n", err)
		return 1
	}
	rawPath := account.EncodeHomeRelative(abs)
	entry := account.VaultEntry{
		ID:      id,
		Name:    name,
		Path:    abs,
		RawPath: rawPath,
		Server:  opts.Server,
		Account: opts.Account,
		AddedAt: time.Now().UTC().Truncate(time.Second),
	}
	if _, err := account.UpsertVault(entry); err != nil {
		fmt.Fprintf(stderr, "Error: write vaults.yaml: %v\n", err)
		return 1
	}
	bind := "local"
	if opts.Server != "" {
		bind = opts.Server
		if opts.Account != "" {
			bind = opts.Account + "@" + opts.Server
		}
	}
	fmt.Fprintf(stdout, "Linked vault %q (%s) at %s\n", name, bind, abs)
	return 0
}

// runVaultUnlinkCmd implements `lumi vault unlink <id-or-path>`. Removes
// the matching row from vaults.yaml. Files on disk are untouched —
// this is purely a registry operation. UUID match wins over path match
// (so a path that happens to look like a UUID gets the id path).
func runVaultUnlinkCmd(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintf(stderr, "Error: unlink requires an id-or-path argument\n\n")
		fmt.Fprint(stderr, vaultUsage)
		return 64
	}
	for _, a := range args[1:] {
		// link's flags shouldn't bleed into unlink — be strict.
		if strings.HasPrefix(a, "-") {
			fmt.Fprintf(stderr, "Error: unknown flag %q for unlink\n", a)
			return 64
		}
	}
	if args[0] == "--help" || args[0] == "-h" {
		fmt.Fprint(stdout, vaultUsage)
		return 0
	}

	target := args[0]
	if len(args) > 1 {
		fmt.Fprintf(stderr, "Error: unexpected extra argument %q\n", args[1])
		return 64
	}

	// UUID-shaped target → id match. Anything else → path match (after
	// abs+clean so the user can pass `./notes/foo` and we still find
	// the row stored at /Users/u/notes/foo).
	if looksLikeUUID(target) {
		removed, remaining, err := account.RemoveVaultByID(target)
		if err != nil {
			fmt.Fprintf(stderr, "Error: %v\n", err)
			return 1
		}
		if !removed {
			fmt.Fprintf(stderr, "No vault with id %q found in vaults.yaml.\n", target)
			return 2
		}
		fmt.Fprintf(stdout, "Unlinked vault %s (%d remaining).\n", target, len(remaining))
		return 0
	}

	abs, err := filepath.Abs(target)
	if err != nil {
		fmt.Fprintf(stderr, "Error: resolve path %q: %v\n", target, err)
		return 1
	}
	removed, remaining, err := account.RemoveVaultByPath(abs)
	if err != nil {
		fmt.Fprintf(stderr, "Error: %v\n", err)
		return 1
	}
	if !removed {
		fmt.Fprintf(stderr, "No vault registered at %q.\n", abs)
		return 2
	}
	fmt.Fprintf(stdout, "Unlinked vault at %s (%d remaining).\n", abs, len(remaining))
	return 0
}

// looksLikeUUID is the CLI-side counterpart to account.isLikelyUUID
// (which is unexported). Inlined here so cmd_vault.go doesn't grow a
// dependency on internal account-package helpers.
func looksLikeUUID(s string) bool {
	if len(s) != 36 {
		return false
	}
	low := strings.ToLower(s)
	for i, r := range low {
		switch i {
		case 8, 13, 18, 23:
			if r != '-' {
				return false
			}
		default:
			if !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'f')) {
				return false
			}
		}
	}
	return true
}

// newVaultID generates a v4 UUID without pulling in a third-party
// package. Format matches what Apple's UUID().uuidString.lowercased()
// emits: 8-4-4-4-12 hex digits, lowercase.
func newVaultID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	// RFC 4122 §4.4: set version (4) and variant (10xx).
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	// %x on a byte slice emits 2 hex chars per byte, lowercase, no
	// separator — so each segment naturally lands at the right width.
	return fmt.Sprintf("%x-%x-%x-%x-%x",
		b[0:4], b[4:6], b[6:8], b[8:10], b[10:16]), nil
}
