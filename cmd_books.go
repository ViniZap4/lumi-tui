package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/vinizap/lumi/tui-client/account"
	"github.com/vinizap/lumi/tui-client/books"
)

const booksUsage = `lumi books — manage books (livros): local folders and server vaults

A book is a separate space of notes. It lives only on this device
(local) or is bound to exactly one (server, account, vault). The registry
is ~/.config/lumi/books.yaml, shared with lumi-apple. On first use it is
imported once from vaults.yaml, which is left untouched for older clients.

Usage:
  lumi books [list]                 list books in picker order (g1..g9)
  lumi books refresh                fetch every account's vaults; new ones
                                    become books, federated copies become
                                    alternates of a single book
  lumi books link <path> [--name <name>]
                                    register a folder as a local book
  lumi books link <path|book> --server <url> [--account <user>] [--vault <slug|id>]
                                    bind a book (or folder) to a server vault;
                                    also repairs an "incomplete binding"
  lumi books unlink <book>          bound+folder -> local book (server
                                    untouched); otherwise remove from list.
                                    Files are never deleted.
  lumi books rename <book> <name>   local: renames here; bound: renames the
                                    vault for every member (vault.manage)
  lumi books use <book>             make <book> the active book (alias: switch)

<book> is a book id, its 1-based position, its folder path or its name.
'lumi vaults' is an alias of 'lumi books list'.
`

// openBooks is swapped by tests.
var openBooks = books.Open

func runBooksCmd(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		return runBooksList(nil, stdout, stderr)
	}
	switch args[0] {
	case "--help", "-h", "help":
		fmt.Fprint(stdout, booksUsage)
		return 0
	case "list", "ls":
		return runBooksList(args[1:], stdout, stderr)
	case "refresh":
		return runBooksRefresh(args[1:], stdout, stderr)
	case "link":
		return runBooksLink(args[1:], stdout, stderr)
	case "unlink":
		return runBooksUnlink(args[1:], stdout, stderr)
	case "rename":
		return runBooksRename(args[1:], stdout, stderr)
	case "use", "switch":
		return runBooksUse(args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "Error: unknown books subcommand %q\n\n", args[0])
		fmt.Fprint(stderr, booksUsage)
		return 64
	}
}

func loadBooks(stdout, stderr io.Writer) (*books.Store, int) {
	st, err := openBooks()
	if err != nil {
		fmt.Fprintf(stderr, "Error reading books.yaml: %v\n", err)
		return nil, 1
	}
	if st.Migrated {
		fmt.Fprintf(stdout, "Imported %d book(s) from vaults.yaml into %s (vaults.yaml left as is).\n",
			len(st.Registry.Books), st.Path)
	}
	if st.Registry.ReadOnly {
		fmt.Fprintf(stderr, "Warning: %s is read-only for this lumi version: %s\n",
			st.Path, strings.Join(st.Registry.Problems, "; "))
	}
	return st, 0
}

func saveBooks(st *books.Store, stderr io.Writer) int {
	if err := st.Save(); err != nil {
		fmt.Fprintf(stderr, "Error writing books.yaml: %v\n", err)
		return 1
	}
	return 0
}

// bookBinding is the one-line "where does it sync" label.
func bookBinding(b books.Book) string {
	switch {
	case !b.IsBound():
		return "local"
	case b.IncompleteBinding():
		return b.Account + "@" + books.Host(b.ServerURL) + "  (erro: ligação incompleta — lumi books link --server)"
	default:
		return b.Account + "@" + books.Host(b.ServerURL)
	}
}

func runBooksList(args []string, stdout, stderr io.Writer) int {
	for _, a := range args {
		if a == "--help" || a == "-h" {
			fmt.Fprint(stdout, booksUsage)
			return 0
		}
		fmt.Fprintf(stderr, "Error: unknown flag %q\n", a)
		return 64
	}
	st, code := loadBooks(stdout, stderr)
	if code != 0 {
		return code
	}
	reg := st.Registry
	if len(reg.Books) == 0 {
		fmt.Fprintln(stdout, "Nenhum livro ainda. Add one with 'lumi books link <path>' or 'lumi login <server>' + 'lumi books refresh'.")
		return 0
	}
	active := reg.ActiveBook()
	for i, b := range reg.Books {
		mark := " "
		if active != nil && active.ID == b.ID {
			mark = "*"
		}
		fmt.Fprintf(stdout, "%s %d  %-28s  %s\n", mark, i+1, truncateMiddle(b.Name, 28), bookBinding(b))
		if b.Path != "" {
			fmt.Fprintf(stdout, "      path: %s\n", b.Path)
		} else if b.IsBound() {
			fmt.Fprintln(stdout, "      path: (none — clone with 'lumi vault clone')")
		}
	}
	return 0
}

func runBooksRefresh(args []string, stdout, stderr io.Writer) int {
	if len(args) > 0 {
		if args[0] == "--help" || args[0] == "-h" {
			fmt.Fprint(stdout, booksUsage)
			return 0
		}
		fmt.Fprintf(stderr, "Error: unexpected argument %q\n", args[0])
		return 64
	}
	st, code := loadBooks(stdout, stderr)
	if code != 0 {
		return code
	}
	file, err := account.Load()
	if err != nil {
		fmt.Fprintf(stderr, "Error reading accounts.yaml: %v\n", err)
		return 1
	}
	if len(file.Accounts) == 0 {
		fmt.Fprintln(stdout, "No accounts. Sign in with: lumi login <server-url>")
		return 0
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	listings, errs := books.FetchListings(ctx, file.Accounts, nil)
	for _, e := range errs {
		fmt.Fprintf(stderr, "Warning: %v\n", e)
	}
	res := books.Reconcile(st.Registry, listings, time.Now(), mustNewID)
	for srv, n := range res.AddedByServer {
		fmt.Fprintf(stdout, "%d livro(s) adicionado(s) de %s\n", n, books.Host(srv))
	}
	for _, bn := range res.Banners {
		fmt.Fprintf(stdout, "%s: %s Use 'lumi books link %q --server <url>' to switch.\n", bn.BookName, bn.Text(), bn.BookName)
	}
	for _, id := range res.Missing {
		if b := st.Registry.Get(id); b != nil {
			fmt.Fprintf(stdout, "Você não tem mais acesso a %q (%s). Remove it with 'lumi books unlink %s'.\n", b.Name, books.Host(b.ServerURL), b.ID)
		}
	}
	if res.Changed() {
		if code := saveBooks(st, stderr); code != 0 {
			return code
		}
	}
	if !res.Changed() && len(res.Missing) == 0 {
		fmt.Fprintln(stdout, "Books are up to date with your accounts.")
	}
	if len(errs) > 0 {
		return 2
	}
	return 0
}

func mustNewID() string {
	id, err := newVaultID()
	if err != nil {
		panic(err)
	}
	return id
}

type booksLinkOpts struct {
	Target, Name, Server, Account, Vault string
}

func parseBooksLinkArgs(args []string) (booksLinkOpts, error) {
	var o booksLinkOpts
	var pos []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		val := func() (string, error) {
			if i+1 >= len(args) {
				return "", fmt.Errorf("%s requires a value", a)
			}
			i++
			return args[i], nil
		}
		var err error
		switch a {
		case "--name":
			o.Name, err = val()
		case "--server":
			o.Server, err = val()
		case "--account":
			o.Account, err = val()
		case "--vault":
			o.Vault, err = val()
		default:
			if strings.HasPrefix(a, "-") {
				return o, fmt.Errorf("unknown flag %q", a)
			}
			pos = append(pos, a)
		}
		if err != nil {
			return o, err
		}
	}
	if len(pos) != 1 {
		return o, errors.New("link takes exactly one <path|book>")
	}
	o.Target = pos[0]
	if (o.Account != "" || o.Vault != "") && o.Server == "" {
		return o, errors.New("--account/--vault need --server")
	}
	return o, nil
}

func runBooksLink(args []string, stdout, stderr io.Writer) int {
	if len(args) > 0 && (args[0] == "--help" || args[0] == "-h") {
		fmt.Fprint(stdout, booksUsage)
		return 0
	}
	o, err := parseBooksLinkArgs(args)
	if err != nil {
		fmt.Fprintf(stderr, "Error: %v\n\n%s", err, booksUsage)
		return 64
	}
	st, code := loadBooks(stdout, stderr)
	if code != 0 {
		return code
	}
	reg := st.Registry

	// Resolve the target: an existing directory, or an existing book.
	var book *books.Book
	abs := ""
	if info, err := os.Stat(books.ExpandHome(o.Target)); err == nil && info.IsDir() {
		abs, _ = filepath.Abs(books.ExpandHome(o.Target))
		book = reg.ByPath(abs)
	} else if book = reg.Find(o.Target); book == nil {
		fmt.Fprintf(stderr, "Error: %q is neither a directory nor a known book\n", o.Target)
		return 1
	}

	if o.Server == "" {
		if book != nil {
			fmt.Fprintf(stderr, "%q is already registered as book %q.\n", abs, book.Name)
			return 2
		}
		name := o.Name
		if name == "" {
			name = filepath.Base(abs)
		}
		b, err := reg.Add(books.Book{ID: mustNewID(), Name: name, Kind: books.KindLocal, Path: books.EncodeHome(abs)})
		if err != nil {
			fmt.Fprintf(stderr, "Error: %v\n", err)
			return 1
		}
		if code := saveBooks(st, stderr); code != 0 {
			return code
		}
		fmt.Fprintf(stdout, "Linked local book %q at %s\n", b.Name, abs)
		return 0
	}

	// Binding to a server vault.
	server, err := account.NormalizeServerURL(o.Server)
	if err != nil {
		fmt.Fprintf(stderr, "Error: %v\n", err)
		return 64
	}
	if !account.IsInsecureAllowed(server) {
		fmt.Fprintf(stderr, "Error: plain http is only allowed for localhost, private IPs and *.local (%s)\n", server)
		return 1
	}
	acc, code := lookupAccount(server, o.Account, stderr)
	if code != 0 {
		return code
	}
	client, err := account.NewClient(server)
	if err != nil {
		fmt.Fprintf(stderr, "Error: %v\n", err)
		return 1
	}
	client.SetToken(acc.Token)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	vaults, err := client.ListVaults(ctx)
	if err != nil {
		fmt.Fprintf(stderr, "Error: list vaults: %v\n", err)
		return 1
	}
	want := o.Vault
	var hint []string
	if book != nil {
		hint = append(hint, book.ID, book.VaultID, book.Name)
	}
	if abs != "" {
		hint = append(hint, filepath.Base(abs), books.ReadFolderVaultID(abs))
	}
	remote := matchVault(vaults, want, hint)
	if remote == nil {
		if want == "" {
			fmt.Fprintf(stderr, "Error: could not infer the vault; pass --vault <slug|id> (%d vault(s) visible at %s)\n", len(vaults), server)
		} else {
			fmt.Fprintf(stderr, "Error: vault %q not found at %s\n", want, server)
		}
		return 1
	}
	if other := reg.ByVaultID(remote.ID); other != nil && (book == nil || other.ID != book.ID) {
		fmt.Fprintf(stderr, "Error: vault %q is already bound to book %q (one book per vault). Unlink it first.\n", remote.Name, other.Name)
		return 1
	}
	if book == nil {
		name := o.Name
		if name == "" {
			name = remote.Name
		}
		nb := books.Book{ID: mustNewID(), Name: name, Kind: books.KindBound, Path: books.EncodeHome(abs)}
		if reg.Get(remote.ID) == nil {
			nb.ID = strings.ToLower(remote.ID)
		}
		if book, err = reg.Add(nb); err != nil {
			fmt.Fprintf(stderr, "Error: %v\n", err)
			return 1
		}
	}
	book.Kind = books.KindBound
	book.ServerURL = server
	book.Account = acc.Username
	book.VaultID = strings.ToLower(remote.ID)
	book.Name = remote.Name // bound books cache the server name; no local alias
	if code := saveBooks(st, stderr); code != 0 {
		return code
	}
	fmt.Fprintf(stdout, "Book %q bound to %s@%s (vault %s).\n", book.Name, acc.Username, books.Host(server), remote.Slug)
	if book.Path != "" {
		fmt.Fprintf(stdout, "Push existing notes with: lumi vault sync %s/%s %s --account %s\n", server, remote.Slug, book.AbsPath(), acc.Username)
	}
	return 0
}

// matchVault resolves --vault (slug or id) or, without it, the first hint
// that equals a vault id or slug.
func matchVault(vaults []account.RemoteVault, want string, hints []string) *account.RemoteVault {
	try := []string{want}
	if want == "" {
		try = hints
	}
	for _, h := range try {
		if h == "" {
			continue
		}
		for i, v := range vaults {
			if strings.EqualFold(v.ID, h) || v.Slug == h {
				return &vaults[i]
			}
		}
	}
	return nil
}

func runBooksUnlink(args []string, stdout, stderr io.Writer) int {
	if len(args) != 1 || strings.HasPrefix(args[0], "-") {
		if len(args) == 1 && (args[0] == "--help" || args[0] == "-h") {
			fmt.Fprint(stdout, booksUsage)
			return 0
		}
		fmt.Fprintf(stderr, "Error: unlink takes exactly one <book>\n")
		return 64
	}
	st, code := loadBooks(stdout, stderr)
	if code != 0 {
		return code
	}
	b := st.Registry.Find(args[0])
	if b == nil {
		fmt.Fprintf(stderr, "No book matches %q.\n", args[0])
		return 2
	}
	name := b.Name
	if b.IsBound() && b.Path != "" {
		b.Unbind()
		fmt.Fprintf(stdout, "Book %q is now local; the server vault was not touched.\n", name)
	} else {
		st.Registry.Remove(b.ID)
		fmt.Fprintf(stdout, "Removed %q from the list; no files were deleted and the server was not touched.\n", name)
	}
	return saveBooks(st, stderr)
}

func runBooksRename(args []string, stdout, stderr io.Writer) int {
	if len(args) != 2 {
		fmt.Fprintf(stderr, "Error: usage: lumi books rename <book> <name>\n")
		return 64
	}
	st, code := loadBooks(stdout, stderr)
	if code != 0 {
		return code
	}
	b := st.Registry.Find(args[0])
	if b == nil {
		fmt.Fprintf(stderr, "No book matches %q.\n", args[0])
		return 2
	}
	newName := strings.TrimSpace(args[1])
	if newName == "" {
		fmt.Fprintln(stderr, "Error: name must not be empty")
		return 64
	}
	if b.IsBound() {
		if b.IncompleteBinding() {
			fmt.Fprintln(stderr, "Error: this book's binding is incomplete; relink it first (lumi books link <book> --server <url>)")
			return 1
		}
		acc, code := lookupAccount(b.ServerURL, b.Account, stderr)
		if code != 0 {
			return code
		}
		client, err := account.NewClient(b.ServerURL)
		if err != nil {
			fmt.Fprintf(stderr, "Error: %v\n", err)
			return 1
		}
		client.SetToken(acc.Token)
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		v, err := client.RenameVault(ctx, b.VaultID, newName)
		if err != nil {
			var apiErr *account.APIError
			if errors.As(err, &apiErr) && apiErr.Status == 403 {
				fmt.Fprintln(stderr, "Error: renaming a bound book renames it for every member and needs the vault.manage capability.")
				return 1
			}
			fmt.Fprintf(stderr, "Error: rename on server: %v\n", err)
			return 1
		}
		newName = v.Name
		if newName == "" {
			newName = strings.TrimSpace(args[1])
		}
	}
	old := b.Name
	b.Name = newName
	if code := saveBooks(st, stderr); code != 0 {
		return code
	}
	fmt.Fprintf(stdout, "Renamed %q -> %q.\n", old, newName)
	return 0
}

func runBooksUse(args []string, stdout, stderr io.Writer) int {
	if len(args) != 1 {
		fmt.Fprintln(stderr, "Error: usage: lumi books use <book>")
		return 64
	}
	st, code := loadBooks(stdout, stderr)
	if code != 0 {
		return code
	}
	b := st.Registry.Find(args[0])
	if b == nil {
		fmt.Fprintf(stderr, "No book matches %q.\n", args[0])
		return 2
	}
	if err := st.Registry.SetActive(b.ID, time.Now()); err != nil {
		fmt.Fprintf(stderr, "Error: %v\n", err)
		return 1
	}
	if code := saveBooks(st, stderr); code != 0 {
		return code
	}
	fmt.Fprintf(stdout, "Active book: %s\n", b.Name)
	if b.Path == "" {
		fmt.Fprintln(stdout, "This book has no local folder yet; clone it with 'lumi vault clone'.")
	}
	return 0
}

// updateBooks applies fn to the book registry and saves it. Used by the
// legacy `lumi vault link|clone|unlink` commands so books.yaml stays the
// canonical list while vaults.yaml keeps being written for older clients.
// Best-effort: failures are reported as warnings, never fatal.
func updateBooks(stderr io.Writer, fn func(reg *books.Registry) bool) {
	st, err := openBooks()
	if err != nil {
		fmt.Fprintf(stderr, "Warning: books.yaml not updated: %v\n", err)
		return
	}
	if !fn(st.Registry) {
		return
	}
	if err := st.Save(); err != nil {
		fmt.Fprintf(stderr, "Warning: books.yaml not updated: %v\n", err)
	}
}
