package ui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/vinizap/lumi/tui-client/account"
	"github.com/vinizap/lumi/tui-client/books"
	"github.com/vinizap/lumi/tui-client/config"
	"github.com/vinizap/lumi/tui-client/opener"
	"github.com/vinizap/lumi/tui-client/theme"
)

// Accounts and servers inside the TUI (SPEC-PARTE2 §9, SPEC-LIVROS §5, §8):
// list accounts, sign in (server URL + username + masked password), link
// from Lumi.app, sign out of this TUI or of the whole Mac, remove an
// account or a whole server, set a server's web address. The account list
// is shared with Lumi.app (accounts.yaml v3); this TUI's tokens live in
// tokens.yaml (0600) and are never rendered; the password lives only in
// the form until it is sent.

type accountsScreen struct {
	list    []account.Account
	cursor  int
	form    *loginForm
	confirm string // "signout", "signoutmac", "remove", "server"
	origin  *string
	busy    bool
	err     string
	info    string
}

type loginForm struct {
	field              int // 0 server, 1 user, 2 password
	server, user, pass string
}

// loginDoneMsg carries a finished sign-in (the token stays inside acct
// and goes straight to accounts.yaml).
type loginDoneMsg struct {
	acct account.Account
	err  error
}

type signOutDoneMsg struct{ server, user string }

// loginTimeout bounds a sign-in request.
var loginTimeout = 30 * time.Second

// linkTimeout bounds waiting for Lumi.app to approve a device link.
var linkTimeout = 2 * time.Minute

// AccountsChangedMsg reports that another process (Lumi.app, another
// TUI) changed the shared account registry or this TUI's sessions. Sent
// by the account sync loop in package main.
type AccountsChangedMsg struct{ Toast string }

func (m Model) onAccountsChanged(msg AccountsChangedMsg) (tea.Model, tea.Cmd) {
	if m.acct != nil && !m.acct.busy && m.acct.form == nil {
		m.reloadAccounts()
	}
	if msg.Toast != "" {
		return m, m.showToast(msg.Toast, ToastInfo, 4*time.Second)
	}
	return m, nil
}

// appAlive reports whether Lumi.app is running on this machine.
func appAlive() bool {
	ch, err := account.NewLinkChannel()
	return err == nil && ch.PeerAlive(account.LinkClientApple)
}

func (m *Model) openAccounts() {
	m.acct = &accountsScreen{}
	m.reloadAccounts()
}

func (m *Model) reloadAccounts() {
	if f, err := account.Load(); err == nil {
		m.acct.list = f.Accounts
	} else {
		m.acct.err = "accounts.yaml: " + err.Error()
	}
	m.acct.cursor = clamp(m.acct.cursor, 0, max(0, len(m.acct.list)-1))
}

func (s *accountsScreen) current() (account.Account, bool) {
	if s.cursor < len(s.list) {
		return s.list[s.cursor], true
	}
	return account.Account{}, false
}

func (m Model) updateAccounts(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	s := m.acct
	key := msg.String()
	if s.busy {
		if key == "ctrl+c" {
			return m, tea.Quit
		}
		return m, nil
	}
	if s.form != nil {
		return m.updateLoginForm(msg)
	}
	if s.origin != nil {
		switch key {
		case "esc":
			s.origin = nil
		case "enter":
			a, _ := s.current()
			v := strings.TrimSpace(*s.origin)
			s.origin = nil
			cfg := config.Load()
			if v == "" {
				delete(cfg.WebOrigins, a.Server)
			} else {
				if _, err := opener.WebURL(v, "", ""); err != nil {
					s.err = "endereço web inválido (use https://…)"
					return m, nil
				}
				cfg.WebOrigins[a.Server] = strings.TrimRight(v, "/")
			}
			if err := cfg.Save(); err != nil {
				s.err = err.Error()
			} else {
				s.info, s.err = "endereço web salvo", ""
				m.cfg = cfg
				// Shared with Lumi.app through the registry (web_origin).
				if f, err := account.Load(); err == nil && !f.ReadOnly {
					for _, x := range f.ForServer(a.Server) {
						x.WebOrigin = strings.TrimRight(v, "/")
					}
					_ = f.Save()
				}
			}
		default:
			if v, ok := editLine(*s.origin, msg); ok {
				*s.origin = v
			}
		}
		return m, nil
	}
	if s.confirm != "" {
		kind := s.confirm
		s.confirm = ""
		if key != "y" && key != "Y" {
			s.info = "cancelado"
			return m, nil
		}
		a, ok := s.current()
		if !ok {
			return m, nil
		}
		return m.accountAction(kind, a)
	}
	s.err, s.info = "", ""
	switch key {
	case "esc", "q":
		m.acct = nil
	case "ctrl+c":
		return m, tea.Quit
	case "j", "down":
		s.cursor = clamp(s.cursor+1, 0, max(0, len(s.list)-1))
	case "k", "up":
		s.cursor = clamp(s.cursor-1, 0, max(0, len(s.list)-1))
	case "a", "n":
		s.form = &loginForm{}
	case "enter", "l":
		if a, ok := s.current(); ok {
			s.form = &loginForm{server: a.Server, user: a.Username, field: 2}
		} else {
			s.form = &loginForm{}
		}
	case "s":
		if a, ok := s.current(); ok && a.Token != "" {
			s.confirm = "signout"
		}
	case "S":
		if _, ok := s.current(); ok {
			s.confirm = "signoutmac"
		}
	case "L":
		if a, ok := s.current(); ok && a.Token == "" {
			if !appAlive() {
				s.err = "o Lumi.app não está aberto — abra o app ou entre com senha (⏎)"
				return m, nil
			}
			s.busy = true
			s.info = "aprove no Lumi.app… (Ctrl-C sai)"
			return m, linkFromAppCmd(a)
		}
	case "d", "x":
		if _, ok := s.current(); ok {
			s.confirm = "remove"
		}
	case "D":
		if _, ok := s.current(); ok {
			s.confirm = "server"
		}
	case "w":
		if a, ok := s.current(); ok {
			v := m.noteCfg().WebOrigins[a.Server]
			if v == "" {
				v, _ = opener.WebOrigin(a.Server, nil)
			}
			s.origin = &v
		}
	case "gb", "b":
		m.acct = nil
		m.openBooksLevel()
	}
	return m, nil
}

func (m Model) updateLoginForm(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	s := m.acct
	f := s.form
	switch msg.String() {
	case "esc":
		f.pass = ""
		s.form = nil
		return m, nil
	case "tab", "down":
		f.field = (f.field + 1) % 3
		return m, nil
	case "shift+tab", "up":
		f.field = (f.field + 2) % 3
		return m, nil
	case "enter":
		if f.field < 2 && (f.server == "" || f.user == "" || f.pass == "") {
			f.field++
			return m, nil
		}
		return m.submitLogin()
	case "ctrl+c":
		return m, tea.Quit
	}
	ptr := []*string{&f.server, &f.user, &f.pass}[f.field]
	if v, ok := editLine(*ptr, msg); ok {
		*ptr = v
	}
	return m, nil
}

func (m Model) submitLogin() (tea.Model, tea.Cmd) {
	s := m.acct
	f := s.form
	server, err := account.NormalizeServerURL(f.server)
	if err != nil {
		s.err = err.Error()
		f.field = 0
		return m, nil
	}
	if strings.HasPrefix(server, "http://") && !account.IsInsecureAllowed(server) {
		s.err = "http:// só é aceito para localhost, IPs privados e *.local — use https://"
		f.field = 0
		return m, nil
	}
	user := strings.TrimSpace(f.user)
	if user == "" || f.pass == "" {
		s.err = "usuário e senha são obrigatórios"
		return m, nil
	}
	pass := f.pass
	f.pass = "" // the password leaves the form now
	s.form = nil
	s.busy = true
	s.info = "entrando em " + books.Host(server) + "…"
	return m, func() tea.Msg {
		c, err := account.NewClient(server)
		if err != nil {
			return loginDoneMsg{err: err}
		}
		ctx, cancel := context.WithTimeout(context.Background(), loginTimeout)
		defer cancel()
		resp, err := c.Login(ctx, user, pass)
		if err != nil {
			return loginDoneMsg{err: err}
		}
		return loginDoneMsg{acct: account.Account{Server: c.BaseURL().String(), Username: resp.User.Username,
			UserID: resp.User.ID, DisplayName: resp.User.DisplayName, Token: resp.Token, ExpiresAt: resp.ExpiresAt,
			AddedAt: time.Now().UTC()}}
	}
}

func (m Model) onLoginDone(msg loginDoneMsg) (tea.Model, tea.Cmd) {
	if m.acct == nil {
		m.openAccounts()
	}
	s := m.acct
	s.busy = false
	if msg.err != nil {
		s.err, s.info = "falha ao entrar: "+loginError(msg.err), ""
		return m, nil
	}
	f, err := account.Load()
	if err != nil {
		s.err = "accounts.yaml: " + err.Error()
		return m, nil
	}
	f.Upsert(msg.acct)
	f.SetLinkOptOut(msg.acct.Server, msg.acct.Username, false)
	if err := f.Save(); err != nil {
		s.err = "accounts.yaml: " + err.Error()
		return m, nil
	}
	if m.bookMgr != nil {
		m.bookMgr.AddAccount(msg.acct)
	}
	m.reloadAccounts()
	for i, a := range s.list {
		if account.SameServer(a.Server, msg.acct.Server) && strings.EqualFold(a.Username, msg.acct.Username) {
			s.cursor = i
		}
	}
	s.info = "entrou como " + msg.acct.Username + " em " + books.Host(msg.acct.Server)
	return m, m.showToast(s.info, ToastSuccess, 3*time.Second)
}

func loginError(err error) string {
	switch {
	case errors.Is(err, account.ErrUnauthorized):
		return "usuário ou senha inválidos"
	case errors.Is(err, account.ErrLinkDenied):
		return "o Lumi.app não permitiu (o terminal não vai pedir de novo sozinho; L tenta outra vez)"
	case errors.Is(err, account.ErrLinkNoSession):
		return "o Lumi.app também não está conectado nesta conta"
	case errors.Is(err, account.ErrLinkNoPeer):
		return "o Lumi.app não está aberto"
	case errors.Is(err, account.ErrInvalidLink):
		return "o link expirou — tente de novo"
	case errors.Is(err, context.DeadlineExceeded):
		return "o Lumi.app não respondeu a tempo"
	}
	var apiErr *account.APIError
	if errors.As(err, &apiErr) {
		switch apiErr.Code {
		case "invalid_credentials":
			return "usuário ou senha inválidos"
		case "rate_limited":
			return "muitas tentativas — tente mais tarde"
		}
		if apiErr.Detail != "" {
			return apiErr.Detail
		}
		return apiErr.Code
	}
	return err.Error()
}

// accountAction signs out (this TUI, or the whole Mac), removes an
// account or a whole server (SPEC-LIVROS §8.5).
//
//   - signout: ends this TUI's session only; Lumi.app stays signed in and
//     the TUI won't ask it for a link on its own.
//   - signoutmac: logout scope=device (ends every linked session) and
//     signed_out_at in the shared registry, so Lumi.app drops its token.
//   - remove / server: the same as signoutmac, then the row goes.
func (m Model) accountAction(kind string, a account.Account) (tea.Model, tea.Cmd) {
	s := m.acct
	f, err := account.Load()
	if err != nil {
		s.err = err.Error()
		return m, nil
	}
	var targets []account.Account
	switch kind {
	case "server":
		for _, x := range f.ForServer(a.Server) {
			targets = append(targets, *x)
		}
	default:
		if x := f.Find(a.Server, a.Username); x != nil {
			targets = []account.Account{*x}
		}
	}
	now := time.Now().UTC()
	var cmds []tea.Cmd
	for _, t := range targets {
		if t.Token != "" {
			cmds = append(cmds, logoutCmd(t, kind != "signout"))
		}
		switch kind {
		case "signout":
			if x := f.Find(t.Server, t.Username); x != nil {
				x.Token = ""
			}
			f.SetLinkOptOut(t.Server, t.Username, true)
		case "signoutmac":
			f.SignOutMac(t.Server, t.Username, now)
		default:
			f.SignOutMac(t.Server, t.Username, now)
			f.Remove(t.Server, t.Username)
		}
		if m.bookMgr != nil && kind != "signout" && kind != "signoutmac" {
			m.bookMgr.RemoveAccount(t.Server, t.Username)
		}
	}
	if err := f.Save(); err != nil {
		s.err = err.Error()
		return m, nil
	}
	m.reloadAccounts()
	switch kind {
	case "signout":
		s.info = "saiu do terminal em " + books.Host(a.Server) + " (" + a.Username + ") — o Lumi.app continua conectado"
	case "signoutmac":
		s.info = "saiu de " + books.Host(a.Server) + " (" + a.Username + ") neste Mac"
	case "server":
		s.info = "servidor " + books.Host(a.Server) + " removido"
	default:
		s.info = "conta removida"
	}
	return m, tea.Batch(cmds...)
}

// logoutCmd revokes the session server-side (best effort). device=true
// also ends every session linked to it (Lumi.app's, when linked).
func logoutCmd(a account.Account, device bool) tea.Cmd {
	return func() tea.Msg {
		c, err := account.NewClient(a.Server)
		if err != nil {
			return nil
		}
		c.SetToken(a.Token)
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if device {
			_ = c.LogoutDevice(ctx)
		} else {
			_ = c.Logout(ctx)
		}
		return signOutDoneMsg{server: a.Server, user: a.Username}
	}
}

// linkFromAppCmd asks Lumi.app for a session (device link, §8.4) and
// stores it; the result arrives as a loginDoneMsg.
func linkFromAppCmd(a account.Account) tea.Cmd {
	return func() tea.Msg {
		ch, err := account.NewLinkChannel()
		if err != nil {
			return loginDoneMsg{err: err}
		}
		ctx, cancel := context.WithTimeout(context.Background(), linkTimeout)
		defer cancel()
		got, err := account.LinkAndSave(ctx, ch, a.Server, a.Username, 0, nil)
		if err != nil {
			return loginDoneMsg{err: err}
		}
		return loginDoneMsg{acct: *got}
	}
}

func (m Model) renderAccounts() string {
	s := m.acct
	t := theme.Current
	dim := lipgloss.NewStyle().Foreground(t.TextDim)
	text := lipgloss.NewStyle().Foreground(t.Text)
	var b strings.Builder
	if s.form != nil {
		f := s.form
		field := func(i int, label, val string) {
			cur := "  "
			st := text
			if f.field == i {
				cur = lipgloss.NewStyle().Foreground(t.Accent).Render("▸ ")
				st = st.Underline(true)
				val += "▏"
			}
			b.WriteString(cur + dim.Render(padRight(label, 10)) + st.Render(padRight(val, 40)) + "\n")
		}
		field(0, "servidor", f.server)
		field(1, "usuário", f.user)
		field(2, "senha", strings.Repeat("•", len([]rune(f.pass))))
		b.WriteString("\n")
		if strings.HasPrefix(strings.TrimSpace(f.server), "http://") {
			b.WriteString(lipgloss.NewStyle().Foreground(t.Warning).Render("⚠ conexão sem TLS (http://)") + "\n\n")
		}
		if s.err != "" {
			b.WriteString(lipgloss.NewStyle().Foreground(t.Error).Render(s.err) + "\n\n")
		}
		b.WriteString(dim.Render("Tab campo · ⏎ entrar · Esc cancelar — a senha nunca é salva"))
		return m.modalBox("entrar num servidor", b.String(), 64)
	}
	if len(s.list) == 0 {
		b.WriteString(text.Render("Nenhuma conta ainda.") + "\n\n")
	}
	now := time.Now()
	appUp := appAlive()
	for i, a := range s.list {
		cur := "  "
		if i == s.cursor {
			cur = "▸ "
		}
		state := lipgloss.NewStyle().Foreground(t.Accent).Render("● conectada")
		switch {
		case a.Token == "" && appUp:
			state = lipgloss.NewStyle().Foreground(t.Warning).Render("● precisa entrar · L liga pelo Lumi.app")
		case a.Token == "":
			state = lipgloss.NewStyle().Foreground(t.Warning).Render("● precisa entrar")
		case !a.ExpiresAt.IsZero() && now.After(a.ExpiresAt):
			state = lipgloss.NewStyle().Foreground(t.Warning).Render("● sessão expirada")
		}
		name := a.Username
		if a.DisplayName != "" && a.DisplayName != a.Username {
			name += " (" + a.DisplayName + ")"
		}
		row := cur + text.Render(padRight(truncate(name, 22), 22)) + "  " + dim.Render(padRight(truncate(books.Host(a.Server), 26), 26)) + "  " + state
		if i == s.cursor {
			row = lipgloss.NewStyle().Background(t.SelectedBg).Render(row)
		}
		b.WriteString(row + "\n")
		if i == s.cursor {
			web := m.noteCfg().WebOrigins[a.Server]
			if web == "" {
				web, _ = opener.WebOrigin(a.Server, nil)
			}
			if web == "" {
				web = "não definido (w)"
			}
			b.WriteString(dim.Render("    "+a.Server+" · web: "+web) + "\n")
		}
	}
	b.WriteString("\n")
	switch {
	case s.origin != nil:
		b.WriteString(text.Render("endereço web › ") + *s.origin + "▏\n" + dim.Render("⏎ salvar · Esc cancelar · vazio = automático") + "\n")
	case s.confirm == "signout":
		b.WriteString(lipgloss.NewStyle().Foreground(t.Warning).Render("Sair desta conta só no terminal? (o Lumi.app continua conectado) y/n") + "\n")
	case s.confirm == "signoutmac":
		b.WriteString(lipgloss.NewStyle().Foreground(t.Warning).Render("Sair desta conta neste Mac (terminal e Lumi.app)? y/n") + "\n")
	case s.confirm == "remove":
		b.WriteString(lipgloss.NewStyle().Foreground(t.Warning).Render("Remover esta conta deste aparelho (terminal e Lumi.app)? y/n") + "\n")
	case s.confirm == "server":
		b.WriteString(lipgloss.NewStyle().Foreground(t.Warning).Render("Remover o servidor e todas as suas contas? y/n") + "\n")
	}
	if s.err != "" {
		b.WriteString(lipgloss.NewStyle().Foreground(t.Error).Render(s.err) + "\n")
	}
	if s.info != "" {
		b.WriteString(lipgloss.NewStyle().Foreground(t.Info).Render(s.info) + "\n")
	}
	b.WriteString(dim.Render("a entrar/novo servidor · ⏎ entrar de novo · L ligar pelo Lumi.app · s sair do terminal · S sair neste Mac · d remover conta · D remover servidor · w endereço web · b livros · Esc"))
	return m.modalBox("contas e servidores", b.String(), 84)
}

var _ = fmt.Sprintf
