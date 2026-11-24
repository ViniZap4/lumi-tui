package account

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeLinkServer implements the device-link endpoints of SPEC-LIVROS
// §8.4 with the server's rules (single use, PKCE S256, issuer token).
type fakeLinkServer struct {
	mu       sync.Mutex
	tokens   map[string]string // token -> username
	codes    map[string]string // code -> challenge
	codeUser map[string]string
	n        int
	revoked  []string
}

func newFakeLinkServer() (*fakeLinkServer, *httptest.Server) {
	f := &fakeLinkServer{tokens: map[string]string{}, codes: map[string]string{}, codeUser: map[string]string{}}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/auth/device-links", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		user, ok := f.tokens[r.Header.Get("X-Lumi-Token")]
		if !ok {
			w.WriteHeader(401)
			return
		}
		var body struct{ Challenge, Label string }
		_ = json.NewDecoder(r.Body).Decode(&body)
		if len(body.Challenge) != 43 {
			w.WriteHeader(400)
			return
		}
		f.n++
		code := strings.Repeat("c", 63) + string(rune('0'+f.n))
		f.codes[code] = body.Challenge
		f.codeUser[code] = user
		w.WriteHeader(201)
		_ = json.NewEncoder(w).Encode(map[string]string{"code": code, "expires_at": time.Now().Add(2 * time.Minute).UTC().Format(time.RFC3339)})
	})
	mux.HandleFunc("/api/auth/device-links/redeem", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		var body struct{ Code, Verifier, Label string }
		_ = json.NewDecoder(r.Body).Decode(&body)
		ch, ok := f.codes[body.Code]
		delete(f.codes, body.Code)
		if !ok || PKCEChallenge(body.Verifier) != ch {
			w.WriteHeader(401)
			_, _ = w.Write([]byte(`{"error":"invalid_link"}`))
			return
		}
		user := f.codeUser[body.Code]
		tok := "linked-" + body.Code[60:]
		f.tokens[tok] = user
		_ = json.NewEncoder(w).Encode(map[string]any{"token": tok, "expires_at": time.Now().Add(720 * time.Hour).UTC().Format(time.RFC3339),
			"user": map[string]string{"id": "uid-" + user, "username": user, "display_name": strings.ToUpper(user)}})
	})
	mux.HandleFunc("/api/auth/logout", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		tok := r.Header.Get("X-Lumi-Token")
		if _, ok := f.tokens[tok]; !ok {
			w.WriteHeader(401)
			return
		}
		delete(f.tokens, tok)
		f.revoked = append(f.revoked, tok)
		w.WriteHeader(204)
	})
	return f, httptest.NewServer(mux)
}

func testChannels(t *testing.T) (tui, app *LinkChannel, dir string) {
	t.Helper()
	dir = t.TempDir()
	linkDir := filepath.Join(dir, "link")
	alive := func(pid int) bool { return pid == 1001 || pid == 2002 }
	tui = &LinkChannel{Dir: linkDir, Self: LinkClientTUI, PID: 1001, PIDAlive: alive}
	app = &LinkChannel{Dir: linkDir, Self: LinkClientApple, PID: 2002, PIDAlive: alive}
	return tui, app, dir
}

func TestLinkFixturesDecode(t *testing.T) {
	var req LinkRequest
	if err := json.Unmarshal(fixture(t, "link-request.json"), &req); err != nil {
		t.Fatal(err)
	}
	if req.V != 1 || req.From != "tui" || req.To != "apple" || req.PID != 4242 || !validChallenge(req.Challenge) ||
		parseStamp(req.CreatedAt).IsZero() {
		t.Fatalf("request: %+v", req)
	}
	var resp LinkResponse
	if err := json.Unmarshal(fixture(t, "link-response.json"), &resp); err != nil || resp.ID != req.ID || len(resp.Code) != 64 {
		t.Fatalf("response: %+v %v", resp, err)
	}
	var denied LinkResponse
	if err := json.Unmarshal(fixture(t, "link-response-denied.json"), &denied); err != nil || denied.Error != "denied" {
		t.Fatalf("denied: %+v %v", denied, err)
	}
	var pkce struct{ Verifier, Challenge string }
	if err := json.Unmarshal(fixture(t, "link-pkce.json"), &pkce); err != nil || PKCEChallenge(pkce.Verifier) != pkce.Challenge {
		t.Fatalf("pkce: %+v", pkce)
	}
	// What we write round-trips into the same shape (seconds-only stamps).
	b, _ := json.Marshal(req)
	if string(b)+"\n" != string(fixture(t, "link-request.json")) {
		t.Fatalf("encoded request differs:\n%s", b)
	}
}

// The TUI holds a session; Lumi.app asks; the TUI answers without asking
// and the app redeems the code with its in-memory verifier.
func TestTUIServesAppRequest(t *testing.T) {
	srv, ts := newFakeLinkServer()
	defer ts.Close()
	srv.tokens["tui-token"] = "alice"
	tui, app, dir := testChannels(t)
	reg := filepath.Join(dir, "accounts.yaml")
	f := &File{}
	f.Upsert(Account{Server: ts.URL, Username: "alice", Token: "tui-token"})
	if err := f.SaveTo(reg); err != nil {
		t.Fatal(err)
	}
	f, _ = LoadFrom(reg)

	req, verifier, err := app.WriteRequest(LinkClientTUI, ts.URL, "alice", "Lumi.app")
	if err != nil {
		t.Fatal(err)
	}
	if info, _ := os.Stat(tui.Dir); info.Mode().Perm() != 0700 {
		t.Fatalf("link dir perm %o", info.Mode().Perm())
	}
	data, _ := os.ReadFile(filepath.Join(tui.Dir, "req-"+req.ID+".json"))
	if strings.Contains(string(data), verifier) {
		t.Fatal("verifier written to disk")
	}
	if n := ServeLinkRequests(context.Background(), tui, f, nil); n != 1 {
		t.Fatalf("served %d", n)
	}
	if len(tui.Pending()) != 0 {
		t.Fatal("answered request still pending")
	}
	resp, err := app.ReadResponse(req.ID)
	if err != nil || resp == nil || resp.Code == "" {
		t.Fatalf("resp %+v %v", resp, err)
	}
	c, _ := NewClient(ts.URL)
	// A process that read resp-*.json but lacks the verifier gets nothing.
	if _, err := c.RedeemDeviceLink(context.Background(), resp.Code, strings.Repeat("A", 43), "thief"); !errors.Is(err, ErrInvalidLink) {
		t.Fatalf("redeem without verifier: %v", err)
	}
	// And the code is spent.
	if _, err := c.RedeemDeviceLink(context.Background(), resp.Code, verifier, "Lumi.app"); !errors.Is(err, ErrInvalidLink) {
		t.Fatalf("code reused: %v", err)
	}
}

func TestTUIAnswersNoSession(t *testing.T) {
	_, ts := newFakeLinkServer()
	defer ts.Close()
	tui, app, _ := testChannels(t)
	f := &File{}
	f.Upsert(Account{Server: ts.URL, Username: "alice"}) // no TUI token
	req, _, _ := app.WriteRequest(LinkClientTUI, ts.URL, "alice", "Lumi.app")
	ServeLinkRequests(context.Background(), tui, f, nil)
	resp, _ := app.ReadResponse(req.ID)
	if resp == nil || resp.Error != "no_session" {
		t.Fatalf("resp %+v", resp)
	}
}

// The TUI asks Lumi.app (simulated holder that approves) and stores the
// resulting session; the registry and tokens.yaml are updated.
func TestTUIRequestLinkFromApp(t *testing.T) {
	srv, ts := newFakeLinkServer()
	defer ts.Close()
	srv.tokens["app-token"] = "alice"
	tui, app, dir := testChannels(t)
	reg := filepath.Join(dir, "accounts.yaml")
	if err := os.WriteFile(reg, EncodeRegistry([]Account{{Server: ts.URL, Username: "alice", AddedAt: time.Now()}}), 0600); err != nil {
		t.Fatal(err)
	}
	if err := app.WritePresence(); err != nil {
		t.Fatal(err)
	}
	f, _ := LoadFrom(reg)
	if len(f.NeedsLink()) != 1 {
		t.Fatalf("needs link: %+v", f.NeedsLink())
	}

	done := make(chan struct{})
	go func() { // the app: approve every request with its own session
		defer close(done)
		c, _ := NewClient(ts.URL)
		c.SetToken("app-token")
		for i := 0; i < 200; i++ {
			for _, r := range app.Pending() {
				link, err := c.IssueDeviceLink(context.Background(), r.Challenge, "Lumi.app")
				if err != nil {
					t.Error(err)
					return
				}
				_ = app.Respond(LinkResponse{ID: r.ID, Code: link.Code, ExpiresAt: stamp(link.ExpiresAt)})
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	a, err := LinkAndSave(ctx, tui, ts.URL, "alice", 10*time.Millisecond, nil)
	<-done
	if err != nil {
		t.Fatal(err)
	}
	if a.Token == "" || a.Token == "app-token" || a.DisplayName != "ALICE" {
		t.Fatalf("linked account: %+v", a)
	}
	again, _ := LoadFrom(reg)
	if got := again.Find(ts.URL, "alice"); got == nil || got.Token != a.Token {
		t.Fatalf("not persisted: %+v", got)
	}
	entries, _ := os.ReadDir(tui.Dir)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "req-") || strings.HasPrefix(e.Name(), "resp-") {
			t.Fatalf("leftover %s", e.Name())
		}
	}
}

func TestTUIRequestLinkDeniedSetsOptOut(t *testing.T) {
	_, ts := newFakeLinkServer()
	defer ts.Close()
	tui, app, dir := testChannels(t)
	reg := filepath.Join(dir, "accounts.yaml")
	_ = os.WriteFile(reg, EncodeRegistry([]Account{{Server: ts.URL, Username: "alice", AddedAt: time.Now()}}), 0600)
	_ = app.WritePresence()
	go func() {
		for i := 0; i < 200; i++ {
			for _, r := range app.Pending() {
				_ = app.Respond(LinkResponse{ID: r.ID, Error: "denied"})
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := LinkAndSave(ctx, tui, ts.URL, "alice", 10*time.Millisecond, nil); !errors.Is(err, ErrLinkDenied) {
		t.Fatalf("err = %v", err)
	}
	f, _ := LoadFrom(reg)
	if !f.LinkOptedOut(ts.URL, "alice") || len(f.NeedsLink()) != 0 {
		t.Fatal("denial should opt out of automatic link requests")
	}
}

func TestRequestLinkNeedsRunningApp(t *testing.T) {
	tui, _, _ := testChannels(t)
	if _, err := RequestLink(context.Background(), tui, "https://x.example", "alice", 0, nil); !errors.Is(err, ErrLinkNoPeer) {
		t.Fatalf("err = %v", err)
	}
	// A presence file whose process is gone doesn't count.
	dead := &LinkChannel{Dir: tui.Dir, Self: LinkClientApple, PID: 999, PIDAlive: tui.PIDAlive}
	_ = dead.WritePresence()
	if tui.PeerAlive(LinkClientApple) {
		t.Fatal("dead app reported alive")
	}
}

func TestPendingValidationAndGC(t *testing.T) {
	tui, app, _ := testChannels(t)
	now := time.Date(2026, 11, 24, 10, 0, 0, 0, time.UTC)
	tui.Now = func() time.Time { return now }
	app.Now = tui.Now

	good, _, _ := app.WriteRequest(LinkClientTUI, "https://x.example", "alice", "Lumi.app")
	// Wrong direction, bad challenge, and a stale request from a dead process.
	_ = tui.writeJSON("req-11111111-1111-4111-8111-111111111111.json", LinkRequest{V: 1, ID: "11111111-1111-4111-8111-111111111111", From: "tui", To: "tui", Server: "https://x", Username: "a", Challenge: good.Challenge, PID: 1001, CreatedAt: stamp(now)})
	_ = tui.writeJSON("req-22222222-2222-4222-8222-222222222222.json", LinkRequest{V: 1, ID: "22222222-2222-4222-8222-222222222222", From: "apple", To: "tui", Server: "https://x", Username: "a", Challenge: "short", PID: 2002, CreatedAt: stamp(now)})
	stale := LinkRequest{V: 1, ID: "33333333-3333-4333-8333-333333333333", From: "apple", To: "tui", Server: "https://x", Username: "a", Challenge: good.Challenge, PID: 777, CreatedAt: stamp(now.Add(-10 * time.Minute))}
	_ = tui.writeJSON("req-"+stale.ID+".json", stale)
	// Old but its requester (the app) is still running: still valid.
	live := stale
	live.ID, live.PID = "44444444-4444-4444-8444-444444444444", 2002
	_ = tui.writeJSON("req-"+live.ID+".json", live)

	got := tui.Pending()
	ids := map[string]bool{}
	for _, r := range got {
		ids[r.ID] = true
	}
	if len(got) != 2 || !ids[good.ID] || !ids[live.ID] {
		t.Fatalf("pending: %+v", got)
	}
	if _, err := os.Stat(filepath.Join(tui.Dir, "req-"+stale.ID+".json")); !os.IsNotExist(err) {
		t.Fatal("stale request not collected")
	}
	for _, name := range []string{"req-" + good.ID + ".json"} {
		if info, _ := os.Stat(filepath.Join(tui.Dir, name)); info.Mode().Perm() != 0600 {
			t.Fatalf("%s perm %o", name, info.Mode().Perm())
		}
	}
}

func TestLogoutDeviceSendsScope(t *testing.T) {
	var body map[string]string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.WriteHeader(204)
	}))
	defer ts.Close()
	c, _ := NewClient(ts.URL)
	c.SetToken("t")
	if err := c.LogoutDevice(context.Background()); err != nil || body["scope"] != "device" {
		t.Fatalf("err=%v body=%v", err, body)
	}
}
