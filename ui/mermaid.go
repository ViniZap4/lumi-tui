package ui

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-runewidth"
	"github.com/vinizap/lumi/tui-client/mermaid"
	"github.com/vinizap/lumi/tui-client/theme"
)

// Mermaid blocks in the prose reader. A terminal cannot show the SVG
// the web and Apple clients draw, so a ```mermaid fence becomes a
// framed block headed "◇ mermaid · <kind>": simple flowcharts and
// sequence diagrams are drawn with box-drawing characters (package
// mermaid), everything else shows its source. gx inside the block opens
// the note in the Lumi app / web client (the `lumi open` deep link),
// where the real diagram renders.

// mermaidHead is the start of a block's top border (plain text); the
// reader finds blocks by it.
const mermaidHead = "╭─ ◇ mermaid"

// reMermaidFence opens a mermaid fence: ```mermaid / ~~~ mermaid.
var reMermaidFence = regexp.MustCompile("^\\s{0,3}(`{3,}|~{3,})\\s*mermaid\\s*$")

// mermaidFenceEnd reports whether line closes a fence opened by open.
func mermaidFenceEnd(line, open string) bool {
	t := strings.TrimSpace(line)
	return len(t) >= len(open) && strings.Trim(t, open[:1]) == ""
}

// renderMermaid draws one block for a column of width cells.
func renderMermaid(src string, width int) string {
	t := theme.Current
	width = max(12, width)
	inner := width - 4
	border := lipgloss.NewStyle().Foreground(t.Border)
	kind := mermaid.Kind(src)

	head := border.Render("╭─ ") +
		lipgloss.NewStyle().Foreground(t.Accent).Render("◇") + " " +
		lipgloss.NewStyle().Foreground(t.TextDim).Render("mermaid")
	headW := runewidth.StringWidth("╭─ ◇ mermaid")
	if kind != "" {
		k := " · " + kind
		head += lipgloss.NewStyle().Foreground(t.Secondary).Bold(true).Render(k)
		headW += runewidth.StringWidth(k)
	}
	head += border.Render(" " + strings.Repeat("─", max(0, width-headW-2)) + "╮")

	var body []string
	if lines, ok := mermaid.Render(src, inner); ok {
		dw := 0
		for _, l := range lines {
			dw = max(dw, l.Width())
		}
		lead := strings.Repeat(" ", (inner-dw)/2) // centred, like the web
		for _, l := range lines {
			body = append(body, lead+styleMermaidLine(l))
		}
	} else {
		code := lipgloss.NewStyle().Foreground(t.Text)
		comment := lipgloss.NewStyle().Foreground(t.TextDim).Italic(true)
		for _, l := range strings.Split(strings.Trim(src, "\n"), "\n") {
			l = strings.ReplaceAll(sanitizeCode(l), "\t", "    ")
			st := code
			if strings.HasPrefix(strings.TrimSpace(l), "%%") {
				st = comment
			}
			for _, part := range wrapCells(l, inner) {
				body = append(body, st.Render(part))
			}
		}
	}
	out := []string{head}
	side := border.Render("│")
	for _, b := range body {
		pad := max(0, inner-runewidth.StringWidth(stripAnsi(b)))
		out = append(out, side+" "+b+strings.Repeat(" ", pad)+" "+side)
	}
	out = append(out, border.Render("╰"+strings.Repeat("─", width-2)+"╯"))
	return strings.Join(out, "\n")
}

// sanitizeCode drops control characters (escape sequences in a note
// must never reach the terminal).
func sanitizeCode(s string) string {
	return strings.Map(func(r rune) rune {
		if r == '\t' {
			return r
		}
		if r < 0x20 || r == 0x7f || (r >= 0x80 && r < 0xa0) {
			return -1
		}
		return r
	}, s)
}

// wrapCells hard-wraps s to w display cells.
func wrapCells(s string, w int) []string {
	if s == "" {
		return []string{""}
	}
	var out []string
	var cur strings.Builder
	cw := 0
	for _, r := range s {
		rw := runewidth.RuneWidth(r)
		if cw+rw > w && cw > 0 {
			out = append(out, cur.String())
			cur.Reset()
			cw = 0
		}
		cur.WriteRune(r)
		cw += rw
	}
	return append(out, cur.String())
}

func styleMermaidLine(l mermaid.Line) string {
	t := theme.Current
	var b strings.Builder
	for _, sp := range l {
		st := lipgloss.NewStyle()
		switch sp.Class {
		case mermaid.Box:
			st = st.Foreground(t.Secondary)
		case mermaid.Edge:
			st = st.Foreground(t.TextDim)
		case mermaid.Arrow:
			st = st.Foreground(t.Accent)
		case mermaid.Text:
			st = st.Foreground(t.Text).Bold(true)
		case mermaid.Label:
			st = st.Foreground(t.Info)
		case mermaid.Note:
			st = st.Foreground(t.TextDim).Italic(true)
		default:
			b.WriteString(sp.Text)
			continue
		}
		b.WriteString(st.Render(sp.Text))
	}
	return b.String()
}

// mermaidBlocks finds the rendered mermaid blocks: [start, end] line
// ranges of plain (the reader's engine text).
func mermaidBlocks(plain [][]rune) [][2]int {
	var out [][2]int
	for i := 0; i < len(plain); i++ {
		if !strings.HasPrefix(string(plain[i]), mermaidHead) {
			continue
		}
		j := i + 1
		for j < len(plain) && !strings.HasPrefix(string(plain[j]), "╰") {
			j++
		}
		out = append(out, [2]int{i, min(j, len(plain)-1)})
		i = j
	}
	return out
}

// inMermaid reports whether rendered line l is inside a mermaid block.
func (d *readerDoc) inMermaid(l int) bool {
	for _, b := range d.mermaid {
		if l >= b[0] && l <= b[1] {
			return true
		}
	}
	return false
}

// mermaidFenceAt reports whether source line l of body sits inside a
// ```mermaid fence (fence lines included).
func mermaidFenceAt(body string, l int) bool {
	open := ""
	for i, line := range strings.Split(body, "\n") {
		if open == "" {
			if m := reMermaidFence.FindStringSubmatch(line); m != nil {
				open = m[1]
			} else if fenceLine.MatchString(line) {
				open = "-" // another fence: skip to its end
			}
			if i == l {
				return open != "" && open != "-"
			}
			continue
		}
		closes := false
		if open == "-" {
			closes = fenceLine.MatchString(line)
		} else {
			closes = mermaidFenceEnd(line, open)
		}
		if i == l {
			return open != "-"
		}
		if closes {
			open = ""
		}
	}
	return false
}

// openInAppFn opens a note in the Lumi app or web client through
// `lumi open <note path>` (the same resolution as the CLI: book from the
// path, note id from the sync cache, lumi:// on macOS, else the web
// URL). Tests replace it.
var openInAppFn = func(notePath string) (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	abs, err := filepath.Abs(notePath)
	if err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, exe, "open", abs).CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

// appOpenedMsg is the result of openInAppFn.
type appOpenedMsg struct {
	out string
	err error
}

// openNoteInApp opens p's note in the app / web (gx on a diagram).
func (m *Model) openNoteInApp(p *notePane) tea.Cmd {
	path := p.buf.path
	m.statusMsg = "abrindo o diagrama no app…"
	return func() tea.Msg {
		out, err := openInAppFn(path)
		return appOpenedMsg{out: out, err: err}
	}
}

func (m Model) onAppOpened(msg appOpenedMsg) (Model, tea.Cmd) {
	first := msg.out
	if i := strings.IndexByte(first, '\n'); i >= 0 {
		first = first[:i]
	}
	if msg.err != nil {
		if first == "" {
			first = msg.err.Error()
		}
		m.statusMsg = "lumi open: " + truncate(strings.TrimPrefix(first, "Error: "), 70)
		return m, nil
	}
	m.statusMsg = "aberto: " + truncate(strings.TrimPrefix(first, "Opened "), 60)
	return m, nil
}

// cursorInMermaid: the pane's cursor (the reader's / focused preview's,
// else the editor's) is inside a mermaid diagram.
func (m Model) cursorInMermaid(p *notePane) bool {
	switch {
	case p.view == viewReader:
		d, _ := m.readerDoc(p)
		return d.inMermaid(d.eng.Cursor().Line)
	case p.view == viewSplit && p.previewFocus:
		d, _ := m.previewDoc(p)
		return d.inMermaid(d.eng.Cursor().Line)
	}
	return mermaidFenceAt(p.buf.eng.Text(), p.buf.eng.Cursor().Line)
}
