// tui-client/main.go
package main

import (
	"fmt"
	"os"
	"path/filepath"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/vinizap/lumi/tui-client/ui"
)

func main() {
	var rootDir string

	// Check command line args first
	if len(os.Args) > 1 {
		rootDir = os.Args[1]
	} else {
		// Fall back to env var
		rootDir = os.Getenv("LUMI_NOTES_DIR")
		if rootDir == "" {
			// Default to current directory
			rootDir = "."
		}
	}

	// Verify directory exists
	if info, err := os.Stat(rootDir); err != nil || !info.IsDir() {
		fmt.Fprintf(os.Stderr, "Error: '%s' is not a valid directory\n", rootDir)
		os.Exit(1)
	}

	// Resolve to absolute path so image/link resolution works regardless of CWD
	if abs, err := filepath.Abs(rootDir); err == nil {
		rootDir = abs
	}

	p := tea.NewProgram(
		ui.NewSimpleModel(rootDir),
		tea.WithAltScreen(),
	)

	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
