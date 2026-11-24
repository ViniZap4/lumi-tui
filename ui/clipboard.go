package ui

import (
	"encoding/base64"
	"io"
	"os"

	"github.com/atotto/clipboard"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/vinizap/lumi/tui-client/vim"
)

// termOut is where terminal control sequences go (OSC 52, DECSCUSR).
// Tests replace it with a buffer.
var termOut io.Writer = os.Stdout

// termClipboard is the OS clipboard (pbcopy, xclip/wl-copy, Windows),
// falling back to OSC 52 — the terminal sets the clipboard, which also
// works over SSH — when no clipboard tool is available. Reading falls
// back to the last text this process wrote.
type termClipboard struct{ last string }

func newTermClipboard() *termClipboard { return &termClipboard{} }

func (c *termClipboard) Write(s string) error {
	c.last = s
	if err := clipboard.WriteAll(s); err == nil {
		return nil
	}
	return writeOSC52(termOut, s)
}

func (c *termClipboard) Read() (string, error) {
	if s, err := clipboard.ReadAll(); err == nil {
		return s, nil
	}
	return c.last, nil
}

// maxOSC52 keeps the sequence under common terminal limits.
const maxOSC52 = 100_000

func writeOSC52(w io.Writer, s string) error {
	if len(s) > maxOSC52 {
		s = s[:maxOSC52]
	}
	_, err := io.WriteString(w, "\x1b]52;c;"+base64.StdEncoding.EncodeToString([]byte(s))+"\x07")
	return err
}

// cursorShapeCmd sets the terminal cursor shape (DECSCUSR). The frame
// also draws the shape (block, bar, underline) on the in-frame cursor,
// since Bubbletea keeps the hardware cursor hidden while it runs.
func cursorShapeCmd(seq string) tea.Cmd {
	return func() tea.Msg {
		_, _ = io.WriteString(termOut, seq)
		return nil
	}
}

var _ vim.Clipboard = (*termClipboard)(nil)
