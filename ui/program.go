package ui

import (
	"io"
	"os"

	tea "github.com/charmbracelet/bubbletea"
	"golang.org/x/term"
)

// ProgramOptions are the Bubbletea options the TUI runs with. in/out
// default to the process terminal (nil); tests pass a pseudo-terminal.
// A terminal output is wrapped so every frame is one synchronized
// update (syncWriter, DEC 2026).
func ProgramOptions(in io.Reader, out *os.File) []tea.ProgramOption {
	opts := []tea.ProgramOption{tea.WithAltScreen()}
	if in != nil {
		opts = append(opts, tea.WithInput(in))
	}
	if out == nil {
		out = os.Stdout
	}
	if term.IsTerminal(int(out.Fd())) {
		opts = append(opts, tea.WithOutput(syncWriter{out}))
	} else if out != os.Stdout {
		opts = append(opts, tea.WithOutput(out))
	}
	return opts
}
