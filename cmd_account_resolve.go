package main

import (
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/vinizap/lumi/tui-client/account"
)

// splitAccountFlag removes `--account <username>` (or `--account=<u>`)
// from args and returns the remaining args plus the username. Shared by
// every server-facing subcommand now that accounts.yaml v2 allows more
// than one account per server (SPEC-LIVROS §2.2).
func splitAccountFlag(args []string) ([]string, string, error) {
	out := make([]string, 0, len(args))
	username := ""
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--account":
			if i+1 >= len(args) {
				return nil, "", fmt.Errorf("--account requires a value")
			}
			username = args[i+1]
			i++
		case strings.HasPrefix(a, "--account="):
			username = strings.TrimPrefix(a, "--account=")
		default:
			out = append(out, a)
		}
	}
	return out, username, nil
}

// lookupAccount loads accounts.yaml and resolves the account for
// (server, username). On failure it writes a user-facing message to
// stderr and returns a non-zero exit code.
func lookupAccount(server, username string, stderr io.Writer) (*account.Account, int) {
	file, err := account.Load()
	if err != nil {
		fmt.Fprintf(stderr, "Error reading accounts.yaml: %v\n", err)
		return nil, 1
	}
	acc, err := file.Resolve(server, username)
	switch {
	case errors.Is(err, account.ErrAmbiguousAccount):
		fmt.Fprintf(stderr, "Error: %s has more than one signed-in account; pass --account <username>.\n", server)
		return nil, 1
	case err != nil:
		who := server
		if username != "" {
			who = username + "@" + server
		}
		fmt.Fprintf(stderr,
			"Error: no signed-in account for %s. Run 'lumi login %s' first.\n",
			who, server)
		return nil, 1
	}
	return acc, 0
}
