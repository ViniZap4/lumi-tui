// tui-client/main.go
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/vinizap/lumi/tui-client/account"
	"github.com/vinizap/lumi/tui-client/ui"
)

// Version is overridden at link time via -ldflags="-X main.Version=...".
var Version = "dev"

const usage = `lumi — terminal markdown notes

Usage:
  lumi                       open the notes dir from $LUMI_NOTES_DIR or the current dir
  lumi <directory>           open a directory of .md files (lumi-managed or plain)
  lumi <file.md>             open a single .md file (parent dir becomes the workspace)
  lumi login <server-url>    sign in to a lumi-server v2 instance (Phase 5)
  lumi accounts [--verify]   list signed-in servers (Phase 5)
  lumi vaults                list vaults from ~/.config/lumi/vaults.yaml (Phase 5)
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
		case "vaults":
			os.Exit(runVaultsCmd(os.Args[2:], os.Stdout, os.Stderr))
		}
	}

	rootDir, initialNote, splash, exitCode, ok := parseArgs(os.Args[1:])
	if !ok {
		os.Exit(exitCode)
	}

	// Splash screen only when the user gave lumi no hint at all (no CLI
	// arg AND no $LUMI_NOTES_DIR). In that case, before falling back to
	// "splash on cwd", offer the vault picker if vaults.yaml has any
	// entries — Apple-client users will usually have at least one,
	// even before they've signed in to a server.
	if splash && initialNote == "" {
		entries, err := account.LoadVaults()
		if err == nil && shouldRunVaultPicker(splash, initialNote, len(entries)) {
			picked, perr := runVaultPicker(entries)
			if perr == nil && picked != nil {
				rootDir = picked.Path
				splash = false
			}
			// Cancellation or loader error → fall through to splash so
			// the user is never blocked by a misbehaving registry.
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

	var model ui.Model
	if splash && initialNote == "" {
		model = ui.NewSimpleModel(rootDir)
	} else {
		model = ui.NewModelWithInitialNote(rootDir, initialNote)
	}

	p := tea.NewProgram(
		model,
		tea.WithAltScreen(),
	)

	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

// parseArgs resolves CLI arguments to (rootDir, initialNote, splash)
// and an exit signal. ok=false means the caller should exit
// immediately (help/version printed, or a usage error was emitted).
//
//   - No args, no $LUMI_NOTES_DIR: rootDir = current dir; splash = true
//     (this is the only case that opens with the welcome animation —
//     a brand-new user typing `lumi` from anywhere needs context).
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
