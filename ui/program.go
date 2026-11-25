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
	return ProgramOptionsTap(in, out, nil)
}

// ProgramOptionsTap is ProgramOptions with tap called with every frame
// exactly as it is written to a terminal output (tests measure frames
// there instead of timing reads from the pseudo-terminal).
func ProgramOptionsTap(in io.Reader, out *os.File, tap func([]byte)) []tea.ProgramOption {
	opts := []tea.ProgramOption{tea.WithAltScreen()}
	if in != nil {
		opts = append(opts, tea.WithInput(in))
	}
	if out == nil {
		out = os.Stdout
	}
	if term.IsTerminal(int(out.Fd())) {
		opts = append(opts, tea.WithOutput(syncWriter{File: out, tap: tap}))
	} else if out != os.Stdout {
		opts = append(opts, tea.WithOutput(out))
	}
	return opts
}

// Animating reports whether a frame tick is wanted (a spring moves or a
// dot breathes). Tests use it to know when the screen has settled.
func (m Model) Animating() bool { return m.mo.busy() }
