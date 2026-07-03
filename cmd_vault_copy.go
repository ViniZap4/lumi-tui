package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/vinizap/lumi/tui-client/account"
)

// copyOpts captures parsed positional args for `lumi vault copy`.
type copyOpts struct {
	Server    string
	Slug      string
	Recipient string
}

const copyUsage = `lumi vault copy — share an independent copy of a vault with another user

Usage:
  lumi vault copy <server-url>/<slug> <recipient-username>

Behaviour:
  * The server forks the vault's current state into a brand-new vault
    owned by <recipient-username>. New id, new slug, fresh history.
  * The copy shares NOTHING after creation: no membership, no live
    link — edits diverge permanently. Provenance is recorded on the
    fork (copied_from) and in the audit log.
  * Requires the 'vault.export' capability on the source vault.
  * The recipient must already have an account on the server.
`

var errCopyHelp = errors.New("vault copy: help requested")

// parseCopyArgs parses the CLI surface. Tested directly without I/O.
func parseCopyArgs(args []string) (copyOpts, error) {
	var opts copyOpts
	positional := []string{}
	for _, a := range args {
		switch a {
		case "--help", "-h":
			return copyOpts{}, errCopyHelp
		default:
			if strings.HasPrefix(a, "-") {
				return opts, fmt.Errorf("unknown flag %q", a)
			}
			positional = append(positional, a)
		}
	}
	if len(positional) < 2 {
		return opts, fmt.Errorf("expected `<server-url>/<slug> <recipient-username>`")
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
	opts.Recipient = positional[1]
	return opts, nil
}

// runVaultCopyCmd implements `lumi vault copy`.
func runVaultCopyCmd(args []string, stdout, stderr io.Writer) int {
	opts, err := parseCopyArgs(args)
	if err != nil {
		if err == errCopyHelp {
			fmt.Fprint(stdout, copyUsage)
			return 0
		}
		fmt.Fprintf(stderr, "Error: %v\n\n", err)
		fmt.Fprint(stderr, copyUsage)
		return 64
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	client, vault, code := resolveRemoteVault(ctx, opts.Server, opts.Slug, stderr)
	if code != 0 {
		return code
	}

	fork, err := client.CopyVault(ctx, vault.ID, opts.Recipient)
	if err != nil {
		fmt.Fprintf(stderr, "Error: copy vault: %s\n", describeCopyError(err, opts))
		return 1
	}

	fmt.Fprintf(stdout, "Shared a copy of %q: new vault %q (slug %s), owned by %s.\n",
		vault.Name, fork.Name, fork.Slug, opts.Recipient)
	fmt.Fprintln(stdout, "The copy is independent — edits will not sync back to the original.")
	return 0
}

// describeCopyError maps the copy endpoint's failure modes to
// user-friendly text. Unknown errors pass through verbatim.
func describeCopyError(err error, opts copyOpts) string {
	if errors.Is(err, account.ErrUnauthorized) {
		return "session expired — run 'lumi login' again"
	}
	var apiErr *account.APIError
	if errors.As(err, &apiErr) {
		switch {
		case apiErr.Code == "recipient_not_found":
			return fmt.Sprintf("no user named %q on %s — the recipient needs an account there first",
				opts.Recipient, opts.Server)
		case apiErr.Status == 403:
			return "you need the 'vault.export' capability on this vault to share a copy"
		case apiErr.Status == 400 && apiErr.Detail != "":
			return apiErr.Detail
		}
	}
	return err.Error()
}
