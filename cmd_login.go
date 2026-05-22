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
  given server, and stores the returned session token in
  ~/.config/lumi/accounts.yaml at 0600 permissions.

  Re-running 'lumi login' for a server you're already signed in to
  overwrites the existing entry with the new token.
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
	if err := file.Save(); err != nil {
		fmt.Fprintf(stderr, "Error writing accounts file: %v\n", err)
		return 1
	}

	displayName := resp.User.DisplayName
	if displayName == "" {
		displayName = resp.User.Username
	}
	path, _ := account.Path()
	fmt.Fprintf(stdout, "Signed in as %s (%s).\nToken saved to %s.\n", resp.User.Username, displayName, path)
	return 0
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
