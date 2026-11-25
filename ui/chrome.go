package ui

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-runewidth"
	"github.com/vinizap/lumi/tui-client/theme"
	"github.com/vinizap/lumi/tui-client/vim"
)

// Screen chrome shared by every view:
//
//	┌ body (contentHeight rows) ─────────────────────────────┐
//	│ legend: context keys / which-key / prompt / confirm    │  (optional)
//	│ status: mode · path · position        book state · n   │
//	└────────────────────────────────────────────────────────┘

// legendVisible: the legend row is shown when enabled, and always while
// a prompt, a confirmation or the filter needs the line.
func (m Model) legendVisible() bool {
	return m.legendOn || m.showInput || m.showConfirm || m.filterTyping
}

func (m Model) statusVisible() bool { return true }

// bottomRows is how many rows the chrome takes.
func (m Model) bottomRows() int {
	n := 0
	if m.legendVisible() {
		n++
	}
	if m.statusVisible() {
		n++
	}
	return n
}

// contentHeight is the body height.
func (m Model) contentHeight() int { return max(1, m.height-m.bottomRows()) }

// legendContext resolves where the user is for the legend.
func (m Model) legendContext() LegendContext {
	switch {
	case m.showConfirm:
		return CtxConfirm
	case m.showInput:
		return CtxPrompt
	case m.showPalette:
		return CtxPalette
	case m.showHelp:
		return CtxHelp
	case m.showBookDetails:
		return CtxDetails
	case m.acct != nil && m.acct.form != nil:
		return CtxLogin
	case m.acct != nil:
		return CtxAccounts
	case m.clone != nil:
		return CtxClone
	case m.filterTyping:
		return CtxFilter
	case m.viewMode == ViewConfig:
		return CtxSettings
	case m.floatExp && m.bk != nil:
		return CtxBooks
	case m.floatExp:
		switch m.pane {
		case PaneParent:
			return CtxParent
		case PanePreview:
			return CtxPreview
		}
		return CtxFloat
	}
	if m.viewMode == ViewFullNote && m.ws != nil && m.ws.focus != nil {
		p := m.ws.focus
		switch {
		case m.hover != nil:
			return CtxHover
		case m.winPending:
			return CtxWindow
		}
		switch p.view {
		case viewBoard:
			if !p.bedit {
				return CtxBoardRead
			}
			return CtxBoard
		case viewCanvas:
			return CtxCanvas
		case viewSource, viewSplit:
			if p.view == viewSplit && p.previewFocus {
				return proseCtx(m.focusEngine(), CtxSplitPre)
			}
			switch e := p.buf.eng; {
			case e.Mode() == vim.ModeInsert || e.Mode() == vim.ModeReplace:
				return CtxInsert
			case e.Mode() == vim.ModeCmdline:
				return CtxCmdline
			case e.Mode().IsVisual():
				return CtxVisual
			}
			return CtxEditor
		}
		return proseCtx(m.focusEngine(), CtxReader)
	}
	return CtxHome
}

func promptLabel(mode string) string {
	switch mode {
	case "create":
		return "new"
	case "rename":
		return "rename"
	case "rename_folder":
		return "rename folder"
	case "config_editor":
		return "editor command"
	case "find":
		return "find"
	case "config_escape":
		return "escape sequences (jj,jk)"
	}
	return mode
}

// renderLegendRow is the legend line, or the prompt / confirmation /
// filter input when one is open.
func (m Model) renderLegendRow() string {
	t := theme.Current
	caret := lipgloss.NewStyle().Foreground(t.Accent).Render("▏")
	switch {
	case m.showConfirm:
		q := lipgloss.NewStyle().Foreground(t.Warning).Bold(true).Render(" " + m.confirmMsg)
		hint := lipgloss.NewStyle().Foreground(mutedColor).Render("  y confirm · n cancel")
		return padVisual(q+hint, m.width)
	case m.showInput:
		label := lipgloss.NewStyle().Foreground(t.Primary).Bold(true).Render(" " + promptLabel(m.inputMode) + " › ")
		val := lipgloss.NewStyle().Foreground(t.Text).Render(m.inputValue)
		hint := ""
		if m.inputMode == "create" {
			hint = lipgloss.NewStyle().Foreground(mutedColor).Render("   a/b/name · dir/ · ⏎ ok · Esc cancel")
		}
		return padVisual(label+val+caret+hint, m.width)
	case m.legendContext() == CtxCmdline:
		kind, text := m.focusEngine().Cmdline()
		return padVisual(lipgloss.NewStyle().Foreground(t.Primary).Bold(true).Render(" "+string(kind))+
			lipgloss.NewStyle().Foreground(t.Text).Render(text)+caret, m.width)
	case m.filterTyping:
		label := lipgloss.NewStyle().Foreground(t.Primary).Bold(true).Render(" / ")
		val := lipgloss.NewStyle().Foreground(t.Text).Render(m.filter)
		n := lipgloss.NewStyle().Foreground(mutedColor).Render(fmt.Sprintf("   %d/%d · ⏎ keep · Esc clear", len(m.items), len(m.allItems)))
		return padVisual(label+val+caret+n, m.width)
	}
	if m.floatExp && m.viewMode != ViewConfig && m.overlaySettled() && m.acct == nil && !m.showPalette && !m.showHelp {
		// the overlay's own footer carries the legend (float_mouse.go)
		return padVisual("", m.width)
	}
	return renderLegendLine(legendFor(m.legendContext(), m.pendingInput()), m.width)
}

// overlaySettled: the floating explorer is fully open (its footer shows).
func (m Model) overlaySettled() bool {
	a, ok := m.mo.get(animOverlay)
	return !ok || a.value(0, 1) >= 1 || !a.active
}

func (m Model) modeName() string {
	switch m.legendContext() {
	case CtxFilter:
		return "FILTER"
	case CtxPrompt:
		return "INPUT"
	case CtxConfirm:
		return "CONFIRM"
	case CtxPalette:
		return "PALETTE"
	case CtxHelp:
		return "HELP"
	case CtxBooks, CtxDetails:
		return "BOOKS"
	case CtxSettings:
		return "SETTINGS"
	case CtxReader:
		return "READ"
	case CtxHome:
		return "HOME"
	case CtxVisual, CtxEditor, CtxInsert, CtxCmdline:
		if e := m.focusEngine(); e != nil {
			return e.Mode().String()
		}
		return "VISUAL"
	case CtxBoardRead:
		return "READ"
	case CtxBoard:
		return "EDIT"
	case CtxCanvas:
		return "CANVAS"
	case CtxWindow:
		return "WINDOW"
	case CtxFloat:
		return "EXPLORE"
	case CtxHover:
		return "LINK"
	case CtxAccounts:
		return "ACCOUNTS"
	case CtxLogin:
		return "SIGN IN"
	case CtxClone:
		return "CLONE"
	case CtxSplitPre:
		return "PREVIEW"
	case CtxParent:
		return "PARENT"
	case CtxPreview:
		return "PREVIEW"
	}
	return "EXPLORE"
}

// renderStatusLine: mode pill, location, position, flags on the left;
// pending input, book sync state and pending pushes on the right.
func (m Model) renderStatusLine() string {
	t := theme.Current
	pill := m.renderModePill()
	dim := lipgloss.NewStyle().Foreground(t.TextDim)
	var left []string
	switch {
	case m.floatExp && m.bk != nil:
		left = append(left, dim.Render("livros"))
	case m.floatExp:
		left = append(left, dim.Render(m.displayPath()))
		if len(m.items) > 0 {
			left = append(left, dim.Render(fmt.Sprintf("%d/%d", m.cursor+1, len(m.items))))
		}
		if m.filter != "" && !m.filterTyping {
			left = append(left, lipgloss.NewStyle().Foreground(t.Accent).Render("/"+m.filter))
		}
		if m.showHidden {
			left = append(left, dim.Render("hidden"))
		}
		if m.yanked != nil {
			left = append(left, dim.Render("yanked: "+truncate(m.yanked.Name, 20)))
		}
	case m.viewMode == ViewHome:
		left = append(left, dim.Render(m.bookName()))
	case m.viewMode == ViewFullNote:
		if m.ws != nil && m.ws.focus != nil {
			p := m.ws.focus
			e := p.buf.eng
			left = append(left, dim.Render(kindOf(p.buf.path).glyph()+" "+m.displayRel(p.buf.path)))
			switch p.view {
			case viewSource, viewSplit:
				c := e.Cursor()
				left = append(left, dim.Render(fmt.Sprintf("Ln %d Col %d", c.Line+1, c.Col+1)))
			case viewReader:
				d, h := m.readerDoc(p)
				c := d.eng.Cursor()
				pct := 100
				if n := len(d.lines) - h; n > 0 {
					pct = min(100, d.eng.Top*100/n)
				}
				left = append(left, dim.Render(fmt.Sprintf("Ln %d Col %d · %d%%", c.Line+1, c.Col+1, pct)))
			}
			if p.buf.dirty() {
				left = append(left, lipgloss.NewStyle().Foreground(t.Warning).Render("não salvo"))
			}
			if r := e.Recording(); r != 0 {
				left = append(left, lipgloss.NewStyle().Foreground(t.Warning).Render("gravando @"+string(r)))
			}
			if n := len(m.ws.leaves()); n > 1 {
				left = append(left, dim.Render(fmt.Sprintf("%d painéis", n)))
			}
			if m.zoomed() != nil {
				left = append(left, lipgloss.NewStyle().Foreground(t.Accent).Bold(true).Render("zoom"))
			}
			if fe := m.focusEngine(); fe != nil && fe != e {
				e = fe
			}
			if msg, isErr := e.Message(); msg != "" && m.statusMsg == "" {
				st := lipgloss.NewStyle().Foreground(t.Info)
				if isErr {
					st = st.Foreground(t.Error)
				}
				left = append(left, st.Render(truncate(msg, max(10, m.width/2))))
			}
		}
	}
	if m.statusMsg != "" {
		left = append(left, lipgloss.NewStyle().Foreground(t.Info).Render(m.statusMsg))
	}
	l := pill + " " + strings.Join(left, dim.Render(" · "))

	var right []string
	if p := m.pendingInput(); p != "" {
		right = append(right, lipgloss.NewStyle().Foreground(t.Warning).Bold(true).Render(p))
	}
	if m.viewMode == ViewFullNote && m.ws != nil && m.ws.focus != nil {
		pend := ""
		if m.winPending {
			pend = "⌃w"
		}
		if e := m.focusEngine(); e != nil {
			pend += e.Pending()
		}
		if pend != "" {
			right = append(right, lipgloss.NewStyle().Foreground(t.Warning).Bold(true).Render(pend))
		}
	}
	if seg := m.renderBookSegment(); seg != "" {
		right = append(right, seg)
	} else {
		right = append(right, dim.Render("local"))
	}
	r := strings.Join(right, "  ") + " "
	gap := m.width - lipgloss.Width(l) - lipgloss.Width(r)
	if gap < 1 {
		l = truncateToVisualWidth(l, max(0, m.width-lipgloss.Width(r)-1))
		gap = max(1, m.width-lipgloss.Width(l)-lipgloss.Width(r))
	}
	return padVisual(l+strings.Repeat(" ", gap)+r, m.width)
}

// renderModePill is the mode pill; on a mode change its width morphs
// from the old label's to the new one's (spring), the label clipped or
// padded inside it.
func (m Model) renderModePill() string {
	t := theme.Current
	label := " " + m.modeName() + " "
	if a, ok := m.mo.get(animPill); ok && a.active && m.mo.pillFrom != "" {
		from := runewidth.StringWidth(" " + m.mo.pillFrom + " ")
		w := max(3, lerp(from, runewidth.StringWidth(label), a.value(0, 1.15)))
		label = runewidth.FillRight(runewidth.Truncate(label, w, ""), w)
	}
	return lipgloss.NewStyle().Foreground(t.Background).Background(m.modeTint()).Bold(true).Render(label)
}

// modeColor is a vim mode's colour (DESIGN-LANGUAGE §6.7): normal =
// primary, insert = accent, visual = secondary, replace = error. The
// pill, the editor's left edge and the current line number take it.
func modeColor(md vim.Mode) lipgloss.Color {
	t := theme.Current
	switch {
	case md == vim.ModeInsert:
		return t.Accent
	case md == vim.ModeReplace:
		return t.Error
	case md.IsVisual():
		return t.Secondary
	}
	return t.Primary
}

// modeTint is the focused editor's mode colour; primary elsewhere.
func (m Model) modeTint() lipgloss.Color {
	switch m.legendContext() {
	case CtxEditor, CtxInsert, CtxVisual, CtxCmdline:
		if e := m.focusEngine(); e != nil {
			return modeColor(e.Mode())
		}
	}
	return theme.Current.Primary
}

// displayRel shows a path relative to the book root as "~/…".
func (m Model) displayRel(p string) string {
	r, err := filepath.Rel(m.rootDir, p)
	if err != nil || strings.HasPrefix(r, "..") {
		return p
	}
	return "~/" + filepath.ToSlash(r)
}

// fitHeight pads or cuts s to exactly h lines.
func fitHeight(s string, h int) string {
	lines := strings.Split(s, "\n")
	if len(lines) > h {
		lines = lines[:h]
	}
	for len(lines) < h {
		lines = append(lines, "")
	}
	return strings.Join(lines, "\n")
}

// cutVisual returns the part of an ANSI string between visual columns
// [from, to). Escape sequences before `from` are kept so styles carry
// over; wide runes that straddle an edge are replaced by spaces.
func cutVisual(s string, from, to int) string {
	var out strings.Builder
	col := 0
	inEsc := false
	for _, r := range s {
		if r == '\x1b' {
			inEsc = true
			out.WriteRune(r)
			continue
		}
		if inEsc {
			out.WriteRune(r)
			if (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') {
				inEsc = false
			}
			continue
		}
		w := runewidth.RuneWidth(r)
		switch {
		case col >= to:
		case col >= from && col+w <= to:
			out.WriteRune(r)
		case col+w > from && col < to:
			// straddles an edge
			for i := max(col, from); i < min(col+w, to); i++ {
				out.WriteByte(' ')
			}
		}
		col += w
	}
	return out.String()
}

// overlayBox draws box over base with its top-left at (x, y).
func overlayBox(base, box string, x, y, width int) string {
	lines := strings.Split(base, "\n")
	for i, bl := range strings.Split(box, "\n") {
		row := y + i
		if row < 0 || row >= len(lines) {
			continue
		}
		bw := lipgloss.Width(bl)
		line := padVisual(lines[row], width)
		lines[row] = cutVisual(line, 0, x) + "\x1b[0m" + bl + "\x1b[0m" + cutVisual(line, x+bw, width)
	}
	return strings.Join(lines, "\n")
}

// overlayCenter centres box horizontally, near the top third.
func (m Model) overlayCenter(base, box string, top bool) string {
	bw, bh := lipgloss.Width(box), lipgloss.Height(box)
	x := max(0, (m.width-bw)/2)
	y := max(0, (m.contentHeight()-bh)/2)
	if top {
		y = min(y, 2)
	}
	return overlayBox(base, box, x, y, m.width)
}

// focusEngine is the engine that takes the focused pane's keys: the
// editor's, or the read-only prose engine of the reader / focused
// preview. nil outside the notes workspace and for board / canvas.
func (m Model) focusEngine() *vim.Engine {
	if m.viewMode != ViewFullNote || m.ws == nil || m.ws.focus == nil {
		return nil
	}
	p := m.ws.focus
	switch p.view {
	case viewReader:
		d, _ := m.readerDoc(p)
		return d.eng
	case viewSplit:
		if p.previewFocus {
			d, _ := m.previewDoc(p)
			return d.eng
		}
		return p.buf.eng
	case viewSource:
		return p.buf.eng
	}
	return nil
}

// proseCtx is the legend context of a read-only prose engine.
func proseCtx(e *vim.Engine, normal LegendContext) LegendContext {
	switch {
	case e == nil:
		return normal
	case e.Mode() == vim.ModeCmdline:
		return CtxCmdline
	case e.Mode().IsVisual():
		return CtxReadVisual
	}
	return normal
}
