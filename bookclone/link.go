package bookclone

import (
	"fmt"
	"time"

	"github.com/vinizap/lumi/tui-client/account"
	"github.com/vinizap/lumi/tui-client/books"
)

// LinkFolder binds an existing bound book (one with no folder on this
// device) to the folder abs — the in-app form of
// `lumi books link <path> --server <url> --vault <id>`: the book row gets
// the path and vaults.yaml gets the same row `lumi vault clone` writes,
// for older clients. The feed then mirrors the server's notes into the
// folder (files already there are kept; conflicts keep both versions).
func LinkFolder(update func(fn func(reg *books.Registry) bool) error, b books.Book, abs string) error {
	if !b.IsBound() || b.VaultID == "" {
		return fmt.Errorf("o livro %q não está ligado a um servidor", b.Name)
	}
	var inner error
	err := update(func(reg *books.Registry) bool {
		cur := reg.Get(b.ID)
		if cur == nil {
			inner = fmt.Errorf("livro %q não está mais na lista", b.Name)
			return false
		}
		if other := reg.ByPath(abs); other != nil && other.ID != cur.ID {
			inner = fmt.Errorf("a pasta já é o livro %q", other.Name)
			return false
		}
		cur.Path = books.EncodeHome(abs)
		return true
	})
	if inner != nil {
		return inner
	}
	if err != nil {
		return err
	}
	_, err = account.UpsertVault(account.VaultEntry{
		ID: b.VaultID, Name: b.Name, Path: abs, RawPath: account.EncodeHomeRelative(abs),
		Server: b.ServerURL, Account: b.Account, AddedAt: time.Now().UTC().Truncate(time.Second),
	})
	return err
}
