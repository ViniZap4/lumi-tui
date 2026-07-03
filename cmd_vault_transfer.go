package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"golang.org/x/term"

	"github.com/vinizap/lumi/tui-client/account"
)

// transferOpts captures parsed flags + positional args for
// `lumi vault transfer`.
type transferOpts struct {
	Server   string
	Slug     string
	Username string
	Yes      bool
}

const transferUsage = `lumi vault transfer — hand vault ownership to another member

Usage:
  lumi vault transfer <server-url>/<slug> <username> [--yes]

Behaviour:
  * Resolves <username> against the vault's member list — ownership can
    only move to someone who is already a member (invite them first).
  * Owner-only: the server rejects the call unless your account owns
    the vault.
  * Transfer is NOT casually reversible: once it lands, only the NEW
    owner can transfer it back. You are asked to confirm interactively
    unless --yes is passed. Non-interactive use (scripts, pipes)
    requires --yes.

Flags:
  --yes             Skip the interactive y/N confirmation.
`

var errTransferHelp = errors.New("vault transfer: help requested")

// parseTransferArgs parses the CLI surface. Tested directly without I/O.
func parseTransferArgs(args []string) (transferOpts, error) {
	var opts transferOpts
	positional := []string{}
	for _, a := range args {
		switch a {
		case "--help", "-h":
			return transferOpts{}, errTransferHelp
		case "--yes", "-y":
			opts.Yes = true
		default:
			if strings.HasPrefix(a, "-") {
				return opts, fmt.Errorf("unknown flag %q", a)
			}
			positional = append(positional, a)
		}
	}
	if len(positional) < 2 {
		return opts, fmt.Errorf("expected `<server-url>/<slug> <username>`")
	}
	if len(positional) > 2 {
		return opts, fmt.Errorf("unexpected extra arguments: %v", positional[2:])
	}
	server, slug, err := splitServerAndSlug(positional[0])
	if err != nil {
		return opts, err
	}
	opts.Server = server
	opts.Slug = slug
	opts.Username = positional[1]
	return opts, nil
}

// stdinIsTerminal reports whether os.Stdin is an interactive terminal.
// Package-level var so tests can force the interactive path without a
// real TTY.
var stdinIsTerminal = func() bool {
	return term.IsTerminal(int(os.Stdin.Fd()))
}

// promptYesNo prints `prompt` and reads one line from `in`. Only an
// explicit "y" / "yes" (case-insensitive) counts as consent — anything
// else, including EOF or an empty line, is a no. Mirrors the
// conservative default of `rm -i`.
func promptYesNo(in io.Reader, out io.Writer, prompt string) (bool, error) {
	fmt.Fprint(out, prompt)
	line, err := bufio.NewReader(in).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return false, err
	}
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes":
		return true, nil
	default:
		return false, nil
	}
}

// resolveRemoteVault loads the signed-in account for `server`, builds
// an authenticated client, and resolves `slug` to a vault. On failure
// it writes a friendly message to stderr and returns a non-zero exit
// code; on success the exit code is 0 and client+vault are non-nil.
// Shared by `vault transfer` and `vault copy`.
func resolveRemoteVault(ctx context.Context, server, slug string, stderr io.Writer) (*account.Client, *account.RemoteVault, int) {
	file, err := account.Load()
	if err != nil {
		fmt.Fprintf(stderr, "Error reading accounts.yaml: %v\n", err)
		return nil, nil, 1
	}
	var acc *account.Account
	for i, a := range file.Accounts {
		if strings.EqualFold(a.Server, server) {
			acc = &file.Accounts[i]
			break
		}
	}
	if acc == nil {
		fmt.Fprintf(stderr,
			"Error: no signed-in account for %s. Run 'lumi login %s' first.\n",
			server, server)
		return nil, nil, 1
	}
	if acc.IsExpired() {
		fmt.Fprintf(stderr,
			"Error: account token for %s is expired. Run 'lumi login %s' to refresh.\n",
			server, server)
		return nil, nil, 1
	}

	client, err := account.NewClient(server)
	if err != nil {
		fmt.Fprintf(stderr, "Error: %v\n", err)
		return nil, nil, 1
	}
	client.SetToken(acc.Token)

	vaults, err := client.ListVaults(ctx)
	if err != nil {
		fmt.Fprintf(stderr, "Error: list vaults: %v\n", err)
		return nil, nil, 1
	}
	for i, v := range vaults {
		if v.Slug == slug {
			return client, &vaults[i], 0
		}
	}
	fmt.Fprintf(stderr, "Error: vault slug %q not found at %s (you have %d vault(s) visible).\n",
		slug, server, len(vaults))
	return nil, nil, 1
}

// runVaultTransferCmd implements `lumi vault transfer`.
func runVaultTransferCmd(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	opts, err := parseTransferArgs(args)
	if err != nil {
		if err == errTransferHelp {
			fmt.Fprint(stdout, transferUsage)
			return 0
		}
		fmt.Fprintf(stderr, "Error: %v\n\n", err)
		fmt.Fprint(stderr, transferUsage)
		return 64
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	client, vault, code := resolveRemoteVault(ctx, opts.Server, opts.Slug, stderr)
	if code != 0 {
		return code
	}

	members, err := client.ListMembers(ctx, vault.ID)
	if err != nil {
		fmt.Fprintf(stderr, "Error: list members: %v\n", err)
		return 1
	}

	// username → member row. The transfer endpoint wants a user_id, and
	// the server only accepts existing members — resolve and fail with a
	// clear message before touching the network again.
	var target *account.VaultMember
	names := make([]string, 0, len(members))
	for i, m := range members {
		names = append(names, m.Username)
		if strings.EqualFold(m.Username, opts.Username) {
			target = &members[i]
		}
	}
	if target == nil {
		fmt.Fprintf(stderr,
			"Error: %q is not a member of vault %q. Ownership can only be transferred to an existing member — invite them first.\n",
			opts.Username, opts.Slug)
		if len(names) > 0 {
			fmt.Fprintf(stderr, "Current members: %s\n", strings.Join(names, ", "))
		}
		return 1
	}

	// Resolve the current owner's username for the old→new confirmation
	// line. Falls back to the raw uuid if the owner somehow isn't in the
	// member list (shouldn't happen — owners hold an irremovable grant).
	oldOwner := vault.OwnerUserID
	for _, m := range members {
		if m.UserID == vault.OwnerUserID {
			oldOwner = m.Username
			break
		}
	}

	if target.UserID == vault.OwnerUserID {
		fmt.Fprintf(stdout, "%s already owns vault %q — nothing to do.\n", target.Username, opts.Slug)
		return 0
	}

	// Transfer is not casually reversible (only the new owner can hand
	// it back), so require explicit consent: --yes, or an interactive
	// y/N prompt when stdin is a real terminal. Piped/scripted stdin
	// without --yes is refused rather than silently consuming input.
	if !opts.Yes {
		if !stdinIsTerminal() {
			fmt.Fprintf(stderr,
				"Error: ownership transfer requires confirmation. Pass --yes, or run interactively for a y/N prompt.\n")
			return 1
		}
		ok, perr := promptYesNo(stdin, stdout, fmt.Sprintf(
			"Transfer ownership of %q (%s) from %s to %s? Only %s will be able to reverse this. [y/N] ",
			vault.Name, opts.Slug, oldOwner, target.Username, target.Username))
		if perr != nil {
			fmt.Fprintf(stderr, "Error: read confirmation: %v\n", perr)
			return 1
		}
		if !ok {
			fmt.Fprintln(stdout, "Aborted — ownership unchanged.")
			return 1
		}
	}

	updated, err := client.TransferOwnership(ctx, vault.ID, target.UserID)
	if err != nil {
		fmt.Fprintf(stderr, "Error: transfer ownership: %s\n", describeTransferError(err))
		return 1
	}

	newOwner := target.Username
	if updated.OwnerUserID != "" && updated.OwnerUserID != target.UserID {
		// Trust the server's answer over our expectation.
		newOwner = updated.OwnerUserID
	}
	fmt.Fprintf(stdout, "Ownership of vault %q (%s) transferred: %s -> %s.\n",
		updated.Name, updated.Slug, oldOwner, newOwner)
	return 0
}

// describeTransferError maps the transfer endpoint's failure modes to
// user-friendly text. Unknown errors pass through verbatim.
func describeTransferError(err error) string {
	if errors.Is(err, account.ErrUnauthorized) {
		return "session expired — run 'lumi login' again"
	}
	var apiErr *account.APIError
	if errors.As(err, &apiErr) {
		switch apiErr.Status {
		case 403:
			return "only the vault owner can transfer ownership"
		case 400:
			if apiErr.Detail != "" {
				return apiErr.Detail
			}
			return fmt.Sprintf("the server rejected the transfer (%s) — is the target still a member?", apiErr.Code)
		}
	}
	return err.Error()
}
