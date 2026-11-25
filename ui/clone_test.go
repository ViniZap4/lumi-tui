package ui

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/mattn/go-runewidth"
	"github.com/vinizap/lumi/tui-client/account"
	"github.com/vinizap/lumi/tui-client/books"
	"github.com/vinizap/lumi/tui-client/feed"
)

const testVault = "0b9d2a52-7c1e-4b38-9a43-1f2e3d4c5b6a"

// cloneServer is a fake lumi server: /api/info, login (password
// "s3cret"), one vault "test" with two notes. Requests with any token
// other than the current one get 401. block, when set, holds note
// downloads until closed.
type cloneServer struct {
	*httptest.Server
	mu    sync.Mutex
	token string
	block chan struct{}
}

func newCloneServer(t *testing.T) *cloneServer {
	t.Helper()
	cs := &cloneServer{token: "tok"}
	cs.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		cs.mu.Lock()
		tok, block := cs.token, cs.block
		cs.mu.Unlock()
		switch r.URL.Path {
		case "/api/info":
			w.Write([]byte(`{"name":"lumi-test","version":"2.9.0","protocol":1}`))
			return
		case "/api/auth/login":
			var body struct{ Username, Password string }
			json.NewDecoder(r.Body).Decode(&body)
			if body.Password != "s3cret" {
				w.WriteHeader(401)
				w.Write([]byte(`{"error":"invalid_credentials"}`))
				return
			}
			w.Write([]byte(`{"token":"` + tok + `","expires_at":"2099-01-01T12:00:00Z","user":{"id":"u1","username":"ana","display_name":"Ana"}}`))
			return
		}
		if r.Header.Get("X-Lumi-Token") != tok {
			w.WriteHeader(401)
			w.Write([]byte(`{"error":"unauthorized"}`))
			return
		}
		switch r.URL.Path {
		case "/api/vaults":
			w.Write([]byte(`{"vaults":[{"id":"` + testVault + `","slug":"test","name":"test","created_by":"u1","created_at":""}]}`))
		case "/api/vaults/" + testVault + "/notes":
			w.Write([]byte(`{"notes":[{"id":"n1","vault_id":"v","path":"hello.md","title":"Hello"},{"id":"n2","vault_id":"v","path":"sub/world.md","title":"World"}],"limit":200,"offset":0}`))
		case "/api/vaults/" + testVault + "/notes/n1/content", "/api/vaults/" + testVault + "/notes/n2/content":
			if block != nil {
				<-block
			}
			id := filepath.Base(filepath.Dir(r.URL.Path))
			w.Write([]byte(`{"id":"` + id + `","path":"x","body":"# ` + id + `\n"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(cs.Close)
	return cs
}

// cloneEnv: temp HOME with accounts.yaml (token or not) and books.yaml
// holding the server book "test" without a folder, plus a local book
// the model runs in.
func cloneEnv(t *testing.T, srv string, token string) (Model, string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(home, ".cache"))
	f := &account.File{}
	f.Upsert(account.Account{Server: srv, Username: "ana", DisplayName: "Ana", Token: token,
		ExpiresAt: time.Date(2099, 1, 1, 12, 0, 0, 0, time.UTC), AddedAt: time.Now()})
	if err := f.Save(); err != nil {
		t.Fatal(err)
	}
	local := filepath.Join(home, "notas")
	os.MkdirAll(local, 0o755)
	mustWrite(t, filepath.Join(local, "a.md"), "# a\n")
	st, err := books.Open()
	if err != nil {
		t.Fatal(err)
	}
	st.Registry.Add(books.Book{ID: "local1", Name: "Notas", Kind: books.KindLocal, Path: "~/notas"})
	st.Registry.Add(books.Book{ID: testVault, Name: "test", Kind: books.KindBound, ServerURL: srv, Account: "ana", VaultID: testVault})
	if err := st.Save(); err != nil {
		t.Fatal(err)
	}
	mgr := feed.NewManager(st, f.Accounts)
	m := NewModelWithInitialNote(local, "").WithBooks(mgr, "local1")
	m.width, m.height = 110, 32
	return m, home
}

// toServerBook opens the books level on the server account's book.
func toServerBook(t *testing.T, m Model) Model {
	t.Helper()
	m = keys(t, m, "g", "b")
	if m.bk == nil {
		t.Fatal("books level")
	}
	m = keys(t, m, "h", "j", "l")
	v := stripAnsi(m.View())
	if !strings.Contains(v, "c clonar aqui") || !strings.Contains(v, "sem pasta") {
		t.Fatalf("preview should offer clone in place:\n%s", v)
	}
	return m
}

// finishClone feeds the clone goroutine's messages until it is done (a
// message taken by an abandoned test command is recovered from the
// flow's finished result).
func finishClone(t *testing.T, m Model) Model {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for m.clone != nil && m.clone.running {
		if time.Now().After(deadline) {
			t.Fatal("clone never finished")
		}
		var msg tea.Msg
		select {
		case msg = <-m.clone.ch:
		case <-time.After(50 * time.Millisecond):
			if d := m.clone.finished.Load(); d != nil {
				msg = *d
			} else {
				continue
			}
		}
		next, cmd := m.Update(msg)
		m = settle(next.(Model), dropWait(cmd))
	}
	return m
}

// dropWait: the test reads the clone channel itself.
func dropWait(cmd tea.Cmd) tea.Cmd { return nil }

func TestCloneInPlaceFromBooksLevel(t *testing.T) {
	cs := newCloneServer(t)
	m, home := cloneEnv(t, cs.URL, "tok")
	m = toServerBook(t, m)
	m = keys(t, m, "c")
	if m.clone == nil || m.clone.input != "~/lumi/test" {
		t.Fatalf("clone card with default folder, got %+v", m.clone)
	}
	if m.legendContext() != CtxClone {
		t.Fatalf("legend %v", m.legendContext())
	}
	// keys() settles commands, so the whole clone runs here
	m = keys(t, m, "enter")
	m = finishClone(t, m)
	if m.clone != nil {
		t.Fatalf("card should close, err %q", m.clone.err)
	}
	dest := filepath.Join(home, "lumi", "test")
	if b, err := os.ReadFile(filepath.Join(dest, "sub", "world.md")); err != nil || string(b) != "# n2\n" {
		t.Fatalf("world.md: %q %v", b, err)
	}
	if m.SwitchTarget() != dest {
		t.Fatalf("opens the book: %q", m.SwitchTarget())
	}
	st, _ := books.Open()
	if b := st.Registry.Get(testVault); b == nil || b.Path != "~/lumi/test" {
		t.Fatalf("books.yaml row %+v", b)
	}
	if b, _ := m.bookMgr.View(testVault); b.Book.Path != "~/lumi/test" {
		t.Fatal("the manager sees the new folder")
	}
	vs, _ := account.LoadVaults()
	if len(vs) != 1 || vs[0].ID != testVault || vs[0].Server != cs.URL || vs[0].Account != "ana" {
		t.Fatalf("vaults.yaml %+v", vs)
	}
}

func TestEnterOnPathlessBookClonesAndMouseActions(t *testing.T) {
	cs := newCloneServer(t)
	m, _ := cloneEnv(t, cs.URL, "tok")
	m = toServerBook(t, m)
	// click "c clonar aqui" in the preview
	x, y := findText(t, m.View(), "c clonar aqui")
	tm, _ := m.Update(tea.MouseMsg{X: x + 1, Y: y, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
	m = tm.(Model)
	if m.clone == nil || m.clone.kind != cloneKindClone {
		t.Fatal("click on the preview action opens the clone card")
	}
	// the card's Esc button
	x, y = findText(t, m.View(), "Esc cancelar")
	tm, _ = m.Update(tea.MouseMsg{X: x, Y: y, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
	m = tm.(Model)
	if m.clone != nil {
		t.Fatal("Esc button cancels")
	}
	// ⏎ on the pathless book also clones; L links instead
	m = keys(t, m, "enter")
	if m.clone == nil || m.clone.kind != cloneKindClone {
		t.Fatal("⏎ on a book without a folder opens the clone card")
	}
	m = keys(t, m, "esc", "L")
	if m.clone == nil || m.clone.kind != cloneKindLink || m.clone.input != "~/" {
		t.Fatalf("L opens link card: %+v", m.clone)
	}
}

func findText(t *testing.T, view, needle string) (int, int) {
	t.Helper()
	for y, l := range strings.Split(stripAnsi(view), "\n") {
		if i := strings.Index(l, needle); i >= 0 {
			return runewidth.StringWidth(l[:i]), y
		}
	}
	t.Fatalf("%q not on screen:\n%s", needle, stripAnsi(view))
	return 0, 0
}

func TestCloneValidationErrorsAndCancel(t *testing.T) {
	cs := newCloneServer(t)
	m, home := cloneEnv(t, cs.URL, "tok")
	m = toServerBook(t, m)
	m = keys(t, m, "c", "ctrl+u")
	m = keys(t, m, "enter")
	if m.clone.err != "informe uma pasta" {
		t.Fatalf("empty: %q", m.clone.err)
	}
	m = typeText(t, m, "rel/dir")
	m = keys(t, m, "enter")
	if !strings.Contains(m.clone.err, "absoluto") {
		t.Fatalf("relative: %q", m.clone.err)
	}
	m = keys(t, m, "ctrl+u")
	m = typeText(t, m, "~/lumi/../etc")
	m = keys(t, m, "enter")
	if !strings.Contains(m.clone.err, "..") {
		t.Fatalf("traversal: %q", m.clone.err)
	}
	m = keys(t, m, "ctrl+u")
	m = typeText(t, m, "~/notas/dentro")
	m = keys(t, m, "enter")
	if !strings.Contains(m.clone.err, "Notas") || m.clone.running {
		t.Fatalf("inside another book: %q", m.clone.err)
	}
	if v := stripAnsi(m.View()); !strings.Contains(v, "✕ fica dentro do livro \"Notas\"") {
		t.Fatalf("inline error on the card:\n%s", v)
	}
	m = keys(t, m, "esc")
	if m.clone != nil || m.bk == nil {
		t.Fatal("Esc closes the card back to the books level")
	}

	// cancel while running: nothing registered, nothing left behind
	cs.mu.Lock()
	cs.block = make(chan struct{})
	cs.mu.Unlock()
	m = keys(t, m, "c")
	// press directly: the test reads the clone channel itself
	press := func(k string) {
		next, _ := m.Update(keyMsg(k))
		m = next.(Model)
	}
	press("enter")
	if !m.clone.running {
		t.Fatal("running")
	}
	if v := stripAnsi(m.View()); !strings.Contains(v, "clonando “test”") || !strings.Contains(v, "Esc cancelar") {
		t.Fatalf("progress card:\n%s", v)
	}
	press("esc")
	if !m.clone.cancelling {
		t.Fatal("Esc cancels")
	}
	close(cs.block)
	m = finishClone(t, m)
	if m.clone != nil || !strings.Contains(m.toastMsg, "cancelado") {
		t.Fatalf("cancelled: %+v toast %q", m.clone, m.toastMsg)
	}
	if _, err := os.Stat(filepath.Join(home, "lumi", "test")); !os.IsNotExist(err) {
		t.Fatal("a cancelled clone leaves no folder")
	}
	if st, _ := books.Open(); st.Registry.Get(testVault).Path != "" {
		t.Fatal("a cancelled clone registers nothing")
	}
}

func TestSignInThenClone(t *testing.T) {
	cs := newCloneServer(t)
	m, home := cloneEnv(t, cs.URL, "") // signed out: "precisa entrar"
	m = toServerBook(t, m)
	if !strings.Contains(stripAnsi(m.View()), "precisa entrar") {
		t.Fatal("state precisa entrar")
	}
	m = keys(t, m, "enter")
	if m.acct == nil || m.acct.form == nil || m.legendContext() != CtxLogin {
		t.Fatal("⏎ first opens the sign-in card")
	}
	f := m.acct.form
	if f.server != cs.URL || f.user != "ana" || f.field != 2 {
		t.Fatalf("prefilled form %+v", f)
	}
	// prefilled server is checked right away
	if f.status == nil || !f.status.ok || f.status.info.Name != "lumi-test" {
		t.Fatalf("server check %+v", f.status)
	}
	m = typeText(t, m, "wrong")
	m = keys(t, m, "enter")
	if !strings.Contains(m.acct.form.err, "inválidos") {
		t.Fatalf("wrong password: %q", m.acct.form.err)
	}
	m = typeText(t, m, "s3cret")
	m = keys(t, m, "enter")
	if m.acct != nil {
		t.Fatalf("sign-in should hand over to the clone card: %+v", m.acct.form)
	}
	if m.clone == nil || m.clone.input != "~/lumi/test" {
		t.Fatalf("clone card after sign-in: %+v", m.clone)
	}
	m = keys(t, m, "enter")
	m = finishClone(t, m)
	if m.SwitchTarget() != filepath.Join(home, "lumi", "test") {
		t.Fatalf("opened %q (card %+v)", m.SwitchTarget(), m.clone)
	}
	af, _ := account.Load()
	if a := af.Find(cs.URL, "ana"); a == nil || a.Token != "tok" {
		t.Fatal("token saved")
	}
}

func TestExpiredSessionDuringCloneSignsInAndResumes(t *testing.T) {
	cs := newCloneServer(t)
	m, home := cloneEnv(t, cs.URL, "stale") // looks signed in, server says 401
	m = toServerBook(t, m)
	m = keys(t, m, "c", "ctrl+u")
	m = typeText(t, m, "~/livros/teste")
	m = keys(t, m, "enter")
	m = finishClone(t, m)
	if m.acct == nil || m.acct.form == nil || !strings.Contains(m.acct.form.err, "sessão expirou") {
		t.Fatalf("401 → sign-in: %+v", m.acct)
	}
	if _, err := os.Stat(filepath.Join(home, "livros", "teste")); !os.IsNotExist(err) {
		t.Fatal("the failed attempt is cleaned up")
	}
	m = typeText(t, m, "s3cret")
	m = keys(t, m, "enter") // signs in, then the clone resumes and runs
	m = finishClone(t, m)
	if m.SwitchTarget() != filepath.Join(home, "livros", "teste") {
		t.Fatalf("opened %q", m.SwitchTarget())
	}
}

func TestLinkExistingFolder(t *testing.T) {
	cs := newCloneServer(t)
	m, home := cloneEnv(t, cs.URL, "tok")
	os.MkdirAll(filepath.Join(home, "old-test"), 0o755)
	m = toServerBook(t, m)
	m = keys(t, m, "L")
	m = typeText(t, m, "nope")
	m = keys(t, m, "enter")
	if !strings.Contains(m.clone.err, "não existe") {
		t.Fatalf("missing dir: %q", m.clone.err)
	}
	m = keys(t, m, "ctrl+u")
	m = typeText(t, m, "~/old-test")
	m = keys(t, m, "enter")
	if m.clone != nil || m.SwitchTarget() != filepath.Join(home, "old-test") {
		t.Fatalf("linked and opened: %+v %q", m.clone, m.SwitchTarget())
	}
	st, _ := books.Open()
	if st.Registry.Get(testVault).Path != "~/old-test" {
		t.Fatal("books.yaml path")
	}
}

func TestCloneCardGoldens(t *testing.T) {
	cs := newCloneServer(t)
	m, _ := cloneEnv(t, cs.URL, "tok")
	m.width, m.height = 100, 28
	b := m.bookMgr.Registry()[1]
	b.ServerURL = "https://lumi-api.example.com" // stable host in the golden
	m.openCloneCard(b, cloneKindClone)
	golden(t, "clone-prompt-100x28", m.View())
	f := m.clone
	f.running, f.phase, f.done, f.total, f.current = true, "baixando", 7, 20, "diario/2026-11-24.md"
	f.dest = filepath.Join(os.Getenv("HOME"), "lumi", "test")
	golden(t, "clone-progress-100x28", m.View())
}

func TestAccountsScreenHierarchyGolden(t *testing.T) {
	cs := newCloneServer(t)
	m, _ := cloneEnv(t, cs.URL, "tok")
	m = keys(t, m, "g", "a")
	s := m.acct
	if len(s.rows) != 3 || s.rows[0].kind != rowServer || s.rows[1].kind != rowAccount || s.rows[2].kind != rowBook {
		t.Fatalf("rows %+v", s.rows)
	}
	if st := s.servers[s.rows[0].server]; st == nil || !st.ok || st.info.Version != "2.9.0" {
		t.Fatalf("server checked on open: %+v", st)
	}
	// stable latency + host for the golden
	st := *s.servers[s.rows[0].server]
	st.latency = 42 * time.Millisecond
	s.servers[s.rows[0].server] = &st
	v := stripAnsi(m.View())
	for _, want := range []string{"SERVIDORES", "online · 42 ms · lumi-test 2.9.0", "Ana · ana", "sessão até 1 jan 2099", "1 livro", "sem pasta aqui"} {
		if !strings.Contains(v, want) {
			t.Errorf("accounts card missing %q", want)
		}
	}
	m = keys(t, m, "j", "j")
	if !strings.Contains(stripAnsi(m.View()), "c clonar livro") {
		t.Fatal("book row offers clone")
	}
	// click the account row, then its "s sair" button
	x, y := findText(t, m.View(), "Ana · ana")
	tm, _ := m.Update(tea.MouseMsg{X: x, Y: y, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
	m = tm.(Model)
	if m.acct.cursor != 1 {
		t.Fatalf("click selects the account row, cursor %d", m.acct.cursor)
	}
	x, y = findText(t, m.View(), "s sair")
	tm, _ = m.Update(tea.MouseMsg{X: x, Y: y, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
	m = tm.(Model)
	if m.acct.confirm != "signout" {
		t.Fatal("s sair button asks to confirm")
	}
	m = keys(t, m, "n")
	// book row: c hands over to the clone card
	m = keys(t, m, "j", "c")
	if m.acct != nil || m.clone == nil {
		t.Fatal("c on a book row opens the clone card")
	}
}

func TestAccountsGoldenStable(t *testing.T) {
	m := noteGoldenModel(t)
	m.width, m.height = 100, 30
	f := &account.File{}
	f.Upsert(account.Account{Server: "https://lumi-api.example.com", Username: "ana", DisplayName: "Ana", Token: "t",
		ExpiresAt: time.Date(2099, 1, 1, 12, 0, 0, 0, time.UTC)})
	f.Upsert(account.Account{Server: "https://lumi-api.example.com", Username: "bob"})
	f.Upsert(account.Account{Server: "http://192.168.0.10:8080", Username: "casa", Token: "t"})
	if err := f.Save(); err != nil {
		t.Fatal(err)
	}
	m.openAccounts()
	m.acct.needCheck = false
	m.acct.servers["https://lumi-api.example.com"] = &serverStatus{ok: true, latency: 38 * time.Millisecond,
		info: serverInfo{Name: "lumi", Version: "2.9.0"}}
	m.acct.servers["http://192.168.0.10:8080"] = &serverStatus{err: "servidor inacessível — sem conexão"}
	golden(t, "accounts-servers-100x30", m.View())

	m = keys(t, m, "a")
	m = typeText(t, m, "lumi.example.com")
	m.acct.form.status = &serverStatus{ok: true, latency: 21 * time.Millisecond, info: serverInfo{Name: "lumi", Version: "2.9.0"}}
	m = keys(t, m, "tab")
	m = typeText(t, m, "ana")
	m = keys(t, m, "tab")
	m = typeText(t, m, "hunter2")
	m.acct.form.err = "muitas tentativas — tente de novo em 2 min"
	golden(t, "login-card-error-100x30", m.View())
}

func TestLoginErrors(t *testing.T) {
	cases := map[string]error{
		"muitas tentativas — tente de novo em 2 min": &account.APIError{Status: 429, Code: "rate_limited", RetryAfter: 2 * time.Minute},
		"conta bloqueada — 15 min":                   &account.APIError{Status: 423, Code: "account_locked", RetryAfter: 15 * time.Minute},
		"conta bloqueada — 2 min":                    &account.APIError{Status: 423, Code: "account_locked", RetryAfter: 61 * time.Second},
		"conta bloqueada — menos de 1 min":           &account.APIError{Status: 423, Code: "account_locked", RetryAfter: 30 * time.Second},
		"conta bloqueada":                            &account.APIError{Status: 423, Code: "account_locked"},
		"usuário ou senha inválidos":                 &account.APIError{Status: 401, Code: "invalid_credentials"},
	}
	for want, err := range cases {
		if got := loginError(err); got != want {
			t.Errorf("%v → %q want %q", err, got, want)
		}
	}
	f := &loginForm{server: "lumi.example.com"}
	if u, bad := f.serverURL(); bad != "" || u != "https://lumi.example.com" {
		t.Fatalf("scheme assumed: %q %q", u, bad)
	}
	f.server = "http://lumi.example.com"
	if _, bad := f.serverURL(); !strings.Contains(bad, "http://") {
		t.Fatalf("plain http to a public host: %q", bad)
	}
}

func TestBooksDirSetting(t *testing.T) {
	cs := newCloneServer(t)
	m, _ := cloneEnv(t, cs.URL, "tok")
	m.cfg.BooksDir = "~/Livros"
	m = toServerBook(t, m)
	m = keys(t, m, "c")
	if m.clone.input != "~/Livros/test" {
		t.Fatalf("default follows books_dir: %q", m.clone.input)
	}
}
