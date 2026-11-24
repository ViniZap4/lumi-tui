package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/vinizap/lumi/tui-client/account"
)

// sharedLoginServer is a minimal lumi-server for SPEC-LIVROS §8: login,
// device links (issue/redeem), logout with scope=device over link groups.
type sharedLoginServer struct {
	mu      sync.Mutex
	group   map[string]int    // token -> link group
	live    map[string]bool   // live tokens
	codes   map[string][2]any // code -> {challenge, issuer group}
	nextTok int
	scopes  []string
}

func newSharedLoginServer(t *testing.T) (*sharedLoginServer, *httptest.Server) {
	s := &sharedLoginServer{group: map[string]int{}, live: map[string]bool{}, codes: map[string][2]any{}}
	newTok := func(g int) string {
		s.nextTok++
		tok := "tok-" + string(rune('a'+s.nextTok))
		s.live[tok] = true
		s.group[tok] = g
		return tok
	}
	session := func(w http.ResponseWriter, tok string) {
		_ = json.NewEncoder(w).Encode(map[string]any{"token": tok, "expires_at": time.Now().Add(time.Hour).UTC().Format(time.RFC3339),
			"user": map[string]string{"id": "u1", "username": "alice", "display_name": "Alice"}})
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/auth/login", func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()
		session(w, newTok(s.nextTok+100))
	})
	mux.HandleFunc("/api/auth/device-links", func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()
		tok := r.Header.Get("X-Lumi-Token")
		if !s.live[tok] {
			w.WriteHeader(401)
			return
		}
		var body struct{ Challenge string }
		_ = json.NewDecoder(r.Body).Decode(&body)
		code := strings.Repeat("9", 60) + tok[len(tok)-1:] + "abc"
		s.codes[code] = [2]any{body.Challenge, s.group[tok]}
		w.WriteHeader(201)
		_ = json.NewEncoder(w).Encode(map[string]string{"code": code, "expires_at": time.Now().Add(2 * time.Minute).UTC().Format(time.RFC3339)})
	})
	mux.HandleFunc("/api/auth/device-links/redeem", func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()
		var body struct{ Code, Verifier string }
		_ = json.NewDecoder(r.Body).Decode(&body)
		c, ok := s.codes[body.Code]
		delete(s.codes, body.Code)
		if !ok || account.PKCEChallenge(body.Verifier) != c[0].(string) {
			w.WriteHeader(401)
			return
		}
		session(w, newTok(c[1].(int)))
	})
	mux.HandleFunc("/api/auth/logout", func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()
		tok := r.Header.Get("X-Lumi-Token")
		if !s.live[tok] {
			w.WriteHeader(401)
			return
		}
		var body struct{ Scope string }
		_ = json.NewDecoder(r.Body).Decode(&body)
		s.scopes = append(s.scopes, body.Scope)
		if body.Scope == "device" {
			for t, g := range s.group {
				if g == s.group[tok] {
					delete(s.live, t)
				}
			}
		}
		delete(s.live, tok)
		w.WriteHeader(204)
	})
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return s, ts
}

// fakeApp plays Lumi.app on the link channel: announces itself (with the
// test's own pid, so it is "running") and approves the TUI's requests
// with its session, or answers with errReply.
func fakeApp(t *testing.T, ctx context.Context, server, appToken, errReply string) {
	t.Helper()
	ch, err := account.NewLinkChannel()
	if err != nil {
		t.Fatal(err)
	}
	app := &account.LinkChannel{Dir: ch.Dir, Self: account.LinkClientApple}
	if err := app.WritePresence(); err != nil {
		t.Fatal(err)
	}
	go func() {
		c, _ := account.NewClient(server)
		c.SetToken(appToken)
		for ctx.Err() == nil {
			for _, r := range app.Pending() {
				if errReply != "" {
					_ = app.Respond(account.LinkResponse{ID: r.ID, Error: errReply})
					continue
				}
				link, err := c.IssueDeviceLink(ctx, r.Challenge, "Lumi.app")
				if err != nil {
					_ = app.Respond(account.LinkResponse{ID: r.ID, Error: "failed"})
					continue
				}
				_ = app.Respond(account.LinkResponse{ID: r.ID, Code: link.Code, ExpiresAt: link.ExpiresAt.UTC().Format(time.RFC3339)})
			}
			time.Sleep(10 * time.Millisecond)
		}
	}()
}

func writeRegistry(t *testing.T, home string, accs ...account.Account) string {
	t.Helper()
	path := filepath.Join(home, ".config", "lumi", "accounts.yaml")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, account.EncodeRegistry(accs), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

// The app signed in (account in the shared registry, no TUI token): a
// server command links through the app instead of failing.
func TestLookupAccountLinksFromApp(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	srv, ts := newSharedLoginServer(t)
	srv.live["app-token"], srv.group["app-token"] = true, 1
	writeRegistry(t, home, account.Account{Server: ts.URL, Username: "alice", AddedAt: time.Now()})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	fakeApp(t, ctx, ts.URL, "app-token", "")

	var stderr bytes.Buffer
	acc, code := lookupAccount(ts.URL, "", &stderr)
	if code != 0 || acc.Token == "" || acc.Token == "app-token" {
		t.Fatalf("code=%d acc=%+v stderr=%s", code, acc, stderr.String())
	}
	if !strings.Contains(stderr.String(), "approve it in the app") {
		t.Fatalf("stderr: %s", stderr.String())
	}
	f, _ := account.Load()
	if a := f.Find(ts.URL, "alice"); a == nil || a.Token != acc.Token {
		t.Fatalf("link not stored: %+v", a)
	}
	// Then "sign out on this Mac" from the TUI ends the app's session too.
	var out, errb bytes.Buffer
	if c := runAccountsCmd([]string{"signout", ts.URL, "--mac"}, &out, &errb); c != 0 {
		t.Fatalf("signout --mac: %d %s", c, errb.String())
	}
	if srv.live["app-token"] || srv.live[acc.Token] {
		t.Fatal("linked sessions still live after sign out on this Mac")
	}
	data, _ := os.ReadFile(filepath.Join(home, ".config", "lumi", "accounts.yaml"))
	if !strings.Contains(string(data), "signed_out_at: 20") {
		t.Fatalf("signed_out_at not recorded for Lumi.app:\n%s", data)
	}
}

func TestLookupAccountWithoutAppExplains(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	writeRegistry(t, home, account.Account{Server: "https://lumi.example", Username: "alice", AddedAt: time.Now()})
	var stderr bytes.Buffer
	if _, code := lookupAccount("https://lumi.example", "", &stderr); code != 1 ||
		!strings.Contains(stderr.String(), "not signed in as alice") {
		t.Fatalf("code=%d stderr=%s", code, stderr.String())
	}
}

func TestAccountsLinkDeniedThenSignOutLocalKeepsApp(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	srv, ts := newSharedLoginServer(t)
	srv.live["app-token"], srv.group["app-token"] = true, 1
	writeRegistry(t, home, account.Account{Server: ts.URL, Username: "alice", AddedAt: time.Now()})
	ctx, cancel := context.WithCancel(context.Background())
	fakeApp(t, ctx, ts.URL, "app-token", "denied")
	var out, errb bytes.Buffer
	if c := runAccountsCmd([]string{"link", ts.URL}, &out, &errb); c == 0 || !strings.Contains(errb.String(), "did not allow") {
		t.Fatalf("denied link: %d %s", c, errb.String())
	}
	cancel()
	f, _ := account.Load()
	if !f.LinkOptedOut(ts.URL, "alice") {
		t.Fatal("denial should be remembered")
	}

	// Now approve, then sign this terminal out only: the app's session lives.
	ctx2, cancel2 := context.WithCancel(context.Background())
	defer cancel2()
	fakeApp(t, ctx2, ts.URL, "app-token", "")
	out.Reset()
	errb.Reset()
	if c := runAccountsCmd([]string{"link", ts.URL}, &out, &errb); c != 0 {
		t.Fatalf("explicit link after denial: %d %s", c, errb.String())
	}
	if c := runAccountsCmd([]string{"signout", ts.URL}, &out, &errb); c != 0 {
		t.Fatalf("signout: %d %s", c, errb.String())
	}
	if !srv.live["app-token"] {
		t.Fatal("signing the terminal out ended the app's session")
	}
	f, _ = account.Load()
	if a := f.Find(ts.URL, "alice"); a == nil || a.Token != "" || !a.SignedOutAt.IsZero() || !f.LinkOptedOut(ts.URL, "alice") {
		t.Fatalf("after local sign out: %+v", a)
	}
}

// `lumi login` answers a pending Lumi.app request right away.
func TestLoginServesAppRequest(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	_, ts := newSharedLoginServer(t)
	ch, _ := account.NewLinkChannel()
	app := &account.LinkChannel{Dir: ch.Dir, Self: account.LinkClientApple}
	if err := app.WritePresence(); err != nil {
		t.Fatal(err)
	}
	req, verifier, err := app.WriteRequest(account.LinkClientTUI, ts.URL, "alice", "Lumi.app")
	if err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	if c := runLoginCmd([]string{ts.URL}, strings.NewReader("alice\nsecret\n"), &out, &errb); c != 0 {
		t.Fatalf("login: %d %s", c, errb.String())
	}
	if !strings.Contains(out.String(), "Lumi.app is signed in too") {
		t.Fatalf("stdout: %s", out.String())
	}
	resp, err := app.ReadResponse(req.ID)
	if err != nil || resp == nil || resp.Code == "" {
		t.Fatalf("resp %+v %v", resp, err)
	}
	c, _ := account.NewClient(ts.URL)
	if s, err := c.RedeemDeviceLink(context.Background(), resp.Code, verifier, "Lumi.app"); err != nil || s.Token == "" {
		t.Fatalf("app redeem: %v", err)
	}
	reg, _ := os.ReadFile(filepath.Join(home, ".config", "lumi", "accounts.yaml"))
	if strings.Contains(string(reg), "tok-") {
		t.Fatalf("token in the shared registry:\n%s", reg)
	}
}
