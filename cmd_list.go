package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"sort"
	"time"

	"github.com/vinizap/lumi/tui-client/account"
)

const accountsUsage = `lumi accounts — list signed-in servers

Usage:
  lumi accounts

  Lists every server you've signed in to (one row per server URL).
  Token validity is checked lazily on use; this command does NOT call
  the server. Use 'lumi accounts --verify' to actively probe each
  server's /api/users/me endpoint.
`

const vaultsUsage = `lumi vaults — list known vaults from ~/.config/lumi/vaults.yaml

Usage:
  lumi vaults

  Lists every vault recorded in the shared registry written by
  lumi-apple (or by a future 'lumi vault link' subcommand). This is the
  cross-client list of vault locations — local-only vaults are shown
  alongside server-bound ones.
`

// runAccountsCmd implements `lumi accounts`. Lists every signed-in server.
// With --verify, each row also probes /api/users/me to confirm the token.
func runAccountsCmd(args []string, stdout, stderr io.Writer) int {
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
		status := "saved"
		if a.IsExpired() {
			status = "expired"
			exitCode = 2
		}
		if verify {
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

// runVaultsCmd implements `lumi vaults`. Reads ~/.config/lumi/vaults.yaml
// and prints each known vault.
func runVaultsCmd(args []string, stdout, stderr io.Writer) int {
	for _, a := range args {
		switch a {
		case "--help", "-h":
			fmt.Fprint(stdout, vaultsUsage)
			return 0
		default:
			fmt.Fprintf(stderr, "Error: unknown flag %q\n\n", a)
			fmt.Fprint(stderr, vaultsUsage)
			return 64
		}
	}

	entries, err := account.LoadVaults()
	if err != nil {
		fmt.Fprintf(stderr, "Error reading vaults file: %v\n", err)
		return 1
	}
	if len(entries) == 0 {
		path, _ := account.VaultsPath()
		fmt.Fprintf(stdout, "No vaults recorded. The shared vaults.yaml is empty or missing.\n(file: %s)\n", path)
		fmt.Fprintf(stdout, "lumi-apple writes to this file; the TUI gains an upserter in a later slice.\n")
		return 0
	}
	// Stable + already sorted newest-first by the loader; secondary sort by
	// name for ties so the output is deterministic across runs.
	sort.SliceStable(entries, func(i, j int) bool {
		// Preserve loader's lastOpened/added ordering; only break ties by name.
		ki := entries[i].LastOpenedAt
		if ki.IsZero() {
			ki = entries[i].AddedAt
		}
		kj := entries[j].LastOpenedAt
		if kj.IsZero() {
			kj = entries[j].AddedAt
		}
		if !ki.Equal(kj) {
			return ki.After(kj)
		}
		return entries[i].Name < entries[j].Name
	})

	for _, v := range entries {
		bind := "local"
		if v.IsServerBound() {
			bind = v.Server
			if v.Account != "" {
				bind = v.Account + "@" + redactURLPath(v.Server)
			}
		}
		fmt.Fprintf(stdout, "%-30s  %s\n", v.Name, bind)
		fmt.Fprintf(stdout, "  path: %s\n", v.Path)
		if !v.LastOpenedAt.IsZero() {
			fmt.Fprintf(stdout, "  last opened: %s\n", v.LastOpenedAt.Format(time.RFC3339))
		} else {
			fmt.Fprintf(stdout, "  added: %s (never opened)\n", v.AddedAt.Format(time.RFC3339))
		}
	}
	return 0
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
