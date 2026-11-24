// tui-client/main.go
package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/vinizap/lumi/tui-client/account"
	"github.com/vinizap/lumi/tui-client/books"
	"github.com/vinizap/lumi/tui-client/feed"
	"github.com/vinizap/lumi/tui-client/ui"
	"github.com/vinizap/lumi/tui-client/vim"
)

// Version is overridden at link time via -ldflags="-X main.Version=...".
var Version = "dev"

const usage = `lumi — terminal markdown notes

Usage:
  lumi                       open the notes dir from $LUMI_NOTES_DIR or the current dir
  lumi <directory>           open a directory of .md files (lumi-managed or plain)
  lumi <file.md>             open a single .md file (parent dir becomes the workspace)
  lumi login <server-url>    sign in to a lumi-server v2 instance (Phase 5)
  lumi accounts [--verify]   list signed-in accounts (several per server)
  lumi accounts link|signout|remove   share sign-in with Lumi.app; sign out here or on the whole Mac
  lumi books [list|refresh|link|unlink|rename|use]   manage books (livros)
  lumi vaults                alias of 'lumi books list'
  lumi vault link <path>     register a directory as a vault (Phase 5)
  lumi vault clone <url>/<slug> [<path>]   pull a server vault locally (Phase 5)
  lumi vault transfer <url>/<slug> <username>   hand vault ownership to a member (v3)
  lumi vault copy <url>/<slug> <recipient>      share an independent copy (v3)
  lumi open [<book>[/<note>]] [--web]   open in the Lumi app (macOS) or the web client
  lumi --help, -h            show this help
  lumi --version, -v         print version

Plain markdown notes (no YAML frontmatter) are supported. Lumi reads them
without modification and preserves the on-disk format on save unless the
note already had frontmatter or you explicitly add metadata from the UI.
`

func main() {
	// Subcommand dispatch happens before parseArgs so `lumi <subcommand>` is
	// not mis-parsed as `lumi <directory>`.
	if len(os.Args) >= 2 {
		switch os.Args[1] {
		case "login":
			os.Exit(runLoginCmd(os.Args[2:], os.Stdin, os.Stdout, os.Stderr))
		case "accounts":
			os.Exit(runAccountsCmd(os.Args[2:], os.Stdout, os.Stderr))
		case "books", "livros":
			os.Exit(runBooksCmd(os.Args[2:], os.Stdout, os.Stderr))
		case "vaults":
			// SPEC-LIVROS §6 T2: `lumi vaults` is an alias of `lumi books list`.
			os.Exit(runBooksCmd(append([]string{"list"}, os.Args[2:]...), os.Stdout, os.Stderr))
		case "vault":
			os.Exit(runVaultCmd(os.Args[2:], os.Stdout, os.Stderr))
		case "open":
			os.Exit(runOpenCmd(os.Args[2:], os.Stdout, os.Stderr))
		}
	}

	rootDir, initialNote, splash, exitCode, ok := parseArgs(os.Args[1:])
	if !ok {
		os.Exit(exitCode)
	}

	// Book registry (books.yaml; imported once from vaults.yaml). A
	// failure here never blocks opening notes.
	store, berr := books.Open()
	if berr != nil {
		fmt.Fprintf(os.Stderr, "Warning: books.yaml: %v\n", berr)
		store = &books.Store{Registry: &books.Registry{}}
	}

	// When the user gave lumi no hint at all (no CLI arg AND no
	// $LUMI_NOTES_DIR), offer the book picker before falling back to the
	// current folder, when there are books with a folder on this device.
	if splash && initialNote == "" {
		var withFolder []books.Book
		for _, b := range store.Registry.Books {
			if b.Path != "" {
				withFolder = append(withFolder, b)
			}
		}
		if shouldRunVaultPicker(splash, initialNote, len(withFolder)) {
			picked, perr := runVaultPicker(withFolder)
			if perr == nil && picked != nil {
				rootDir = picked.AbsPath()
				splash = false
			}
			// Cancellation or loader error → fall through to the current
			// folder so the user is never blocked by a misbehaving registry.
		}
	}

	if abs, err := filepath.Abs(rootDir); err == nil {
		rootDir = abs
	}
	if initialNote != "" {
		if abs, err := filepath.Abs(initialNote); err == nil {
			initialNote = abs
		}
	}

	// Legacy per-folder token (<root>/.lumi/config.yaml) → accounts.yaml,
	// then scrub it from the folder (SPEC-LIVROS §2.5). Best-effort.
	importFolderToken(rootDir, os.Stderr)

	accounts := []account.Account{}
	if f, err := account.Load(); err == nil {
		accounts = f.Accounts
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	mgr := feed.NewManager(store, accounts)
	mgr.Start(ctx)
	// Shared sign-in with Lumi.app (SPEC-LIVROS §8).
	acctSync := startAccountSync(ctx, mgr, accounts)
	defer acctSync.stop()

	for {
		activeID := ""
		if !splash {
			if b, ok := mgr.BookByPath(rootDir); ok {
				activeID = b.ID
				// Opening a book counts as using it (active + last_opened_at).
				if store.Registry.ReadOnly {
					// never rewrite a newer books.yaml
				} else if err := mgr.SetActive(b.ID); err != nil {
					fmt.Fprintf(os.Stderr, "Warning: could not update books.yaml: %v\n", err)
				}
			}
			// Keep vaults.yaml's last_opened_at fresh for older clients.
			_, _ = account.BumpLastOpenedAt(rootDir, time.Now())
		}

		// Opens the note, or the book's last note, or the empty home
		// with the floating explorer open (ui/lastnote.go).
		model := ui.NewModelWithInitialNote(rootDir, initialNote).WithBooks(mgr, activeID)

		p := tea.NewProgram(model, ui.ProgramOptions(nil, nil)...)
		acctSync.setSend(p.Send)
		final, err := p.Run()
		acctSync.setSend(nil)
		// the built-in editor may have changed the cursor shape (DECSCUSR)
		fmt.Fprint(os.Stdout, vim.ResetCursorShape)
		if fm, ok := final.(ui.Model); ok {
			fm.FlushEdits()
		}
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		// `gb` / `g1`–`g9` picked another book: restart the model on it.
		if fm, ok := final.(ui.Model); ok && fm.SwitchTarget() != "" {
			rootDir, initialNote, splash = fm.SwitchTarget(), "", false
			continue
		}
		return
	}
}

// parseArgs resolves CLI arguments to (rootDir, initialNote, splash)
// and an exit signal. ok=false means the caller should exit
// immediately (help/version printed, or a usage error was emitted).
//
//   - No args, no $LUMI_NOTES_DIR: rootDir = current dir; splash = true
//     ("no hint": the only case that offers the book picker first).
//   - $LUMI_NOTES_DIR set: rootDir = env var; splash = false.
//   - One dir: rootDir = dir; splash = false.
//   - One .md file: rootDir = parent dir; initialNote = file path; splash = false.
//   - Help / version flag: print + exit 0.
//   - Anything else: print error + usage to stderr; exit 64 (EX_USAGE).
func parseArgs(args []string) (rootDir, initialNote string, splash bool, exitCode int, ok bool) {
	if len(args) == 0 {
		envDir := os.Getenv("LUMI_NOTES_DIR")
		if envDir == "" {
			d, n, code, dirOk := validateDir(".")
			return d, n, true, code, dirOk
		}
		d, n, code, dirOk := validateDir(envDir)
		return d, n, false, code, dirOk
	}

	switch args[0] {
	case "--help", "-h", "help":
		fmt.Print(usage)
		return "", "", false, 0, false
	case "--version", "-v", "version":
		fmt.Printf("lumi %s\n", Version)
		return "", "", false, 0, false
	}

	if strings.HasPrefix(args[0], "-") {
		fmt.Fprintf(os.Stderr, "Error: unknown flag %q\n\n", args[0])
		fmt.Fprint(os.Stderr, usage)
		return "", "", false, 64, false
	}

	target := args[0]
	info, err := os.Stat(target)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: cannot open %q: %v\n", target, err)
		return "", "", false, 1, false
	}
	if info.IsDir() {
		return target, "", false, 0, true
	}
	// Single file: must be markdown for lumi to make sense of it.
	if !strings.HasSuffix(strings.ToLower(target), ".md") {
		fmt.Fprintf(os.Stderr, "Error: %q is not a directory or .md file\n", target)
		return "", "", false, 1, false
	}
	parent := filepath.Dir(target)
	if parent == "" {
		parent = "."
	}
	return parent, target, false, 0, true
}

func validateDir(dir string) (string, string, int, bool) {
	info, err := os.Stat(dir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: cannot open %q: %v\n", dir, err)
		return "", "", 1, false
	}
	if !info.IsDir() {
		fmt.Fprintf(os.Stderr, "Error: %q is not a directory\n", dir)
		return "", "", 1, false
	}
	return dir, "", 0, true
}
