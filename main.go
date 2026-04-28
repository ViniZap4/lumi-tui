// tui-client/main.go
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/vinizap/lumi/tui-client/ui"
)

// Version is overridden at link time via -ldflags="-X main.Version=...".
var Version = "dev"

const usage = `lumi — terminal markdown notes

Usage:
  lumi                       open the notes dir from $LUMI_NOTES_DIR or the current dir
  lumi <directory>           open a directory of .md files (lumi-managed or plain)
  lumi <file.md>             open a single .md file (parent dir becomes the workspace)
  lumi --help, -h            show this help
  lumi --version, -v         print version

Plain markdown notes (no YAML frontmatter) are supported. Lumi reads them
without modification and preserves the on-disk format on save unless the
note already had frontmatter or you explicitly add metadata from the UI.
`

func main() {
	rootDir, initialNote, exitCode, ok := parseArgs(os.Args[1:])
	if !ok {
		os.Exit(exitCode)
	}

	if abs, err := filepath.Abs(rootDir); err == nil {
		rootDir = abs
	}
	if initialNote != "" {
		if abs, err := filepath.Abs(initialNote); err == nil {
			initialNote = abs
		}
	}

	model := ui.NewModelWithInitialNote(rootDir, initialNote)

	p := tea.NewProgram(
		model,
		tea.WithAltScreen(),
	)

	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

// parseArgs resolves CLI arguments to (rootDir, initialNote) and an exit
// signal. Returns ok=false when the caller should exit immediately
// (after help/version was printed or a usage error was emitted).
//
//   - No args: rootDir = $LUMI_NOTES_DIR or current dir; initialNote = "".
//   - One dir: rootDir = dir; initialNote = "".
//   - One .md file: rootDir = parent dir; initialNote = file path.
//   - Help / version flag: print + exit 0.
//   - Anything else: print error + usage to stderr; exit 64 (EX_USAGE).
func parseArgs(args []string) (rootDir, initialNote string, exitCode int, ok bool) {
	if len(args) == 0 {
		dir := os.Getenv("LUMI_NOTES_DIR")
		if dir == "" {
			dir = "."
		}
		return validateDir(dir)
	}

	switch args[0] {
	case "--help", "-h", "help":
		fmt.Print(usage)
		return "", "", 0, false
	case "--version", "-v", "version":
		fmt.Printf("lumi %s\n", Version)
		return "", "", 0, false
	}

	if strings.HasPrefix(args[0], "-") {
		fmt.Fprintf(os.Stderr, "Error: unknown flag %q\n\n", args[0])
		fmt.Fprint(os.Stderr, usage)
		return "", "", 64, false
	}

	target := args[0]
	info, err := os.Stat(target)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: cannot open %q: %v\n", target, err)
		return "", "", 1, false
	}
	if info.IsDir() {
		return target, "", 0, true
	}
	// Single file: must be markdown for lumi to make sense of it.
	if !strings.HasSuffix(strings.ToLower(target), ".md") {
		fmt.Fprintf(os.Stderr, "Error: %q is not a directory or .md file\n", target)
		return "", "", 1, false
	}
	parent := filepath.Dir(target)
	if parent == "" {
		parent = "."
	}
	return parent, target, 0, true
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
