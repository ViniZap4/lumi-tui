package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/vinizap/lumi/tui-client/books"
	"github.com/vinizap/lumi/tui-client/config"
	"github.com/vinizap/lumi/tui-client/feed"
	"github.com/vinizap/lumi/tui-client/filesystem"
	"github.com/vinizap/lumi/tui-client/opener"
)

const openUsage = `lumi open — open a book or note in the Lumi app or the web client

Usage:
  lumi open [<book>[/<note-path>]] [--web] [--print]

  <book> is a book name, id, picker number (1-9) or folder; without it,
  the book of the current folder (or the active book) opens. <note-path>
  is relative to the book folder (".md" optional).

  macOS opens the Lumi app through lumi://open?… and falls back to the
  web client when no app handles lumi://. Linux and Windows open the web
  client in the default browser.

  --web     always open the web client
  --print   print the URL instead of opening it

  The web address comes from Settings (web_origins in config.yaml) or is
  derived from the server: https://lumi-api.<host> → https://lumi.<host>.
`

// Test hooks: never launch anything from tests.
var (
	openRunner opener.Runner = func(name string, args ...string) error {
		return exec.Command(name, args...).Run()
	}
	openGOOS = runtime.GOOS
)

func runOpenCmd(args []string, stdout, stderr io.Writer) int {
	var ref string
	forceWeb, printOnly := false, false
	for _, a := range args {
		switch a {
		case "--web":
			forceWeb = true
		case "--print":
			printOnly = true
		case "-h", "--help":
			fmt.Fprint(stdout, openUsage)
			return 0
		default:
			if strings.HasPrefix(a, "-") || ref != "" {
				fmt.Fprintf(stderr, "Error: unexpected argument %q\n\n%s", a, openUsage)
				return 64
			}
			ref = a
		}
	}
	st, err := books.Open()
	if err != nil {
		fmt.Fprintf(stderr, "Error: books.yaml: %v\n", err)
		return 1
	}
	b, notePath, err := resolveOpenRef(st.Registry, ref)
	if err != nil {
		fmt.Fprintf(stderr, "Error: %v\n", err)
		return 1
	}
	appURL, webURL, err := openURLs(*b, notePath, config.Load())
	if err != nil && !errors.Is(err, opener.ErrNoWebOrigin) {
		fmt.Fprintf(stderr, "Error: %v\n", err)
		return 1
	}
	if (forceWeb || openGOOS != "darwin") && webURL == "" {
		fmt.Fprintln(stderr, webOriginHint(*b))
		return 1
	}
	if printOnly {
		if forceWeb || openGOOS != "darwin" || appURL == "" {
			fmt.Fprintln(stdout, webURL)
		} else {
			fmt.Fprintln(stdout, appURL)
		}
		return 0
	}
	used, err := opener.Open(openGOOS, openRunner, appURL, webURL, forceWeb)
	if err != nil {
		if errors.Is(err, opener.ErrNoWebOrigin) {
			fmt.Fprintln(stderr, webOriginHint(*b))
		} else {
			fmt.Fprintf(stderr, "Error: %v\n", err)
		}
		return 1
	}
	fmt.Fprintf(stdout, "Opened %s\n", used)
	return 0
}

func webOriginHint(b books.Book) string {
	if !b.IsBound() {
		return fmt.Sprintf("Error: %q is a local book; only the Lumi app (macOS) can open it.", b.Name)
	}
	return fmt.Sprintf("Error: the web address of %s is unknown. Set it in lumi settings (, → Servidores) or add\n  web_origins: %s=https://<web-host>\nto ~/.config/lumi/config.yaml.", b.ServerURL, b.ServerURL)
}

// resolveOpenRef finds the book (and note path) for `lumi open <ref>`.
func resolveOpenRef(reg *books.Registry, ref string) (*books.Book, string, error) {
	if ref == "" {
		if wd, err := os.Getwd(); err == nil {
			for i := range reg.Books {
				p := reg.Books[i].AbsPath()
				if p != "" && (books.SameDir(p, wd) || strings.HasPrefix(wd, p+string(filepath.Separator))) {
					return &reg.Books[i], "", nil
				}
			}
		}
		if b := reg.ActiveBook(); b != nil {
			return b, "", nil
		}
		return nil, "", fmt.Errorf("no book here and no active book (lumi books list)")
	}
	if b := reg.Find(ref); b != nil {
		return b, "", nil
	}
	// <book>/<note-path>: try every '/' split, longest book name first.
	for i := strings.LastIndex(ref, "/"); i > 0; i = strings.LastIndex(ref[:i], "/") {
		if b := reg.Find(ref[:i]); b != nil {
			return b, ref[i+1:], nil
		}
	}
	return nil, "", fmt.Errorf("no book matches %q (lumi books list)", ref)
}

// openURLs builds the app and web URLs for a book/note.
func openURLs(b books.Book, notePath string, cfg *config.Config) (appURL, webURL string, err error) {
	t := opener.Target{}
	var noteID, rel string
	if notePath != "" {
		rel, noteID, err = resolveNote(b, notePath)
		if err != nil {
			return "", "", err
		}
	}
	if b.IsBound() {
		if b.VaultID == "" {
			return "", "", fmt.Errorf("%q has an incomplete binding (lumi books link)", b.Name)
		}
		t.ServerURL, t.Account, t.VaultID, t.NoteID = b.ServerURL, b.Account, b.VaultID, noteID
	} else {
		t.LocalPath, t.NoteRel = b.AbsPath(), rel
	}
	appURL, err = opener.AppURL(t)
	if err != nil {
		return "", "", err
	}
	if !b.IsBound() {
		return appURL, "", opener.ErrNoWebOrigin
	}
	origin, err := opener.WebOrigin(b.ServerURL, cfg.WebOrigins)
	if err != nil {
		return appURL, "", err
	}
	webURL, err = opener.WebURL(origin, b.VaultID, noteID)
	return appURL, webURL, err
}

// resolveNote maps a note path inside the book to (relative path, note
// id). The id comes from the sync cache (bound books) or the note's
// frontmatter; paths outside the book are refused.
func resolveNote(b books.Book, notePath string) (string, string, error) {
	root := b.AbsPath()
	rel := filepath.ToSlash(filepath.Clean(notePath))
	if strings.HasPrefix(rel, "../") || rel == ".." || filepath.IsAbs(notePath) {
		return "", "", fmt.Errorf("note path %q is outside the book", notePath)
	}
	if root != "" && !strings.HasSuffix(rel, ".md") && !strings.HasSuffix(rel, ".canvas") {
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(rel))); err != nil {
			rel += ".md"
		}
	}
	if b.IsBound() && b.VaultID != "" {
		dir, _ := feed.CacheDir()
		c := feed.LoadCache(dir, b.ID, b.VaultID, b.ServerURL)
		for id, n := range c.Notes {
			if filepath.ToSlash(n.Path) == rel {
				return rel, id, nil
			}
		}
	}
	if root == "" {
		return "", "", fmt.Errorf("%q has no folder on this device; cannot resolve %q", b.Name, notePath)
	}
	abs := filepath.Join(root, filepath.FromSlash(rel))
	if r, err := filepath.Rel(root, abs); err != nil || strings.HasPrefix(r, "..") {
		return "", "", fmt.Errorf("note path %q is outside the book", notePath)
	}
	n, err := filesystem.ReadNote(abs)
	if err != nil {
		return "", "", fmt.Errorf("note %q not found in %s", notePath, b.Name)
	}
	if b.IsBound() && n.ID == "" {
		return "", "", fmt.Errorf("note %q has no id yet (sync the book first)", notePath)
	}
	return rel, n.ID, nil
}
