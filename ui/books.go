package ui

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/vinizap/lumi/tui-client/books"
	"github.com/vinizap/lumi/tui-client/feed"
	"github.com/vinizap/lumi/tui-client/theme"
)

// booksChangedMsg fires whenever the feed manager reports a change.
type booksChangedMsg struct{}

// WithBooks attaches the book registry/feed manager. activeID is the book
// whose folder this model is showing ("" when the folder is not a
// registered book).
func (m Model) WithBooks(mgr *feed.Manager, activeID string) Model {
	m.bookMgr = mgr
	m.activeBookID = activeID
	if mgr != nil {
		if v, ok := mgr.View(activeID); ok {
			m.lastMirrorGen = v.MirrorGen
		}
	}
	return m
}

// SwitchTarget is the folder of the book the user switched to, or "" when
// the program ended normally.
func (m Model) SwitchTarget() string { return m.switchTo }

func (m Model) waitForBooks() tea.Msg {
	if m.bookMgr == nil {
		return nil
	}
	<-m.bookMgr.Notify()
	return booksChangedMsg{}
}

func (m Model) onBooksChanged() (tea.Model, tea.Cmd) {
	cmds := []tea.Cmd{m.waitForBooks}
	for _, n := range m.bookMgr.PopNotices() {
		cmds = append(cmds, m.showToast(n, ToastInfo, 5*time.Second))
	}
	if v, ok := m.bookMgr.View(m.activeBookID); ok && v.MirrorGen != m.lastMirrorGen {
		// The feed pulled server changes into this folder.
		m.lastMirrorGen = v.MirrorGen
		m.mo.to(animPulse, 1, 0) // the sync dot pulses
		cmds = append(cmds, m.loadItems, m.loadRecent)
		if m.showNav {
			cmds = append(cmds, m.loadNavItems)
		}
		if m.ws != nil {
			for path := range m.ws.bufs {
				m.reloadBuffer(path)
			}
		}
	}
	return m, tea.Batch(cmds...)
}

// localEdit tells the feed manager a note in the active book changed on
// disk, so it is pushed (3-way) right away when online, or counted as
// pending when not.
func (m *Model) localEdit() {
	if m.bookMgr != nil && m.activeBookID != "" {
		go m.bookMgr.LocalEdit(m.activeBookID)
	}
}

// pendingLabel is "1 pendente" / "N pendentes".
func pendingLabel(n int) string {
	if n == 1 {
		return "1 pendente"
	}
	return fmt.Sprintf("%d pendentes", n)
}

// switchToBook opens book n (0-based picker index).
func (m Model) switchToBook(idx int) (tea.Model, tea.Cmd) {
	if m.bookMgr == nil {
		return m, nil
	}
	list := m.bookMgr.Registry()
	if idx < 0 || idx >= len(list) {
		return m, nil
	}
	b := list[idx]
	if b.Path == "" {
		// no folder on this device: clone it here (or link a folder)
		return m.beginBookFlow(b, cloneKindClone)
	}
	if b.ID == m.activeBookID {
		return m, nil
	}
	if err := m.bookMgr.SetActive(b.ID); err != nil {
		return m, m.showToast("Livro: "+err.Error(), ToastError, 4*time.Second)
	}
	m.switchTo = b.AbsPath()
	return m, tea.Quit
}

func stateColor(s books.State) lipgloss.Color {
	switch s {
	case books.StateSignIn:
		return theme.Current.Warning
	case books.StateError:
		return theme.Current.Error
	case books.StateOffline, books.StateLocal:
		return theme.Current.TextDim
	case books.StateSyncing:
		return theme.Current.Info
	default:
		return theme.Current.Accent
	}
}

// renderBookSegment is the status-line segment ("● atualizado").
func (m Model) renderBookSegment() string {
	if m.bookMgr == nil {
		return ""
	}
	v, ok := m.bookMgr.View(m.activeBookID)
	if !ok {
		return ""
	}
	st := v.Status.State
	seg := m.syncGlyph(st) + lipgloss.NewStyle().Foreground(stateColor(st)).Render(" "+st.Label())
	if n := len(v.Pending); n > 0 {
		seg += lipgloss.NewStyle().Foreground(theme.Current.Warning).Render(" · " + pendingLabel(n))
	}
	return seg + lipgloss.NewStyle().Foreground(mutedColor).Render("  "+truncate(v.Book.Name, 24))
}

func (m Model) modalBox(title, body string, width int) string {
	var s strings.Builder
	s.WriteString(lipgloss.NewStyle().Bold(true).Foreground(theme.Current.Primary).Render(title))
	s.WriteString("\n\n")
	s.WriteString(body)
	modal := lipgloss.NewStyle().
		Width(width).
		Border(lipgloss.RoundedBorder()).
		BorderForeground(theme.Current.Border).
		Background(theme.Current.Background).
		Padding(1, 2).
		Render(s.String())
	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, modal,
		lipgloss.WithWhitespaceChars(" "),
		lipgloss.WithWhitespaceForeground(theme.Current.OverlayBg),
		lipgloss.WithWhitespaceBackground(theme.Current.OverlayBg))
}

func (m Model) renderBookDetails() string {
	dim := lipgloss.NewStyle().Foreground(theme.Current.TextDim)
	text := lipgloss.NewStyle().Foreground(theme.Current.Text)
	v, ok := m.bookMgr.View(m.activeBookID)
	if !ok {
		return m.modalBox("estado do livro", text.Render("Esta pasta não é um livro registrado.\n\n")+
			dim.Render("lumi books link <pasta>"), 60)
	}
	b := v.Book
	row := func(k, val string) string { return dim.Render(padRight(k, 12)) + text.Render(val) + "\n" }
	var s strings.Builder
	st := v.Status.State
	s.WriteString(row("estado", lipgloss.NewStyle().Foreground(stateColor(st)).Render(st.Glyph()+" "+st.Label())))
	if v.Status.Reason != "" {
		s.WriteString(row("motivo", v.Status.Reason))
	}
	if !b.IsBound() {
		s.WriteString(row("pasta", b.Path))
		return m.modalBox(b.Name, s.String(), 64)
	}
	s.WriteString(row("servidor", b.ServerURL))
	s.WriteString(row("conta", b.Account))
	if v.Role != "" {
		s.WriteString(row("papel", v.Role))
	}
	feedLine := v.Feed.String()
	if v.RetryIn > 0 && (v.Feed == books.FeedBackoff || v.Feed == books.FeedDisabled) {
		feedLine += fmt.Sprintf(" (nova tentativa em %s)", v.RetryIn.Round(time.Second))
	}
	s.WriteString(row("feed", feedLine))
	if v.FeedErr != "" {
		s.WriteString(row("", truncate(v.FeedErr, 48)))
	}
	if v.LastSynced.IsZero() {
		s.WriteString(row("sincronizou", "nunca"))
	} else {
		s.WriteString(row("sincronizou", "há "+humanSince(time.Since(v.LastSynced))))
	}
	s.WriteString(row("cursor/head", fmt.Sprintf("%d/%d", v.Cursor, v.Head)))
	for i, a := range b.Alternates {
		label := ""
		if i == 0 {
			label = "alternativas"
		}
		s.WriteString(row(label, a.Account+"@"+books.Host(a.ServerURL)))
	}
	if n := len(v.Pending); n > 0 {
		hint := "enviadas quando voltar a conexão"
		if st != books.StateOffline {
			hint = "enviando"
		}
		s.WriteString(row("pendentes", fmt.Sprintf("%s — %s", pendingLabel(n), hint)))
		for i, p := range v.Pending {
			if i == 3 {
				s.WriteString(row("", fmt.Sprintf("… e mais %d", n-3)))
				break
			}
			s.WriteString(row("", truncate(p, 48)))
		}
	}
	if len(v.Dirty) > 0 {
		s.WriteString(row("não enviadas", fmt.Sprintf("%d nota(s) — livro somente leitura", len(v.Dirty))))
	}
	for i, c := range v.Conflicts {
		label := ""
		if i == 0 {
			label = "conflitos"
		}
		s.WriteString(row(label, truncate(c.Path+" → sua versão em "+c.Copy, 48)))
	}
	for i, e := range v.Errors {
		label := ""
		if i == 0 {
			label = "erros"
		}
		s.WriteString(row(label, truncate(e, 48)))
	}
	s.WriteString("\n")
	switch st {
	case books.StateSignIn:
		s.WriteString(dim.Render("Entre em " + books.Host(b.ServerURL) + " para sincronizar: lumi login " + b.ServerURL))
	case books.StateError:
		if b.IncompleteBinding() {
			s.WriteString(dim.Render("[Religar] lumi books link " + b.Path + " --server " + b.ServerURL))
		} else {
			s.WriteString(dim.Render("[Remover da lista] lumi books unlink " + b.ID))
		}
	default:
		s.WriteString(dim.Render("qualquer tecla fecha"))
	}
	return m.modalBox(b.Name, s.String(), 72)
}

func humanSince(d time.Duration) string {
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%d s", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%d min", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%d h", int(d.Hours()))
	default:
		return fmt.Sprintf("%d dias", int(d.Hours()/24))
	}
}

// bkAction is one clickable action of the books-level preview.
type bkAction struct{ label, key string }

// bkPreviewActions are the selected book's actions: a book without a
// folder here offers clone in place and link-a-folder.
func bkPreviewActions(b books.Book) []bkAction {
	if b.IsBound() && b.Path == "" {
		return []bkAction{{"⏎ c clonar aqui", "c"}, {"L ligar pasta", "L"}, {"a contas", "a"}}
	}
	return []bkAction{{"⏎ abrir", "enter"}, {"J/K ordem", ""}, {"s detalhes", "s"}}
}

// bkPreviewActionLine renders the actions (" ⏎ abrir · J/K ordem · …").
func bkPreviewActionLine(b books.Book, width int) string {
	var parts []string
	for _, a := range bkPreviewActions(b) {
		parts = append(parts, a.label)
	}
	line := " " + strings.Join(parts, " · ")
	st := lipgloss.NewStyle().Foreground(theme.Current.TextDim)
	if b.IsBound() && b.Path == "" {
		st = lipgloss.NewStyle().Foreground(theme.Current.Accent)
	}
	return st.Render(truncate(line, max(4, width-1)))
}

// bkPreviewActionAt is the key of the action at x (relative to the
// preview column), "" for none.
func bkPreviewActionAt(b books.Book, x int) string {
	pos := 1
	for i, a := range bkPreviewActions(b) {
		if i > 0 {
			pos += 3
		}
		w := lipgloss.Width(a.label)
		if x >= pos && x < pos+w {
			return a.key
		}
		pos += w
	}
	return ""
}
