package ui

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/vinizap/lumi/tui-client/books"
	"github.com/vinizap/lumi/tui-client/feed"
	"github.com/vinizap/lumi/tui-client/filesystem"
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
		cmds = append(cmds, m.loadItems)
		if m.showNav {
			cmds = append(cmds, m.loadNavItems)
		}
		if m.fullNote != nil {
			if reloaded, err := filesystem.ReadNote(m.fullNote.Path); err == nil {
				m.openNote(reloaded)
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

// openBooksPicker shows the `gb` picker with the cursor on the active book.
func (m *Model) openBooksPicker() {
	if m.bookMgr == nil {
		return
	}
	m.showBooks = true
	m.bookCursor = 0
	for i, b := range m.bookMgr.Registry() {
		if b.ID == m.activeBookID {
			m.bookCursor = i
		}
	}
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
	m.showBooks = false
	if b.Path == "" {
		cmd := m.showToast(fmt.Sprintf("%q não tem pasta neste aparelho — clone com: lumi vault clone %s/<slug>", b.Name, b.ServerURL), ToastInfo, 6*time.Second)
		return m, cmd
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

func (m Model) updateBooksPicker(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	list := m.bookMgr.Registry()
	key := msg.String()
	switch key {
	case "esc", "q", "ctrl+c":
		m.showBooks = false
	case "j", "down":
		if m.bookCursor < len(list)-1 {
			m.bookCursor++
		}
	case "k", "up":
		if m.bookCursor > 0 {
			m.bookCursor--
		}
	case "J", "K":
		if m.bookCursor < len(list) {
			delta := 1
			if key == "K" {
				delta = -1
			}
			if err := m.bookMgr.Move(list[m.bookCursor].ID, delta); err != nil {
				return m, m.showToast("Livro: "+err.Error(), ToastError, 4*time.Second)
			}
			if n := m.bookCursor + delta; n >= 0 && n < len(list) {
				m.bookCursor = n
			}
		}
	case "enter", "l":
		return m.switchToBook(m.bookCursor)
	default:
		if len(key) == 1 && key[0] >= '1' && key[0] <= '9' {
			return m.switchToBook(int(key[0] - '1'))
		}
	}
	return m, nil
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
	seg := lipgloss.NewStyle().Foreground(stateColor(st)).Render(st.Glyph() + " " + st.Label())
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

func (m Model) renderBooksPicker() string {
	list := m.bookMgr.Registry()
	dim := lipgloss.NewStyle().Foreground(theme.Current.TextDim)
	text := lipgloss.NewStyle().Foreground(theme.Current.Text)
	var s strings.Builder
	if len(list) == 0 {
		s.WriteString(text.Render("Nenhum livro ainda."))
		s.WriteString("\n\n")
		s.WriteString(dim.Render("lumi books link <pasta>  ·  lumi login <servidor> + lumi books refresh"))
		return m.modalBox("livros", s.String(), 64)
	}
	for i, b := range list {
		v, _ := m.bookMgr.View(b.ID)
		cursor := "  "
		if i == m.bookCursor {
			cursor = "▸ "
		}
		num := "  "
		if i < 9 {
			num = fmt.Sprintf("%d ", i+1)
		}
		where := "local"
		if b.IsBound() {
			where = b.Account + "@" + books.Host(b.ServerURL)
		}
		if b.Path == "" {
			where += "  (sem pasta)"
		}
		dot := lipgloss.NewStyle().Foreground(stateColor(v.Status.State)).Render(v.Status.State.Glyph())
		name := text.Render(padRight(truncate(b.Name, 24), 24))
		if b.ID == m.activeBookID {
			name = lipgloss.NewStyle().Bold(true).Foreground(theme.Current.Primary).Render(padRight(truncate(b.Name, 24), 24))
		}
		row := cursor + dim.Render(num) + dot + " " + name + "  " + dim.Render(truncate(where, 34))
		if i == m.bookCursor {
			row = lipgloss.NewStyle().Background(theme.Current.SelectedBg).Render(row)
		}
		s.WriteString(row + "\n")
	}
	s.WriteString("\n")
	s.WriteString(dim.Render("j/k mover · enter/1-9 abrir · J/K reordenar · esc fechar"))
	return m.modalBox("livros", s.String(), 76)
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
