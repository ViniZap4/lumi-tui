// tui-client/editor/editor.go
package editor

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/vinizap/lumi/tui-client/config"
	"github.com/vinizap/lumi/tui-client/domain"
)

// OpenCmd opens the note in the configured editor.
func OpenCmd(note *domain.Note) *exec.Cmd {
	return OpenCmdAt(note, 0, 0)
}

// OpenCmdAt opens the note in the configured editor at a specific line and column.
// Line and col are 0-indexed; they are converted to 1-indexed for the editor.
func OpenCmdAt(note *domain.Note, line, col int) *exec.Cmd {
	cfg := config.Load()
	editorName := cfg.Editor
	editorArgs := cfg.EditorArgs

	if envEditor := os.Getenv("EDITOR"); envEditor != "" {
		editorName = envEditor
		editorArgs = nil
	}

	var args []string
	args = append(args, editorArgs...)

	// Add cursor position args based on editor
	if line > 0 || col > 0 {
		l := line + 1 // editors are 1-indexed
		c := col + 1
		base := filepath.Base(editorName)
		switch base {
		case "nvim", "vim", "vi":
			args = append(args, fmt.Sprintf("+call cursor(%d,%d)", l, c))
		default:
			// +LINE is widely supported (nano, micro, helix, etc.)
			args = append(args, fmt.Sprintf("+%d", l))
		}
	}

	args = append(args, note.Path)
	return exec.Command(editorName, args...)
}
