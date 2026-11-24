package ui

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/vinizap/lumi/tui-client/account"
)

// linkServer wraps the clone server with the device-link endpoints and
// logout (SPEC-LIVROS §8.4): Lumi.app holds "apptok"; a redeemed code
// yields the clone server's token, so the linked session can clone.
type linkServer struct {
	*httptest.Server
	cs      *cloneServer
	mu      sync.Mutex
	codes   map[string]string // code -> challenge
	logouts []string          // "<token> <scope>"
}

func newLinkServer(t *testing.T) *linkServer {
	t.Helper()
	ls := &linkServer{cs: newCloneServer(t), codes: map[string]string{}}
	ls.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ls.mu.Lock()
		defer ls.mu.Unlock()
		switch r.URL.Path {
		case "/api/auth/device-links":
			if r.Header.Get("X-Lumi-Token") != "apptok" {
				w.WriteHeader(401)
				return
			}
			var body struct{ Challenge string }
			json.NewDecoder(r.Body).Decode(&body)
			code := strings.Repeat("ab", 32)
			ls.codes[code] = body.Challenge
			w.WriteHeader(201)
			json.NewEncoder(w).Encode(map[string]string{"code": code, "expires_at": time.Now().Add(2 * time.Minute).UTC().Format(time.RFC3339)})
		case "/api/auth/device-links/redeem":
			var body struct{ Code, Verifier string }
			json.NewDecoder(r.Body).Decode(&body)
			ch, ok := ls.codes[body.Code]
			delete(ls.codes, body.Code)
			if !ok || account.PKCEChallenge(body.Verifier) != ch {
				w.WriteHeader(401)
				w.Write([]byte(`{"error":"invalid_link"}`))
				return
			}
			w.Write([]byte(`{"token":"tok","expires_at":"2099-01-01T12:00:00Z","user":{"id":"u1","username":"ana","display_name":"Ana"}}`))
		case "/api/auth/logout":
			b, _ := io.ReadAll(r.Body)
			scope := "session"
			if strings.Contains(string(b), `"device"`) {
				scope = "device"
			}
			ls.logouts = append(ls.logouts, r.Header.Get("X-Lumi-Token")+" "+scope)
			w.WriteHeader(204)
		default:
			ls.cs.Config.Handler.ServeHTTP(w, r)
		}
	}))
	t.Cleanup(ls.Close)
	return ls
}

func (ls *linkServer) loggedOut() []string {
	ls.mu.Lock()
	defer ls.mu.Unlock()
	return append([]string(nil), ls.logouts...)
}

// linkChannels: this TUI's channel and a running Lumi.app (this test
// process is its pid, so it is alive).
func linkChannels(t *testing.T, home string) (tui, app *account.LinkChannel) {
	t.Helper()
	dir := filepath.Join(home, ".config", "lumi", "link")
	tui = &account.LinkChannel{Dir: dir, Self: account.LinkClientTUI}
	app = &account.LinkChannel{Dir: dir, Self: account.LinkClientApple}
	if err := app.WritePresence(); err != nil {
		t.Fatal(err)
	}
	return tui, app
}

// rowOf puts the accounts cursor on the account row of user.
func rowOf(t *testing.T, m Model, user string) Model {
	t.Helper()
	for i, r := range m.acct.rows {
		if r.kind == rowAccount && r.acct.Username == user {
			m.acct.cursor = i
			return m
		}
	}
	t.Fatalf("no account row %q in %+v", user, m.acct.rows)
	return m
}

func TestAccountsCardShowsLinkStates(t *testing.T) {
	m, home := cloneEnv(t, "https://lumi.example.com", "")
	f, _ := account.Load()
	f.Upsert(account.Account{Server: "https://lumi.example.com", Username: "ana", Token: "t", LinkedApp: true,
		ExpiresAt: time.Date(2099, 1, 1, 12, 0, 0, 0, time.UTC)})
	f.Upsert(account.Account{Server: "https://lumi.example.com", Username: "bob"})
	f.Upsert(account.Account{Server: "https://lumi.example.com", Username: "carol"})
	if err := f.Save(); err != nil {
		t.Fatal(err)
	}
	tui, _ := linkChannels(t, home)
	if _, _, err := tui.WriteRequest(account.LinkClientApple, "https://lumi.example.com", "bob", "lumi TUI"); err != nil {
		t.Fatal(err)
	}
	m.openAccounts()
	m.acct.needCheck = false
	v := stripAnsi(m.View())
	for _, want := range []string{"vinculada ao app · até 1 jan 2099", "aguardando aprovação no app"} {
		if !strings.Contains(v, want) {
			t.Fatalf("card lacks %q:\n%s", want, v)
		}
	}
	// carol: no session, app open, nothing pending → offer v.
	m = rowOf(t, m, "carol")
	m.width, m.height = 100, 30
	golden(t, "accounts-link-states-100x30", m.View())
	v = stripAnsi(m.View())
	if !strings.Contains(v, "v vincular ao app") || !strings.Contains(v, "v pede a sessão ao app") {
		t.Fatalf("carol should offer v:\n%s", v)
	}
	if strings.Contains(v, "s sair daqui") || !strings.Contains(v, "S sair neste Mac") {
		t.Fatalf("no TUI session: S only:\n%s", v)
	}
	// ana: linked session → s and S, and the hint says what each does.
	m = rowOf(t, m, "ana")
	v = stripAnsi(m.View())
	if !strings.Contains(v, "s sair daqui") || !strings.Contains(v, "S sair neste Mac") || strings.Contains(v, "v vincular") {
		t.Fatalf("ana buttons:\n%s", v)
	}
	// Book rows keep L = ligar pasta (as in the books level).
	if !strings.Contains(v, "c clonar · L ligar pasta") {
		t.Fatalf("book row hint:\n%s", v)
	}
	// Without the app, no v offer and v reports why.
	_, app := linkChannels(t, home)
	app.RemovePresence()
	m.reloadAccounts()
	m = rowOf(t, m, "carol")
	if strings.Contains(stripAnsi(m.View()), "v vincular ao app") {
		t.Fatal("v offered with the app closed")
	}
	m = keys(t, m, "v")
	if !strings.Contains(m.acct.err, "não está aberto") || m.acct.busy {
		t.Fatalf("v with the app closed: err %q busy %v", m.acct.err, m.acct.busy)
	}
}

func TestSignOutHereVersusOnThisMac(t *testing.T) {
	ls := newLinkServer(t)
	m, _ := cloneEnv(t, ls.URL, "tok-ana")
	f, _ := account.Load()
	f.Upsert(account.Account{Server: ls.URL, Username: "bob", Token: "tok-bob"})
	f.Upsert(account.Account{Server: ls.URL, Username: "carol", Token: "tok-carol"})
	if err := f.Save(); err != nil {
		t.Fatal(err)
	}
	m.openAccounts()
	m.acct.needCheck = false

	// s: this terminal only — session logout, row stays, no signed_out_at,
	// and the TUI won't ask the app on its own.
	m = rowOf(t, m, "ana")
	m = keys(t, m, "s")
	if !strings.Contains(stripAnsi(m.View()), "Sair só neste terminal?") {
		t.Fatal("s asks to sign out here")
	}
	m = keys(t, m, "y")
	f, _ = account.Load()
	ana := f.Find(ls.URL, "ana")
	if ana == nil || ana.Token != "" || !ana.SignedOutAt.IsZero() || !f.LinkOptedOut(ls.URL, "ana") {
		t.Fatalf("after s: %+v optout %v", ana, f.LinkOptedOut(ls.URL, "ana"))
	}
	if !strings.Contains(m.acct.info, "o Lumi.app continua conectado") {
		t.Fatalf("info %q", m.acct.info)
	}

	// S: the whole Mac — device logout, signed_out_at for Lumi.app.
	m = rowOf(t, m, "bob")
	m = keys(t, m, "S")
	if !strings.Contains(stripAnsi(m.View()), "Sair neste Mac (terminal e Lumi.app)?") {
		t.Fatal("S asks to sign out on this Mac")
	}
	m = keys(t, m, "y")
	f, _ = account.Load()
	bob := f.Find(ls.URL, "bob")
	if bob == nil || bob.Token != "" || bob.SignedOutAt.IsZero() {
		t.Fatalf("after S: %+v", bob)
	}

	// d: remove — device logout and the row goes (the app drops it too).
	m = rowOf(t, m, "carol")
	m = keys(t, m, "d", "y")
	f, _ = account.Load()
	if f.Find(ls.URL, "carol") != nil {
		t.Fatal("carol not removed")
	}

	deadline := time.Now().Add(3 * time.Second)
	for len(ls.loggedOut()) < 3 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	got := strings.Join(ls.loggedOut(), ",")
	for _, want := range []string{"tok-ana session", "tok-bob device", "tok-carol device"} {
		if !strings.Contains(got, want) {
			t.Fatalf("logouts %q lack %q", got, want)
		}
	}
}

// v asks the running Lumi.app for a session (it approves), the card shows
// the account as linked, and the book then clones with that session —
// no password typed.
func TestLinkWithAppThenClone(t *testing.T) {
	ls := newLinkServer(t)
	m, home := cloneEnv(t, ls.URL, "") // ana: no session in the TUI
	_, app := linkChannels(t, home)
	linkPoll = 10 * time.Millisecond
	defer func() { linkPoll = 0 }()

	m.openAccounts()
	m.acct.needCheck = false
	m = rowOf(t, m, "ana")
	next, cmd := m.Update(keyMsg("v"))
	m = next.(Model)
	if !m.acct.busy || cmd == nil || !m.acct.awaiting[linkKey(ls.URL, "ana")] {
		t.Fatal("v starts a device link")
	}
	if v := stripAnsi(m.View()); !strings.Contains(v, "aguardando aprovação no app") || !strings.Contains(v, "aprove no Lumi.app") {
		t.Fatalf("waiting state:\n%s", v)
	}

	go func() { // Lumi.app: the user clicks Permitir
		c, _ := account.NewClient(ls.URL)
		c.SetToken("apptok")
		for i := 0; i < 300; i++ {
			for _, r := range app.Pending() {
				link, err := c.IssueDeviceLink(context.Background(), r.Challenge, r.Label)
				if err != nil {
					t.Error(err)
					return
				}
				_ = app.Respond(account.LinkResponse{ID: r.ID, Code: link.Code, ExpiresAt: link.ExpiresAt.UTC().Format(time.RFC3339)})
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
	}()
	done := make(chan tea.Msg, 1)
	go func() { done <- cmd() }()
	var msg tea.Msg
	select {
	case msg = <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("link never finished")
	}
	next, _ = m.Update(msg)
	m = next.(Model)
	if m.acct == nil || m.acct.busy || m.acct.err != "" {
		t.Fatalf("after link: %+v", m.acct)
	}
	f, _ := account.Load()
	if a := f.Find(ls.URL, "ana"); a == nil || a.Token != "tok" || !a.LinkedApp {
		t.Fatalf("linked session not saved: %+v", a)
	}
	if v := stripAnsi(m.View()); !strings.Contains(v, "vinculada ao app") {
		t.Fatalf("card should show the link:\n%s", v)
	}

	// The book row now clones straight away (no sign-in card).
	m = keys(t, m, "j", "c")
	if m.acct != nil || m.clone == nil {
		t.Fatalf("c after the link should open the clone card (acct %+v)", m.acct)
	}
	m = keys(t, m, "enter")
	m = finishClone(t, m)
	if m.SwitchTarget() != filepath.Join(home, "lumi", "test") {
		t.Fatalf("opened %q (card %+v)", m.SwitchTarget(), m.clone)
	}
}

func TestLinkWithAppCancelAndDenied(t *testing.T) {
	ls := newLinkServer(t)
	m, home := cloneEnv(t, ls.URL, "")
	_, app := linkChannels(t, home)
	linkPoll = 10 * time.Millisecond
	defer func() { linkPoll = 0 }()
	m.openAccounts()
	m.acct.needCheck = false
	m = rowOf(t, m, "ana")

	// Esc while waiting cancels the request.
	next, cmd := m.Update(keyMsg("v"))
	m = next.(Model)
	next, _ = m.Update(keyMsg("esc"))
	m = next.(Model)
	next, _ = m.Update(cmd())
	m = next.(Model)
	if m.acct == nil || m.acct.busy || m.acct.info != "pedido ao Lumi.app cancelado" {
		t.Fatalf("cancel: %+v", m.acct)
	}
	if strings.Contains(stripAnsi(m.View()), "aguardando aprovação") {
		t.Fatal("cancelled request still shown as waiting")
	}

	// The app says no: error, and the TUI stops asking on its own.
	next, cmd = m.Update(keyMsg("v"))
	m = next.(Model)
	go func() {
		for i := 0; i < 300; i++ {
			for _, r := range app.Pending() {
				_ = app.Respond(account.LinkResponse{ID: r.ID, Error: "denied"})
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
	}()
	next, _ = m.Update(cmd())
	m = next.(Model)
	if !strings.Contains(m.acct.err, "não permitiu") || !strings.Contains(m.acct.err, "v tenta outra vez") {
		t.Fatalf("denied: %q", m.acct.err)
	}
	f, _ := account.Load()
	if !f.LinkOptedOut(ls.URL, "ana") {
		t.Fatal("denied link should opt out of automatic requests")
	}
}
