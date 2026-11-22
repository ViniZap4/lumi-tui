package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/vinizap/lumi/tui-client/account"
	"github.com/vinizap/lumi/tui-client/config"
)

// importFolderToken migrates a legacy `<root>/.lumi/config.yaml`
// (`server_url` + `server_token`) into accounts.yaml and removes the token
// from the notes folder (SPEC-LIVROS §2.5): notes folders travel through
// iCloud, Syncthing and Git, so a token there is a leak waiting to happen.
//
//   - 2xx from GET /api/users/me: the account is upserted (keyed by the
//     returned username) and the folder token is removed.
//   - 401: the token is dead; it is removed from the folder anyway.
//   - network or other errors: nothing changes; we retry next launch.
func importFolderToken(root string, stderr io.Writer) {
	cfg := config.LoadFolderConfig(root)
	if cfg == nil || cfg.ServerToken == "" || cfg.ServerURL == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := migrateFolderToken(ctx, root, cfg, account.Load, func(f *account.File) error { return f.Save() }); err != nil {
		fmt.Fprintf(stderr, "Note: could not move the server token out of %s/.lumi/config.yaml yet: %v\n", root, err)
	}
}

func migrateFolderToken(ctx context.Context, root string, cfg *config.FolderConfig,
	load func() (*account.File, error), save func(*account.File) error) error {
	client, err := account.NewClient(cfg.ServerURL)
	if err != nil {
		return err
	}
	client.SetToken(cfg.ServerToken)
	user, err := client.CurrentUser(ctx)
	switch {
	case errors.Is(err, account.ErrUnauthorized):
		// Dead token: just scrub it.
	case err != nil:
		return err
	default:
		f, err := load()
		if err != nil {
			return err
		}
		if f.Find(cfg.ServerURL, user.Username) == nil {
			f.Upsert(account.Account{Server: cfg.ServerURL, Username: user.Username, UserID: user.ID,
				DisplayName: user.DisplayName, Token: cfg.ServerToken})
			if err := save(f); err != nil {
				return err
			}
		}
	}
	scrubbed := *cfg
	scrubbed.ServerToken = ""
	return config.SaveFolderConfig(root, &scrubbed)
}
