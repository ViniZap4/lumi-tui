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

const loginUsage = `lumi login — sign in to a lumi-server v2 instance

Usage:
  lumi login <server-url>

  Prompts for username and password, calls POST /api/auth/login on the
  given server, adds the account to ~/.config/lumi/accounts.yaml (shared
  with Lumi.app on this Mac, no secrets) and stores the TUI's session
  token in ~/.config/lumi/tokens.yaml (0600).

  If Lumi.app is running and has no session for this account yet, it gets
  one of its own through a device link (SPEC-LIVROS §8) — the password is
  typed once.

  Re-running 'lumi login' as the same user on the same server replaces
  that entry's token. Signing in as a different user on the same server
  adds a second account (accounts.yaml v2, keyed by server + username);
  pass --account <username> to server commands to pick one.
`

// runLoginCmd executes the 'login' subcommand. Returns the process exit
// code. stdin/stdout/stderr are passed in so tests can drive the flow.
//
// The "prompt" path: read username (echoed) from stdin, then read
// password (not echoed) from /dev/tty if stdin is a terminal — otherwise
// from stdin (lets a test or pipe drive both). The non-tty fallback is
// important: CI / scripts piping `username\npassword\n` should still work.
func runLoginCmd(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) < 1 {
		fmt.Fprint(stderr, "Error: lumi login requires a server URL.\n\n")
		fmt.Fprint(stderr, loginUsage)
		return 64 // EX_USAGE
	}
	if args[0] == "--help" || args[0] == "-h" {
		fmt.Fprint(stdout, loginUsage)
		return 0
	}
	serverURL := strings.TrimSpace(args[0])

	client, err := account.NewClient(serverURL)
	if err != nil {
		fmt.Fprintf(stderr, "Error: %v\n", err)
		return 1
	}

	reader := bufio.NewReader(stdin)

	fmt.Fprintf(stdout, "Sign in to %s\n", client.BaseURL().String())
	fmt.Fprint(stdout, "Username: ")
	username, err := reader.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		fmt.Fprintf(stderr, "Error reading username: %v\n", err)
		return 1
	}
	username = strings.TrimSpace(username)
	if username == "" {
		fmt.Fprint(stderr, "Error: username is required.\n")
		return 1
	}

	password, err := readPassword(stdout, stderr, reader)
	if err != nil {
		fmt.Fprintf(stderr, "Error reading password: %v\n", err)
		return 1
	}
	if password == "" {
		fmt.Fprint(stderr, "Error: password is required.\n")
		return 1
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	resp, err := client.Login(ctx, username, password)
	if err != nil {
		fmt.Fprintf(stderr, "Login failed: %s\n", describeLoginError(err))
		return 1
	}

	file, err := account.Load()
	if err != nil {
		fmt.Fprintf(stderr, "Error reading accounts file: %v\n", err)
		return 1
	}
	file.Upsert(account.Account{
		Server:      client.BaseURL().String(),
		Username:    resp.User.Username,
		UserID:      resp.User.ID,
		DisplayName: resp.User.DisplayName,
		Token:       resp.Token,
		ExpiresAt:   resp.ExpiresAt,
		AddedAt:     time.Now().UTC(),
	})
	file.SetLinkOptOut(client.BaseURL().String(), resp.User.Username, false)
	if err := file.Save(); err != nil {
		fmt.Fprintf(stderr, "Error writing accounts file: %v\n", err)
		return 1
	}

	displayName := resp.User.DisplayName
	if displayName == "" {
		displayName = resp.User.Username
	}
	path, _ := account.Path()
	fmt.Fprintf(stdout, "Signed in as %s (%s).\nAccount saved to %s (token in tokens.yaml).\n", resp.User.Username, displayName, path)
	if n := serveAppLinks(ctx, linkWaitAfterLogin); n > 0 {
		fmt.Fprintln(stdout, "Lumi.app is signed in too.")
	}
	return 0
}

// linkWaitAfterLogin is how long `lumi login` waits for a running
// Lumi.app to notice the new account and ask for a device link.
var linkWaitAfterLogin = 3 * time.Second

// serveAppLinks answers Lumi.app's pending device-link requests with the
// TUI's sessions (SPEC-LIVROS §8.4). When the app is running it keeps
// looking for up to `wait` for a request to show up. Returns how many
// requests got a code.
func serveAppLinks(ctx context.Context, wait time.Duration) int {
	ch, err := account.NewLinkChannel()
	if err != nil {
		return 0
	}
	deadline := time.Now().Add(wait)
	served := 0
	for {
		if len(ch.Pending()) > 0 {
			if f, err := account.Load(); err == nil {
				served += account.ServeLinkRequests(ctx, ch, f, nil)
			}
		}
		if served > 0 || time.Now().After(deadline) || !ch.PeerAlive(account.LinkClientApple) {
			return served
		}
		select {
		case <-ctx.Done():
			return served
		case <-time.After(200 * time.Millisecond):
		}
	}
}

// readPassword reads a password from /dev/tty without echo when stdin is
// a terminal; otherwise reads a line from `reader` so piped input works
// (CI, tests). The piped path always echoes — that's fine because in a
// non-tty context there's no real terminal to redact on.
func readPassword(stdout, stderr io.Writer, fallback *bufio.Reader) (string, error) {
	fmt.Fprint(stdout, "Password: ")
	if term.IsTerminal(int(os.Stdin.Fd())) {
		// Read from the controlling terminal directly so we can suppress
		// echo even if stdin has been redirected to /dev/null elsewhere.
		// On a normal interactive run, /dev/tty == stdin.
		fd := int(os.Stdin.Fd())
		raw, err := term.ReadPassword(fd)
		fmt.Fprintln(stdout)
		if err != nil {
			return "", err
		}
		return strings.TrimSpace(string(raw)), nil
	}
	// Non-terminal stdin: read a line. No echo concern here — there is
	// no human at this end.
	line, err := fallback.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", err
	}
	fmt.Fprintln(stdout)
	return strings.TrimSpace(line), nil
}

// describeLoginError turns the typed account.* errors into user-friendly
// messages. Keeps server-coded errors intact so operators can grep them.
func describeLoginError(err error) string {
	if errors.Is(err, account.ErrUnauthorized) {
		return "invalid username or password"
	}
	if wait, ok := account.Locked(err); ok {
		// pt-BR like every client (the server only says this when the
		// password was right).
		return account.LockedMessage(wait)
	}
	var apiErr *account.APIError
	if errors.As(err, &apiErr) {
		switch apiErr.Code {
		case "invalid_credentials":
			return "invalid username or password"
		case "validation_failed":
			if apiErr.Detail != "" {
				return apiErr.Detail
			}
			return "validation failed"
		case "rate_limited":
			return "too many sign-in attempts — try again later"
		default:
			if apiErr.Detail != "" {
				return fmt.Sprintf("%s (%s)", apiErr.Detail, apiErr.Code)
			}
			return apiErr.Code
		}
	}
	return err.Error()
}
