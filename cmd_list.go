package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"time"

	"github.com/vinizap/lumi/tui-client/account"
)

const accountsUsage = `lumi accounts — signed-in servers (shared with Lumi.app)

Usage:
  lumi accounts [--verify]
  lumi accounts link [<server-url>] [--account <username>]
  lumi accounts signout <server-url> [--account <username>] [--mac]
  lumi accounts remove <server-url> [--account <username>]

  Lists every account on this machine (server + username; a server may
  have several). The list in ~/.config/lumi/accounts.yaml is shared with
  Lumi.app; each client keeps its own session. Token validity is checked
  lazily on use; --verify probes each server's /api/users/me.

  link      sign this terminal in through a running Lumi.app (device link,
            approved in the app); without a server, every account that
            needs it
  signout   end this terminal's session only; with --mac, end the
            sessions of the terminal and Lumi.app on this Mac
  remove    sign out on this Mac and remove the account from the list
`

// runAccountsCmd implements `lumi accounts`. Lists every signed-in server.
// With --verify, each row also probes /api/users/me to confirm the token.
func runAccountsCmd(args []string, stdout, stderr io.Writer) int {
	if len(args) > 0 {
		switch args[0] {
		case "link":
			return runAccountsLink(args[1:], stdout, stderr)
		case "signout", "logout":
			return runAccountsSignOut(args[1:], false, stdout, stderr)
		case "remove", "rm":
			return runAccountsSignOut(args[1:], true, stdout, stderr)
		}
	}
	verify := false
	for _, a := range args {
		switch a {
		case "--help", "-h":
			fmt.Fprint(stdout, accountsUsage)
			return 0
		case "--verify":
			verify = true
		default:
			fmt.Fprintf(stderr, "Error: unknown flag %q\n\n", a)
			fmt.Fprint(stderr, accountsUsage)
			return 64
		}
	}

	file, err := account.Load()
	if err != nil {
		fmt.Fprintf(stderr, "Error reading accounts file: %v\n", err)
		return 1
	}
	if len(file.Accounts) == 0 {
		path, _ := account.Path()
		fmt.Fprintf(stdout, "No accounts. Sign in with: lumi login <server-url>\n(file: %s)\n", path)
		return 0
	}
	exitCode := 0
	for _, a := range file.Accounts {
		status := "signed in"
		if a.Token == "" {
			status = "needs sign-in (lumi login, or lumi accounts link with Lumi.app open)"
			exitCode = 2
		} else if a.IsExpired() {
			status = "expired"
			exitCode = 2
		}
		if verify && a.Token != "" {
			active, msg := verifyAccount(a)
			if active {
				status = "active"
			} else {
				status = "dead — " + msg
				exitCode = 2
			}
		}
		display := a.DisplayName
		if display == "" {
			display = a.Username
		}
		fmt.Fprintf(stdout, "%-40s  %-20s  %s\n",
			truncateMiddle(a.Server, 40),
			a.Username,
			status,
		)
		if display != a.Username {
			fmt.Fprintf(stdout, "  display name: %s\n", display)
		}
		if !a.ExpiresAt.IsZero() {
			fmt.Fprintf(stdout, "  token expires: %s\n", a.ExpiresAt.Format(time.RFC3339))
		}
	}
	return exitCode
}

// verifyAccount calls /api/users/me to confirm a token is still live.
// Returns (active=true, "") on 200, otherwise (false, reason).
func verifyAccount(a account.Account) (bool, string) {
	client, err := account.NewClient(a.Server)
	if err != nil {
		return false, err.Error()
	}
	client.SetToken(a.Token)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := client.CurrentUser(ctx); err != nil {
		if errors.Is(err, account.ErrUnauthorized) {
			return false, "401 (token invalid or expired)"
		}
		return false, err.Error()
	}
	return true, ""
}

// truncateMiddle compresses long strings with `…` for column alignment.
func truncateMiddle(s string, max int) string {
	if len(s) <= max {
		return s
	}
	if max < 4 {
		return s[:max]
	}
	keep := (max - 1) / 2
	return s[:keep] + "…" + s[len(s)-(max-1-keep):]
}

// redactURLPath strips the path portion of a URL for compact display.
// `https://lumi.work.com/foo/bar` -> `https://lumi.work.com`. Malformed
// URLs pass through unchanged.
func redactURLPath(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return raw
	}
	return u.Scheme + "://" + u.Host
}

// runAccountsLink asks Lumi.app for a device link for one account, or for
// every account this terminal has no session for.
func runAccountsLink(args []string, stdout, stderr io.Writer) int {
	rest, username, err := splitAccountFlag(args)
	if err != nil {
		fmt.Fprintf(stderr, "Error: %v\n", err)
		return 64
	}
	file, err := account.Load()
	if err != nil {
		fmt.Fprintf(stderr, "Error reading accounts file: %v\n", err)
		return 1
	}
	var targets []account.Account
	if len(rest) > 0 {
		acc, err := file.Resolve(rest[0], username)
		if err != nil {
			fmt.Fprintf(stderr, "Error: %v (see 'lumi accounts')\n", err)
			return 1
		}
		if acc.Token != "" {
			fmt.Fprintf(stdout, "Already signed in as %s on %s.\n", acc.Username, acc.Server)
			return 0
		}
		targets = []account.Account{*acc}
	} else {
		for _, a := range file.Accounts {
			if a.Token == "" {
				targets = append(targets, a)
			}
		}
		if len(targets) == 0 {
			fmt.Fprintln(stdout, "Every account is signed in on this terminal.")
			return 0
		}
	}
	code := 0
	for _, a := range targets {
		// Explicit request: ignore (and clear on success) the opt-out.
		got, c := linkFromApp(a, false, stderr)
		if c != 0 {
			code = c
			continue
		}
		fmt.Fprintf(stdout, "Signed in as %s on %s through Lumi.app.\n", got.Username, got.Server)
	}
	return code
}

// runAccountsSignOut ends this terminal's session (default), the
// sessions of the whole Mac (--mac), or removes the account (remove=true,
// which implies --mac). SPEC-LIVROS §8.5.
func runAccountsSignOut(args []string, remove bool, stdout, stderr io.Writer) int {
	mac := remove
	var clean []string
	for _, a := range args {
		if a == "--mac" {
			mac = true
			continue
		}
		clean = append(clean, a)
	}
	rest, username, err := splitAccountFlag(clean)
	if err != nil || len(rest) != 1 {
		fmt.Fprint(stderr, accountsUsage)
		return 64
	}
	file, err := account.Load()
	if err != nil {
		fmt.Fprintf(stderr, "Error reading accounts file: %v\n", err)
		return 1
	}
	acc, err := file.Resolve(rest[0], username)
	if err != nil {
		fmt.Fprintf(stderr, "Error: %v (see 'lumi accounts')\n", err)
		return 1
	}
	a := *acc
	if a.Token != "" {
		if c, err := account.NewClient(a.Server); err == nil {
			c.SetToken(a.Token)
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			if mac {
				err = c.LogoutDevice(ctx)
			} else {
				err = c.Logout(ctx)
			}
			cancel()
			if err != nil && !errors.Is(err, account.ErrUnauthorized) {
				fmt.Fprintf(stderr, "Warning: could not reach %s to end the session (%v); it was removed locally.\n", a.Server, err)
			}
		}
	}
	switch {
	case remove:
		file.SignOutMac(a.Server, a.Username, time.Now())
		file.Remove(a.Server, a.Username)
	case mac:
		file.SignOutMac(a.Server, a.Username, time.Now())
	default:
		acc.Token = ""
		file.SetLinkOptOut(a.Server, a.Username, true)
	}
	if err := file.Save(); err != nil {
		fmt.Fprintf(stderr, "Error writing accounts file: %v\n", err)
		return 1
	}
	switch {
	case remove:
		fmt.Fprintf(stdout, "Removed %s on %s from this Mac (terminal and Lumi.app).\n", a.Username, a.Server)
	case mac:
		fmt.Fprintf(stdout, "Signed out of %s on %s on this Mac (terminal and Lumi.app).\n", a.Username, a.Server)
	default:
		fmt.Fprintf(stdout, "Signed this terminal out of %s on %s. Lumi.app stays signed in.\n", a.Username, a.Server)
	}
	return 0
}
