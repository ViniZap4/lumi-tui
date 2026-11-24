package ui

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/vinizap/lumi/tui-client/account"
	"github.com/vinizap/lumi/tui-client/bookclone"
	"github.com/vinizap/lumi/tui-client/books"
	"github.com/vinizap/lumi/tui-client/theme"
)

// Clone in place (SPEC-LIVROS §5 "Baixar"): a server book with no folder
// on this device is cloned from inside the TUI — `c` / ⏎ on it in the
// books level (or g1–g9, the palette, [ ]) opens a small card with a
// proposed folder (`<books_dir>/<slug>`), Enter clones through the same
// code path as `lumi vault clone` (bookclone.Run: vaults.yaml, books.yaml,
// sync bases), with live progress, then opens the book. `L` binds the
// book to a folder that already exists instead. When the account needs a
// sign-in, the sign-in card comes first and the flow continues after it.

type cloneKind int

const (
	cloneKindClone cloneKind = iota
	cloneKindLink
)

type cloneFlow struct {
	kind    cloneKind
	book    books.Book
	input   string
	err     string
	running bool
	// progress
	phase       string // "conectando", "baixando", "registrando"
	done, total int
	current     string
	cancel      context.CancelFunc
	cancelling  bool
	ch          chan tea.Msg
	dest        string // validated absolute destination
	retriedAuth bool
}

// cloneProgressMsg / cloneDoneMsg come from the clone goroutine.
type cloneProgressMsg struct {
	ch          chan tea.Msg
	phase       string
	done, total int
	current     string
}

type cloneDoneMsg struct {
	ch  chan tea.Msg
	res bookclone.Result
	err error
}

// cloneTimeout bounds a whole clone.
var cloneTimeout = 10 * time.Minute

// spinnerWanted: some card shows work in progress.
func (m Model) spinnerWanted() bool {
	if m.clone != nil && m.clone.running {
		return true
	}
	if s := m.acct; s != nil {
		if s.busy {
			return true
		}
		if f := s.form; f != nil && (f.checking || f.busy) {
			return true
		}
		for _, st := range s.servers {
			if st.checking {
				return true
			}
		}
	}
	return false
}

// bookNeedsSignIn: the book's account is missing, signed out, expired or
// the feed reports "precisa entrar".
func (m Model) bookNeedsSignIn(b books.Book) bool {
	if m.bookMgr != nil {
		if v, ok := m.bookMgr.View(b.ID); ok && v.Status.State == books.StateSignIn {
			return true
		}
	}
	_, err := bookAccount(b)
	return err != nil
}

// bookAccount resolves the signed-in account of a bound book.
func bookAccount(b books.Book) (*account.Account, error) {
	f, err := account.Load()
	if err != nil {
		return nil, err
	}
	a, err := f.Resolve(b.ServerURL, b.Account)
	if err != nil {
		return nil, err
	}
	if a.Token == "" || a.IsExpired() {
		return nil, account.ErrNoAccount
	}
	return a, nil
}

// beginBookFlow starts clone (or link) for a book without a folder,
// signing in first when needed.
func (m Model) beginBookFlow(b books.Book, kind cloneKind) (tea.Model, tea.Cmd) {
	if !b.IsBound() {
		return m, m.showToast(fmt.Sprintf("%q é um livro local — não há o que clonar", b.Name), ToastInfo, 3*time.Second)
	}
	if b.VaultID == "" {
		return m, m.showToast(fmt.Sprintf("%q tem uma ligação incompleta — religue com: lumi books link <pasta> --server %s", b.Name, b.ServerURL), ToastError, 6*time.Second)
	}
	if b.Path != "" {
		return m, m.showToast(fmt.Sprintf("%q já tem pasta: %s", b.Name, b.Path), ToastInfo, 3*time.Second)
	}
	if m.bookNeedsSignIn(b) {
		m.openSignInFor(b, kind)
		return m, m.acct.form.checkNow()
	}
	m.openCloneCard(b, kind)
	return m, nil
}

// openCloneCard shows the prompt with the default destination.
func (m *Model) openCloneCard(b books.Book, kind cloneKind) {
	var list []books.Book
	if m.bookMgr != nil {
		list = m.bookMgr.Registry()
	}
	f := &cloneFlow{kind: kind, book: b}
	if kind == cloneKindClone {
		f.input = bookclone.DefaultDest(m.noteCfg().BooksDir, b.Name, list)
	} else {
		f.input = "~/"
	}
	m.clone = f
}

func (m Model) registryList() []books.Book {
	if m.bookMgr != nil {
		return m.bookMgr.Registry()
	}
	if st, err := books.Open(); err == nil {
		return st.Registry.Books
	}
	return nil
}

// updateRegistry goes through the feed manager when there is one, so
// its in-memory registry and books.yaml stay the same.
func (m Model) updateRegistry(fn func(reg *books.Registry) bool) error {
	if m.bookMgr != nil {
		return m.bookMgr.UpdateRegistry(fn)
	}
	st, err := books.Open()
	if err != nil {
		return err
	}
	if !fn(st.Registry) {
		return nil
	}
	return st.Save()
}

func (m Model) updateClone(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	f := m.clone
	key := msg.String()
	if f.running {
		switch key {
		case "esc", "ctrl+c", "q":
			if f.cancel != nil && !f.cancelling {
				f.cancelling = true
				f.cancel()
			}
		}
		return m, nil
	}
	switch key {
	case "esc":
		m.clone = nil
		return m, nil
	case "ctrl+c":
		return m, tea.Quit
	case "enter":
		return m.submitClone()
	case "tab":
		// complete the proposed folder name under the books dir
		if f.kind == cloneKindClone && f.input == "" {
			f.input = bookclone.DefaultDest(m.noteCfg().BooksDir, f.book.Name, m.registryList())
		}
		return m, nil
	}
	if v, ok := editLine(f.input, msg); ok {
		f.input = v
		f.err = ""
	}
	return m, nil
}

func (m Model) submitClone() (tea.Model, tea.Cmd) {
	f := m.clone
	list := m.registryList()
	if f.kind == cloneKindLink {
		abs, err := bookclone.ValidateLinkDest(f.input, list, f.book.ID)
		if err != nil {
			f.err = err.Error()
			return m, nil
		}
		if err := bookclone.LinkFolder(m.updateRegistry, f.book, abs); err != nil {
			f.err = err.Error()
			return m, nil
		}
		m.clone = nil
		cmd := m.showToast(fmt.Sprintf("%q ligado a %s", f.book.Name, books.EncodeHome(abs)), ToastSuccess, 3*time.Second)
		mm, c2 := m.openBookByVault(f.book)
		return mm, tea.Batch(cmd, c2)
	}
	abs, err := bookclone.ValidateCloneDest(f.input, list)
	if err != nil {
		f.err = err.Error()
		return m, nil
	}
	f.dest = abs
	return m.runClone()
}

// runClone starts the clone goroutine for m.clone (dest validated).
func (m Model) runClone() (tea.Model, tea.Cmd) {
	f := m.clone
	acc, err := bookAccount(f.book)
	if err != nil {
		m.openSignInFor(f.book, f.kind)
		m.acct.then.dest = f.input
		return m, m.acct.form.checkNow()
	}
	client, err := account.NewClient(f.book.ServerURL)
	if err != nil {
		f.err = err.Error()
		return m, nil
	}
	client.SetToken(acc.Token)
	ctx, cancel := context.WithTimeout(context.Background(), cloneTimeout)
	ch := make(chan tea.Msg, 16)
	f.running, f.cancelling, f.err = true, false, ""
	f.cancel, f.ch = cancel, ch
	f.phase, f.done, f.total, f.current = "conectando", 0, 0, ""
	send := func(msg tea.Msg) {
		select {
		case ch <- msg:
		case <-ctx.Done():
		}
	}
	opts := bookclone.Options{
		Client: client, Server: f.book.ServerURL, Account: acc.Username,
		VaultID: f.book.VaultID, Path: f.dest,
		OnStart: func(_ account.RemoteVault, total int) {
			send(cloneProgressMsg{ch: ch, phase: "baixando", total: total})
		},
		OnNote: func(done, total int, rel string, _ bool) {
			ph := "baixando"
			if done == total {
				ph = "registrando"
			}
			send(cloneProgressMsg{ch: ch, phase: ph, done: done, total: total, current: rel})
		},
		UpdateBooks: m.updateRegistry,
	}
	go func() {
		defer cancel()
		res, err := bookclone.Run(ctx, opts)
		ch <- cloneDoneMsg{ch: ch, res: res, err: err}
	}()
	return m, waitClone(ch)
}

// waitClone delivers the next message from the clone goroutine.
func waitClone(ch chan tea.Msg) tea.Cmd {
	return func() tea.Msg { return <-ch }
}

func (m Model) onCloneProgress(msg cloneProgressMsg) (tea.Model, tea.Cmd) {
	if f := m.clone; f != nil && f.ch == msg.ch {
		f.phase, f.done, f.total, f.current = msg.phase, msg.done, msg.total, msg.current
	}
	return m, waitClone(msg.ch)
}

func (m Model) onCloneDone(msg cloneDoneMsg) (tea.Model, tea.Cmd) {
	f := m.clone
	if f == nil || f.ch != msg.ch {
		return m, nil
	}
	f.running, f.cancel = false, nil
	if msg.err == nil {
		m.clone = nil
		toast := fmt.Sprintf("%q clonado — %s em %s", f.book.Name, notesLabel(msg.res.Written), books.EncodeHome(f.dest))
		level := ToastSuccess
		if msg.res.BooksErr != nil {
			toast = "books.yaml não foi atualizado: " + msg.res.BooksErr.Error()
			level = ToastError
		}
		cmd := m.showToast(toast, level, 4*time.Second)
		mm, c2 := m.openBookByVault(f.book)
		return mm, tea.Batch(cmd, c2)
	}
	if bookclone.StageOf(msg.err) != bookclone.StageVaultsYAML {
		bookclone.Cleanup(msg.res)
	}
	switch {
	case errors.Is(msg.err, context.Canceled):
		m.clone = nil
		return m, m.showToast("clone cancelado — nada foi registrado", ToastInfo, 3*time.Second)
	case errors.Is(msg.err, account.ErrUnauthorized) && !f.retriedAuth:
		// the session died: sign in, then clone again into the same folder
		dest := f.input
		m.openSignInFor(f.book, cloneKindClone)
		m.acct.then.dest = dest
		m.acct.form.err = "sua sessão expirou — entre de novo para clonar"
		return m, m.acct.form.checkNow()
	}
	f.err = cloneErrText(msg.err)
	return m, nil
}

func notesLabel(n int) string {
	if n == 1 {
		return "1 nota"
	}
	return fmt.Sprintf("%d notas", n)
}

// cloneErrText is the pt-BR reason a clone failed.
func cloneErrText(err error) string {
	var api *account.APIError
	switch {
	case bookclone.StageOf(err) == bookclone.StageNotFound:
		return "este livro não está mais visível para esta conta"
	case errors.Is(err, account.ErrUnauthorized):
		return "o servidor recusou a sessão — entre de novo (ga)"
	case errors.Is(err, context.DeadlineExceeded):
		return "o clone demorou demais — tente de novo"
	case errors.As(err, &api) && api.Status == http.StatusConflict:
		return "uma nota mudou de linhagem (crdt_epoch) durante o download — tente de novo"
	case errors.As(err, &api) && api.Status == http.StatusForbidden:
		return "sem permissão para ler este livro"
	case bookclone.StageOf(err) == bookclone.StageMkdir:
		return "não foi possível criar a pasta: " + errors.Unwrap(err).Error()
	case bookclone.StageOf(err) == bookclone.StageVaultsYAML:
		return "notas baixadas, mas vaults.yaml falhou: " + errors.Unwrap(err).Error()
	}
	if t := netErrText(err); t != "" {
		return t
	}
	return err.Error()
}

// netErrText names network / TLS failures; "" for anything else.
func netErrText(err error) string {
	msg := err.Error()
	switch {
	case strings.Contains(msg, "x509:") || strings.Contains(msg, "tls:") || strings.Contains(msg, "certificate"):
		return "erro de TLS: certificado do servidor não confiável"
	}
	var ne net.Error
	var oe *net.OpError
	var de *net.DNSError
	switch {
	case errors.As(err, &de):
		return "servidor não encontrado (DNS)"
	case errors.As(err, &oe):
		return "servidor inacessível — sem conexão"
	case errors.As(err, &ne) && ne.Timeout():
		return "o servidor não respondeu a tempo"
	}
	return ""
}

// openBookByVault switches to the book bound to b's vault (its id may
// have changed when the clone registered it).
func (m Model) openBookByVault(b books.Book) (tea.Model, tea.Cmd) {
	if m.bookMgr == nil {
		return m, nil
	}
	for i, x := range m.bookMgr.Registry() {
		if x.ID == b.ID || (b.VaultID != "" && x.VaultID == b.VaultID) {
			if x.Path == "" {
				return m, nil
			}
			return m.switchToBook(i)
		}
	}
	return m, nil
}

// renderCloneCard is the prompt / progress card.
func (m Model) cloneCard() card {
	f := m.clone
	t := theme.Current
	dim := lipgloss.NewStyle().Foreground(t.TextDim)
	text := lipgloss.NewStyle().Foreground(t.Text)
	title := lipgloss.NewStyle().Bold(true).Foreground(primaryColor)
	w := m.cardWidth(64)
	var c cardBuilder
	host := books.Host(f.book.ServerURL)
	switch {
	case f.running:
		c.line(title.Render("clonando " + quoteName(f.book.Name)))
		c.line(dim.Render(f.book.Account + "@" + host + "  →  " + books.EncodeHome(f.dest)))
		c.nl()
		label := f.phase
		if f.cancelling {
			label = "cancelando"
		}
		count := ""
		if f.total > 0 {
			count = fmt.Sprintf("  %d/%d notas", f.done, f.total)
		}
		c.line(m.spinner() + " " + text.Render(label) + dim.Render(count))
		pct := ""
		if f.total > 0 {
			pct = fmt.Sprintf(" %3d%%", f.done*100/f.total)
		}
		c.line(progressBar(f.done, max(1, f.total), w-len(pct)) + dim.Render(pct))
		c.line(dim.Render(truncate(f.current, w)))
		c.nl()
		c.button("Esc", "cancelar", "esc")
	default:
		verb := "clonar"
		hint := "pasta nova ou vazia — o livro abre ao terminar"
		if f.kind == cloneKindLink {
			verb = "ligar pasta"
			hint = "uma pasta que já existe — as notas do servidor chegam pelo feed"
		}
		c.line(title.Render(verb + " " + quoteName(f.book.Name)))
		c.line(dim.Render(f.book.Account + "@" + host))
		c.nl()
		c.line(dim.Render("pasta"))
		c.line(lipgloss.NewStyle().Foreground(t.Accent).Render("▸ ") + text.Underline(true).Render(padRight(f.input+"▏", w-2)))
		if f.err != "" {
			c.line(lipgloss.NewStyle().Foreground(t.Error).Render("✕ " + truncate(f.err, w-2)))
		} else {
			c.line(dim.Render(truncate(hint, w)))
		}
		c.nl()
		c.button("⏎", verb, "enter")
		c.gap(3)
		c.button("Esc", "cancelar", "esc")
		if f.kind == cloneKindClone {
			c.gap(3)
			c.text(dim.Render("⌃u limpa · ~ = sua pasta pessoal"))
		}
	}
	return m.buildCard(&c, w)
}

func quoteName(s string) string { return "“" + s + "”" }

// cloneMouse: buttons on the card; a click outside does nothing (the
// prompt is modal).
func (m Model) cloneMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	if msg.Action != tea.MouseActionPress || msg.Button != tea.MouseButtonLeft {
		return m, nil
	}
	if k := m.cloneCard().keyAt(msg.X, msg.Y); k != "" {
		return m.updateClone(pressCardKey(k))
	}
	return m, nil
}
