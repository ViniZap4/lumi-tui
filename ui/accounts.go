package ui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
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

// Accounts and servers inside the TUI (SPEC-PARTE2 §9, SPEC-LIVROS §5),
// `ga`: one card with the hierarchy servidores → contas → livros. Each
// server shows whether it answers (GET /api/info: name, version,
// latency); each account its display name, user, session expiry and
// book count; each book whether it has a folder here. Actions are keys
// and clickable buttons: entrar, sair, remover, adicionar servidor,
// endereço web, clonar livro / ligar pasta.
//
// The sign-in form is its own focused card: server URL validated and
// checked live (reachability + server name/version), username, masked
// password with show/hide, inline errors, a spinner while it works and a
// short success beat before it lands on the account (or continues the
// book flow that asked for it). Tokens go to accounts.yaml (0600) and
// are never rendered; the password lives only in the form until sent.

type acctRowKind int

const (
	rowServer acctRowKind = iota
	rowAccount
	rowBook
)

// acctRow is one line of the hierarchy.
type acctRow struct {
	kind   acctRowKind
	server string
	acct   account.Account // rowAccount (Token == "" for a book's missing account)
	known  bool            // the account exists in accounts.yaml
	book   books.Book      // rowBook
	nBooks int             // rowAccount
	nAccts int             // rowServer
}

type accountsScreen struct {
	list    []account.Account
	rows    []acctRow
	cursor  int
	form    *loginForm
	confirm string // "signout", "remove", "server"
	origin  *string
	busy    bool
	err     string
	info    string
	servers map[string]*serverStatus
	// then continues a book flow (clone / link) after a sign-in; with
	// standalone the screen exists only for that sign-in.
	then       *afterLogin
	standalone bool
	// needCheck: the servers have not been checked since opening (the
	// Update wrapper fires the checks).
	needCheck bool
}

type afterLogin struct {
	bookID string
	vault  string
	kind   cloneKind
	dest   string
}

// serverStatus is the last reachability check of a server.
type serverStatus struct {
	checking bool
	ok       bool
	info     serverInfo
	latency  time.Duration
	err      string
}

// serverInfo is GET /api/info (public).
type serverInfo struct {
	Name         string `json:"name"`
	Version      string `json:"version"`
	Protocol     int    `json:"protocol"`
	Registration string `json:"registration"`
}

type loginForm struct {
	field              int // 0 server, 1 user, 2 password
	server, user, pass string
	show               bool // password visible
	lockServer         bool // prefilled for a book: server/user fixed
	err                string
	busy               bool
	success            string
	// live server check
	checking bool
	checkSeq int
	checked  string // server URL the status belongs to
	status   *serverStatus
	urlErr   string
}

// loginDoneMsg carries a finished sign-in (the token stays inside acct
// and goes straight to accounts.yaml).
type loginDoneMsg struct {
	form *loginForm
	acct account.Account
	err  error
}

type loginSettledMsg struct{ form *loginForm }

type signOutDoneMsg struct{ server, user string }

// serverCheckMsg is a finished GET /api/info.
type serverCheckMsg struct {
	server string
	form   *loginForm // nil: the accounts list
	seq    int
	status serverStatus
}

type serverCheckTickMsg struct {
	form *loginForm
	seq  int
}

// loginTimeout bounds a sign-in request.
var loginTimeout = 30 * time.Second

// serverCheckDelay debounces the live check while the URL is typed.
var serverCheckDelay = 400 * time.Millisecond

// infoClient performs reachability checks (tests restrict it to
// loopback).
var infoClient = &http.Client{Timeout: 5 * time.Second}

func (m *Model) openAccounts() {
	m.acct = &accountsScreen{servers: map[string]*serverStatus{}, needCheck: true}
	m.reloadAccounts()
}

// openSignInFor opens the sign-in card for a book's account; after it,
// the flow (clone / link) continues.
func (m *Model) openSignInFor(b books.Book, kind cloneKind) {
	m.openAccounts()
	s := m.acct
	s.standalone = true
	s.needCheck = false
	s.then = &afterLogin{bookID: b.ID, vault: b.VaultID, kind: kind}
	s.form = &loginForm{server: b.ServerURL, user: b.Account, field: 2, lockServer: b.Account != ""}
	if b.Account == "" {
		s.form.field = 1
	}
}

func (m *Model) reloadAccounts() {
	s := m.acct
	if f, err := account.Load(); err == nil {
		s.list = f.Accounts
	} else {
		s.err = "accounts.yaml: " + err.Error()
	}
	s.rows = m.buildAcctRows()
	s.cursor = clamp(s.cursor, 0, max(0, len(s.rows)-1))
}

// buildAcctRows lays out servidores → contas → livros. Servers and
// accounts referenced only by books show too (needing a sign-in).
func (m Model) buildAcctRows() []acctRow {
	s := m.acct
	reg := m.registryList()
	type acctKey struct{ server, user string }
	var servers []string
	seenSrv := map[string]bool{}
	addSrv := func(srv string) string {
		for _, x := range servers {
			if account.SameServer(x, srv) {
				return x
			}
		}
		if !seenSrv[srv] {
			seenSrv[srv] = true
			servers = append(servers, srv)
		}
		return srv
	}
	accts := map[string][]acctRow{}
	seenAcct := map[acctKey]bool{}
	for _, a := range s.list {
		srv := addSrv(a.Server)
		k := acctKey{srv, strings.ToLower(a.Username)}
		if !seenAcct[k] {
			seenAcct[k] = true
			accts[srv] = append(accts[srv], acctRow{kind: rowAccount, server: srv, acct: a, known: true})
		}
	}
	for _, b := range reg {
		if !b.IsBound() || b.ServerURL == "" {
			continue
		}
		srv := addSrv(b.ServerURL)
		k := acctKey{srv, strings.ToLower(b.Account)}
		if !seenAcct[k] {
			seenAcct[k] = true
			accts[srv] = append(accts[srv], acctRow{kind: rowAccount, server: srv,
				acct: account.Account{Server: b.ServerURL, Username: b.Account}})
		}
	}
	var rows []acctRow
	for _, srv := range servers {
		rows = append(rows, acctRow{kind: rowServer, server: srv, nAccts: len(accts[srv])})
		for _, ar := range accts[srv] {
			var bks []books.Book
			for _, b := range reg {
				if b.IsBound() && account.SameServer(b.ServerURL, srv) && strings.EqualFold(b.Account, ar.acct.Username) {
					bks = append(bks, b)
				}
			}
			ar.nBooks = len(bks)
			rows = append(rows, ar)
			for _, b := range bks {
				rows = append(rows, acctRow{kind: rowBook, server: srv, acct: ar.acct, known: ar.known, book: b})
			}
		}
	}
	return rows
}

func (s *accountsScreen) current() (acctRow, bool) {
	if s.cursor >= 0 && s.cursor < len(s.rows) {
		return s.rows[s.cursor], true
	}
	return acctRow{}, false
}

// currentAccount is the account of the selected row (accounts and books).
func (s *accountsScreen) currentAccount() (account.Account, bool) {
	r, ok := s.current()
	if !ok || r.kind == rowServer || !r.known {
		return account.Account{}, false
	}
	return r.acct, true
}

// selectAccount puts the cursor on (server, user).
func (s *accountsScreen) selectAccount(server, user string) {
	for i, r := range s.rows {
		if r.kind == rowAccount && account.SameServer(r.server, server) && strings.EqualFold(r.acct.Username, user) {
			s.cursor = i
			return
		}
	}
}

// checkAll checks every listed server's reachability.
func (s *accountsScreen) checkAll() tea.Cmd {
	var cmds []tea.Cmd
	for _, r := range s.rows {
		if r.kind != rowServer {
			continue
		}
		cmds = append(cmds, s.checkServer(r.server))
	}
	return tea.Batch(cmds...)
}

func (s *accountsScreen) checkServer(server string) tea.Cmd {
	st := s.servers[server]
	if st == nil {
		st = &serverStatus{}
		s.servers[server] = st
	}
	st.checking = true
	return checkServerCmd(server, nil, 0)
}

// checkServerCmd GETs <server>/api/info and times it.
func checkServerCmd(server string, form *loginForm, seq int) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		st := serverStatus{}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(server, "/")+"/api/info", nil)
		if err != nil {
			st.err = err.Error()
			return serverCheckMsg{server: server, form: form, seq: seq, status: st}
		}
		start := time.Now()
		resp, err := infoClient.Do(req)
		st.latency = time.Since(start)
		if err != nil {
			st.err = netErrText(err)
			if st.err == "" {
				st.err = "servidor inacessível"
			}
			return serverCheckMsg{server: server, form: form, seq: seq, status: st}
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			st.err = fmt.Sprintf("não parece um servidor lumi (HTTP %d)", resp.StatusCode)
			return serverCheckMsg{server: server, form: form, seq: seq, status: st}
		}
		if err := json.NewDecoder(resp.Body).Decode(&st.info); err != nil {
			st.err = "não parece um servidor lumi (resposta inválida)"
			return serverCheckMsg{server: server, form: form, seq: seq, status: st}
		}
		st.ok = true
		return serverCheckMsg{server: server, form: form, seq: seq, status: st}
	}
}

func (m Model) onServerCheck(msg serverCheckMsg) (tea.Model, tea.Cmd) {
	s := m.acct
	if s == nil {
		return m, nil
	}
	if msg.form != nil {
		f := msg.form
		if f == s.form && f.checkSeq == msg.seq {
			st := msg.status
			f.checking, f.status, f.checked = false, &st, msg.server
		}
		return m, nil
	}
	st := msg.status
	s.servers[msg.server] = &st
	return m, nil
}

// formServerURL normalises the form's server field (https:// is assumed
// when no scheme is typed). urlErr is the inline validation message.
func (f *loginForm) serverURL() (string, string) {
	raw := strings.TrimSpace(f.server)
	if raw == "" {
		return "", "informe o endereço do servidor"
	}
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	u, err := account.NormalizeServerURL(raw)
	if err != nil {
		return "", "endereço inválido (ex.: https://lumi.exemplo.com)"
	}
	if strings.HasPrefix(u, "http://") && !account.IsInsecureAllowed(u) {
		return "", "http:// só é aceito para localhost, IPs privados e *.local — use https://"
	}
	return u, ""
}

// checkNow checks the form's server at once (prefilled forms).
func (f *loginForm) checkNow() tea.Cmd {
	if f == nil {
		return nil
	}
	u, bad := f.serverURL()
	f.urlErr = ""
	if bad != "" {
		if strings.TrimSpace(f.server) != "" {
			f.urlErr = bad
		}
		f.checking = false
		return nil
	}
	f.checkSeq++
	f.checking = true
	return checkServerCmd(u, f, f.checkSeq)
}

// scheduleCheck debounces the live check while the URL is edited.
func (f *loginForm) scheduleCheck() tea.Cmd {
	f.checkSeq++
	f.status, f.urlErr = nil, ""
	seq := f.checkSeq
	f.checking = false
	return tea.Tick(serverCheckDelay, func(time.Time) tea.Msg { return serverCheckTickMsg{form: f, seq: seq} })
}

func (m Model) onServerCheckTick(msg serverCheckTickMsg) (tea.Model, tea.Cmd) {
	if m.acct == nil || m.acct.form != msg.form || msg.form.checkSeq != msg.seq {
		return m, nil
	}
	f := msg.form
	u, bad := f.serverURL()
	if bad != "" {
		f.urlErr = bad
		return m, nil
	}
	f.checking = true
	return m, checkServerCmd(u, f, f.checkSeq)
}

// --- keys --------------------------------------------------------------

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
				delete(cfg.WebOrigins, a.server)
			} else {
				if _, err := opener.WebURL(v, "", ""); err != nil {
					s.err = "endereço web inválido (use https://…)"
					return m, nil
				}
				cfg.WebOrigins[a.server] = strings.TrimRight(v, "/")
			}
			if err := cfg.Save(); err != nil {
				s.err = err.Error()
			} else {
				s.info, s.err = "endereço web salvo", ""
				m.cfg = cfg
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
		r, ok := s.current()
		if !ok {
			return m, nil
		}
		return m.accountAction(kind, r)
	}
	s.err, s.info = "", ""
	r, ok := s.current()
	switch key {
	case "esc", "q":
		m.acct = nil
	case "ctrl+c":
		return m, tea.Quit
	case "j", "down":
		s.cursor = clamp(s.cursor+1, 0, max(0, len(s.rows)-1))
	case "k", "up":
		s.cursor = clamp(s.cursor-1, 0, max(0, len(s.rows)-1))
	case "g":
		s.cursor = 0
	case "G":
		s.cursor = max(0, len(s.rows)-1)
	case "a":
		s.form = &loginForm{}
	case "n":
		if ok {
			s.form = &loginForm{server: r.server, field: 1}
			return m, s.form.checkNow()
		}
		s.form = &loginForm{}
	case "enter", "l":
		if !ok {
			s.form = &loginForm{}
			return m, nil
		}
		switch r.kind {
		case rowServer:
			s.form = &loginForm{server: r.server, field: 1}
		case rowAccount:
			s.form = &loginForm{server: r.server, user: r.acct.Username, field: 2}
		case rowBook:
			return m.acctBookAction(r, "enter")
		}
		return m, s.form.checkNow()
	case "c", "L":
		if ok && r.kind == rowBook {
			return m.acctBookAction(r, key)
		}
	case "s":
		if ok && r.kind == rowAccount && r.known && r.acct.Token != "" {
			s.confirm = "signout"
		}
	case "d", "x":
		if ok && r.kind == rowAccount && r.known {
			s.confirm = "remove"
		}
	case "D":
		if ok {
			s.confirm = "server"
		}
	case "r":
		if ok {
			return m, s.checkServer(r.server)
		}
	case "w":
		if ok {
			v := m.noteCfg().WebOrigins[r.server]
			if v == "" {
				v, _ = opener.WebOrigin(r.server, nil)
			}
			s.origin = &v
		}
	case "gb", "b":
		m.acct = nil
		m.openBooksLevel()
	}
	return m, nil
}

// acctBookAction opens / clones / links a book from its row.
func (m Model) acctBookAction(r acctRow, key string) (tea.Model, tea.Cmd) {
	b := r.book
	m.acct = nil
	if b.Path != "" && key != "c" && key != "L" {
		if m.bookMgr == nil {
			return m, nil
		}
		return m.openBookByVault(b)
	}
	kind := cloneKindClone
	if key == "L" {
		kind = cloneKindLink
	}
	return m.beginBookFlow(b, kind)
}

func (m Model) updateLoginForm(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	s := m.acct
	f := s.form
	if f.busy || f.success != "" {
		if msg.String() == "ctrl+c" {
			return m, tea.Quit
		}
		return m, nil
	}
	switch msg.String() {
	case "esc":
		f.pass = ""
		s.form = nil
		if s.standalone {
			m.acct = nil
		}
		return m, nil
	case "tab", "down":
		f.field = m.nextField(f, 1)
		return m, nil
	case "shift+tab", "up":
		f.field = m.nextField(f, -1)
		return m, nil
	case "ctrl+t":
		f.show = !f.show
		return m, nil
	case "enter":
		if f.field < 2 && (f.server == "" || f.user == "" || f.pass == "") {
			f.field = m.nextField(f, 1)
			return m, nil
		}
		return m.submitLogin()
	case "ctrl+c":
		return m, tea.Quit
	}
	if f.lockServer && f.field < 2 {
		return m, nil
	}
	ptr := []*string{&f.server, &f.user, &f.pass}[f.field]
	if v, ok := editLine(*ptr, msg); ok {
		*ptr = v
		f.err = ""
		if f.field == 0 {
			return m, f.scheduleCheck()
		}
	}
	return m, nil
}

// nextField cycles the focus (a prefilled book sign-in keeps it on the
// password).
func (m Model) nextField(f *loginForm, d int) int {
	if f.lockServer {
		return 2
	}
	return (f.field + 3 + d) % 3
}

func (m Model) submitLogin() (tea.Model, tea.Cmd) {
	s := m.acct
	f := s.form
	server, bad := f.serverURL()
	if bad != "" {
		f.err, f.field = bad, 0
		return m, nil
	}
	user := strings.TrimSpace(f.user)
	if user == "" {
		f.err, f.field = "informe o usuário", 1
		return m, nil
	}
	if f.pass == "" {
		f.err, f.field = "informe a senha", 2
		return m, nil
	}
	pass := f.pass
	f.pass = "" // the password leaves the form now
	f.busy, f.err = true, ""
	return m, func() tea.Msg {
		c, err := account.NewClient(server)
		if err != nil {
			return loginDoneMsg{form: f, err: err}
		}
		ctx, cancel := context.WithTimeout(context.Background(), loginTimeout)
		defer cancel()
		resp, err := c.Login(ctx, user, pass)
		if err != nil {
			return loginDoneMsg{form: f, err: err}
		}
		return loginDoneMsg{form: f, acct: account.Account{Server: c.BaseURL().String(), Username: resp.User.Username,
			UserID: resp.User.ID, DisplayName: resp.User.DisplayName, Token: resp.Token, ExpiresAt: resp.ExpiresAt,
			AddedAt: time.Now().UTC()}}
	}
}

func (m Model) onLoginDone(msg loginDoneMsg) (tea.Model, tea.Cmd) {
	if m.acct == nil {
		m.openAccounts()
	}
	s := m.acct
	f := msg.form
	if f == nil || s.form != f {
		f = s.form
	}
	if f == nil {
		f = &loginForm{}
	}
	f.busy = false
	if msg.err != nil {
		s.form = f
		f.err = loginError(msg.err)
		f.field = 2
		return m, nil
	}
	file, err := account.Load()
	if err != nil {
		f.err = "accounts.yaml: " + err.Error()
		return m, nil
	}
	file.Upsert(msg.acct)
	if err := file.Save(); err != nil {
		f.err = "accounts.yaml: " + err.Error()
		return m, nil
	}
	if m.bookMgr != nil {
		m.bookMgr.AddAccount(msg.acct)
	}
	m.reloadAccounts()
	s.selectAccount(msg.acct.Server, msg.acct.Username)
	who := msg.acct.Username
	if msg.acct.DisplayName != "" && msg.acct.DisplayName != who {
		who = msg.acct.DisplayName + " (" + who + ")"
	}
	f.success = "entrou como " + who + " em " + books.Host(msg.acct.Server)
	s.info = f.success
	if m.mo == nil || m.mo.reduced {
		return m.onLoginSettled(loginSettledMsg{form: f})
	}
	return m, tea.Tick(700*time.Millisecond, func(time.Time) tea.Msg { return loginSettledMsg{form: f} })
}

// onLoginSettled ends the success beat: back to the list (on the new
// account), or on with the book flow that asked for the sign-in.
func (m Model) onLoginSettled(msg loginSettledMsg) (tea.Model, tea.Cmd) {
	s := m.acct
	if s == nil || s.form != msg.form {
		return m, nil
	}
	s.form = nil
	toast := m.showToast(s.info, ToastSuccess, 3*time.Second)
	then := s.then
	if then == nil {
		return m, tea.Batch(toast, s.checkAll())
	}
	m.acct = nil
	var book *books.Book
	for _, b := range m.registryList() {
		if b.ID == then.bookID || (then.vault != "" && b.VaultID == then.vault) {
			bb := b
			book = &bb
			break
		}
	}
	if book == nil {
		return m, toast
	}
	if book.Path != "" {
		mm, cmd := m.openBookByVault(*book)
		return mm, tea.Batch(toast, cmd)
	}
	m.openCloneCard(*book, then.kind)
	if then.dest != "" {
		m.clone.input = then.dest
		m.clone.retriedAuth = true
		mm, cmd := m.submitClone()
		return mm, tea.Batch(toast, cmd)
	}
	return m, toast
}

func loginError(err error) string {
	var apiErr *account.APIError
	if errors.As(err, &apiErr) {
		switch apiErr.Code {
		case "invalid_credentials":
			return "usuário ou senha inválidos"
		case "account_locked", "account_disabled":
			msg := "conta bloqueada"
			if apiErr.Code == "account_disabled" {
				msg = "conta desativada — fale com o administrador"
			} else if apiErr.RetryAfter > 0 {
				msg += " — tente de novo em " + humanWait(apiErr.RetryAfter)
			}
			return msg
		case "rate_limited":
			if apiErr.RetryAfter > 0 {
				return "muitas tentativas — tente de novo em " + humanWait(apiErr.RetryAfter)
			}
			return "muitas tentativas — tente mais tarde"
		}
	}
	if errors.Is(err, account.ErrUnauthorized) {
		return "usuário ou senha inválidos"
	}
	if t := netErrText(err); t != "" {
		return t
	}
	if apiErr != nil {
		if apiErr.Detail != "" {
			return apiErr.Detail
		}
		return apiErr.Code
	}
	return err.Error()
}

// humanWait is "45 s" / "3 min" / "2 h".
func humanWait(d time.Duration) string {
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%d s", int(d.Seconds()+0.5))
	case d < time.Hour:
		return fmt.Sprintf("%d min", int(d.Minutes()+0.5))
	default:
		return fmt.Sprintf("%d h", int(d.Hours()+0.5))
	}
}

// accountAction signs out, removes an account or a whole server.
func (m Model) accountAction(kind string, r acctRow) (tea.Model, tea.Cmd) {
	s := m.acct
	f, err := account.Load()
	if err != nil {
		s.err = err.Error()
		return m, nil
	}
	var targets []account.Account
	switch kind {
	case "server":
		for _, x := range f.ForServer(r.server) {
			targets = append(targets, *x)
		}
	default:
		if x := f.Find(r.acct.Server, r.acct.Username); x != nil {
			targets = []account.Account{*x}
		}
	}
	var cmds []tea.Cmd
	for _, t := range targets {
		if t.Token != "" {
			cmds = append(cmds, logoutCmd(t))
		}
		if kind == "signout" {
			if x := f.Find(t.Server, t.Username); x != nil {
				x.Token = ""
			}
		} else {
			f.Remove(t.Server, t.Username)
		}
		if m.bookMgr != nil {
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
		s.info = "saiu de " + books.Host(r.server) + " (" + r.acct.Username + ")"
		s.selectAccount(r.server, r.acct.Username)
	case "server":
		s.info = "servidor " + books.Host(r.server) + " removido"
	default:
		s.info = "conta removida"
	}
	return m, tea.Batch(cmds...)
}

// logoutCmd revokes the session server-side (best effort).
func logoutCmd(a account.Account) tea.Cmd {
	return func() tea.Msg {
		c, err := account.NewClient(a.Server)
		if err != nil {
			return nil
		}
		c.SetToken(a.Token)
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = c.Logout(ctx)
		return signOutDoneMsg{server: a.Server, user: a.Username}
	}
}

// --- rendering ---------------------------------------------------------

func (m Model) renderAccounts() string {
	if m.acct.form != nil {
		return m.loginCard().alone(m)
	}
	return m.accountsCard().alone(m)
}

// sessionLabel is the account's state: dot colour and text.
func sessionLabel(a account.Account, known bool, now time.Time) (lipgloss.Color, string) {
	t := theme.Current
	switch {
	case !known || a.Token == "":
		return t.Warning, "precisa entrar"
	case !a.ExpiresAt.IsZero() && now.After(a.ExpiresAt):
		return t.Warning, "sessão expirada"
	case !a.ExpiresAt.IsZero():
		return t.Accent, "conectada · sessão até " + ptDate(a.ExpiresAt)
	}
	return t.Accent, "conectada"
}

var ptMonths = [...]string{"jan", "fev", "mar", "abr", "mai", "jun", "jul", "ago", "set", "out", "nov", "dez"}

// ptDate is "3 dez 2026".
func ptDate(d time.Time) string {
	d = d.Local()
	return fmt.Sprintf("%d %s %d", d.Day(), ptMonths[d.Month()-1], d.Year())
}

func (m Model) serverLabel(server string) (string, string) {
	t := theme.Current
	st := m.acct.servers[server]
	switch {
	case st == nil:
		return lipgloss.NewStyle().Foreground(t.TextDim).Render("○"), ""
	case st.checking:
		return m.spinner(), "verificando…"
	case st.ok:
		info := fmt.Sprintf("online · %d ms", st.latency.Milliseconds())
		if st.info.Name != "" {
			info += " · " + st.info.Name
		}
		if st.info.Version != "" {
			info += " " + st.info.Version
		}
		return lipgloss.NewStyle().Foreground(t.Accent).Render("●"), info
	}
	return lipgloss.NewStyle().Foreground(t.Error).Render("●"), "offline — " + st.err
}

func (m Model) accountsCard() card {
	s := m.acct
	t := theme.Current
	dim := lipgloss.NewStyle().Foreground(t.TextDim)
	text := lipgloss.NewStyle().Foreground(t.Text)
	head := lipgloss.NewStyle().Foreground(mutedColor).Bold(true)
	w := m.cardWidth(92)
	var c cardBuilder
	c.line(lipgloss.NewStyle().Bold(true).Foreground(primaryColor).Render("contas e servidores") +
		dim.Render(fmt.Sprintf("  ·  %s", plural(countKind(s.rows, rowServer), "servidor", "servidores"))))
	c.nl()
	if len(s.rows) == 0 {
		c.line(text.Render("Nenhum servidor ainda."))
		c.line(dim.Render("Adicione um servidor lumi para sincronizar seus livros."))
		c.nl()
	} else {
		c.line(head.Render("SERVIDORES") + dim.Render("  →  contas  →  livros"))
	}
	// window of rows around the cursor
	room := max(3, m.contentHeight()-16)
	start := 0
	if s.cursor >= room {
		start = s.cursor - room + 1
	}
	now := time.Now()
	for i := start; i < len(s.rows) && i < start+room; i++ {
		r := s.rows[i]
		on := i == s.cursor
		mark := "  "
		if on {
			mark = lipgloss.NewStyle().Foreground(t.Accent).Render("▸ ")
		}
		var line string
		switch r.kind {
		case rowServer:
			dot, info := m.serverLabel(r.server)
			name := lipgloss.NewStyle().Bold(true).Foreground(t.Text).Render(truncate(books.Host(r.server), 34))
			line = mark + dot + " " + padVisual(name, 36) + dim.Render(truncate(info, max(4, w-42)))
		case rowAccount:
			col, st := sessionLabel(r.acct, r.known, now)
			name := r.acct.Username
			if r.acct.DisplayName != "" && r.acct.DisplayName != r.acct.Username {
				name = r.acct.DisplayName + " · " + r.acct.Username
			}
			line = "   " + mark + padVisual(text.Render(truncate(name, 24)), 25) +
				lipgloss.NewStyle().Foreground(col).Render(padVisual("● "+truncate(st, 34), 37)) +
				dim.Render(plural(r.nBooks, "livro", "livros"))
		case rowBook:
			where := dim.Render(truncate(r.book.Path, 30))
			glyph := lipgloss.NewStyle().Foreground(t.Accent).Render("◆")
			if r.book.Path == "" {
				glyph = dim.Render("◇")
				where = lipgloss.NewStyle().Foreground(t.Warning).Render("sem pasta aqui") + dim.Render(" — c clonar · L ligar pasta")
			}
			line = "      " + mark + glyph + " " + padVisual(text.Render(truncate(r.book.Name, 26)), 27) + where
		}
		if on {
			line = lipgloss.NewStyle().Background(t.SelectedBg).Render(padVisual(line, w))
		}
		c.hitLine(w, fmt.Sprintf("#row:%d", i))
		c.line(line)
	}
	if len(s.rows) > start+room {
		c.line(dim.Render(fmt.Sprintf("   … mais %d", len(s.rows)-start-room)))
	}
	c.nl()
	// what the selected row can do
	r, ok := s.current()
	switch {
	case s.origin != nil:
		c.line(text.Render("endereço web › ") + *s.origin + "▏")
		c.button("⏎", "salvar", "enter")
		c.gap(3)
		c.button("Esc", "cancelar", "esc")
		c.gap(3)
		c.text(dim.Render("vazio = automático"))
		c.nl()
	case s.confirm != "":
		q := map[string]string{
			"signout": "Sair desta conta?",
			"remove":  "Remover esta conta deste aparelho?",
			"server":  "Remover o servidor e todas as suas contas?",
		}[s.confirm]
		c.text(lipgloss.NewStyle().Foreground(t.Warning).Render(q + "  "))
		c.button("y", "sim", "y")
		c.gap(3)
		c.button("n", "não", "n")
		c.nl()
	case ok:
		switch r.kind {
		case rowServer:
			c.button("n", "nova conta", "n")
			c.gap(3)
			c.button("r", "verificar", "r")
			c.gap(3)
			c.button("w", "endereço web", "w")
			c.gap(3)
			c.button("D", "remover servidor", "D")
		case rowAccount:
			label := "entrar de novo"
			if !r.known || r.acct.Token == "" {
				label = "entrar"
			}
			c.button("⏎", label, "enter")
			if r.known && r.acct.Token != "" {
				c.gap(3)
				c.button("s", "sair", "s")
			}
			if r.known {
				c.gap(3)
				c.button("d", "remover conta", "d")
			}
			c.gap(3)
			c.button("w", "endereço web", "w")
		case rowBook:
			if r.book.Path == "" {
				c.button("c", "clonar livro", "c")
				c.gap(3)
				c.button("L", "ligar pasta", "L")
			} else {
				c.button("⏎", "abrir livro", "enter")
			}
		}
		c.nl()
	}
	c.button("a", "adicionar servidor", "a")
	c.gap(3)
	c.button("b", "livros", "b")
	c.gap(3)
	c.button("Esc", "voltar", "esc")
	c.nl()
	if s.err != "" {
		c.line(lipgloss.NewStyle().Foreground(t.Error).Render("✕ " + s.err))
	}
	if s.info != "" {
		c.line(lipgloss.NewStyle().Foreground(t.Info).Render(s.info))
	}
	if ok && s.origin == nil && s.confirm == "" && r.kind != rowBook {
		web := m.noteCfg().WebOrigins[r.server]
		if web == "" {
			web, _ = opener.WebOrigin(r.server, nil)
		}
		if web == "" {
			web = "não definido (w)"
		}
		c.line(dim.Render(truncate(r.server+" · web: "+web, w)))
	}
	return m.buildCard(&c, w)
}

func countKind(rows []acctRow, k acctRowKind) int {
	n := 0
	for _, r := range rows {
		if r.kind == k {
			n++
		}
	}
	return n
}

func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return fmt.Sprintf("%d %s", n, many)
}

// loginCard is the focused sign-in card.
func (m Model) loginCard() card {
	s := m.acct
	f := s.form
	t := theme.Current
	dim := lipgloss.NewStyle().Foreground(t.TextDim)
	text := lipgloss.NewStyle().Foreground(t.Text)
	w := m.cardWidth(66)
	var c cardBuilder
	title := "entrar num servidor"
	if s.then != nil {
		title = "entrar para continuar"
	}
	c.line(lipgloss.NewStyle().Bold(true).Foreground(primaryColor).Render(title))
	if s.then != nil {
		c.line(dim.Render("o livro precisa da sua conta neste servidor"))
	}
	c.nl()
	fieldW := w - 12
	field := func(i int, label, val string) {
		cur := "  "
		st := text
		if f.field == i && !f.busy && f.success == "" {
			cur = lipgloss.NewStyle().Foreground(t.Accent).Render("▸ ")
			st = st.Underline(true)
			val += "▏"
		}
		if f.lockServer && i < 2 {
			st = dim
		}
		c.hitLine(w, fmt.Sprintf("#field:%d", i))
		c.text(cur + dim.Render(padRight(label, 10)) + st.Render(padRight(truncate(val, fieldW), fieldW)))
	}
	field(0, "servidor", f.server)
	c.nl()
	// live server status
	status := ""
	switch {
	case f.urlErr != "":
		status = lipgloss.NewStyle().Foreground(t.Error).Render("✕ " + f.urlErr)
	case f.checking:
		status = m.spinner() + dim.Render(" verificando servidor…")
	case f.status != nil && f.status.ok:
		info := "online"
		if f.status.info.Name != "" {
			info = f.status.info.Name
		}
		if f.status.info.Version != "" {
			info += " · " + f.status.info.Version
		}
		info += fmt.Sprintf(" · %d ms", f.status.latency.Milliseconds())
		status = lipgloss.NewStyle().Foreground(t.Accent).Render("● ") + dim.Render(info)
	case f.status != nil:
		status = lipgloss.NewStyle().Foreground(t.Error).Render("● " + f.status.err)
	}
	c.line(strings.Repeat(" ", 12) + truncate(status, fieldW+10))
	if strings.HasPrefix(strings.TrimSpace(f.server), "http://") && f.urlErr == "" {
		c.line(strings.Repeat(" ", 12) + lipgloss.NewStyle().Foreground(t.Warning).Render("⚠ conexão sem TLS (http://)"))
	}
	field(1, "usuário", f.user)
	c.nl()
	pass := strings.Repeat("•", len([]rune(f.pass)))
	if f.show {
		pass = f.pass
	}
	field(2, "senha", pass)
	c.nl()
	c.nl()
	switch {
	case f.success != "":
		c.line(lipgloss.NewStyle().Foreground(t.Accent).Bold(true).Render("✓ " + f.success))
	case f.busy:
		c.line(m.spinner() + text.Render(" entrando em "+books.Host(f.server)+"…"))
	case f.err != "":
		c.line(lipgloss.NewStyle().Foreground(t.Error).Render("✕ " + truncate(f.err, w-2)))
	default:
		c.nl()
	}
	c.nl()
	c.button("⏎", "entrar", "enter")
	c.gap(3)
	c.button("Esc", "cancelar", "esc")
	c.gap(3)
	show := "mostrar senha"
	if f.show {
		show = "ocultar senha"
	}
	c.button("⌃t", show, "ctrl+t")
	c.nl()
	c.line(dim.Render("Tab / ⇧Tab campo · a senha nunca é salva"))
	return m.buildCard(&c, w)
}

// accountsMouse: rows select (double-click acts), buttons press keys,
// fields focus.
func (m Model) accountsMouse(msg tea.MouseMsg, wheel int, press bool) (tea.Model, tea.Cmd) {
	s := m.acct
	if wheel != 0 && s.form == nil {
		s.cursor = clamp(s.cursor+wheel, 0, max(0, len(s.rows)-1))
		return m, nil
	}
	if !press {
		return m, nil
	}
	var cd card
	if s.form != nil {
		cd = m.loginCard()
	} else {
		cd = m.accountsCard()
	}
	k := cd.keyAt(msg.X, msg.Y)
	switch {
	case k == "":
		return m, nil
	case strings.HasPrefix(k, "#row:"):
		var i int
		fmt.Sscan(strings.TrimPrefix(k, "#row:"), &i)
		dbl := m.isDoubleClick(msg.X, msg.Y)
		s.cursor = clamp(i, 0, max(0, len(s.rows)-1))
		if dbl {
			return m.updateAccounts(keyFor("enter"))
		}
		return m, nil
	case strings.HasPrefix(k, "#field:"):
		if s.form != nil && !s.form.lockServer {
			fmt.Sscan(strings.TrimPrefix(k, "#field:"), &s.form.field)
		}
		return m, nil
	}
	return m.updateAccounts(pressCardKey(k))
}
