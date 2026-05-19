package sync

import (
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/vinizap/lumi/tui-client/config"
)

// wsURL must put the token on the query string and URL-encode it. The
// server validates `?token=` for WS handshakes; if we forget the
// encoding step, tokens with `+` or `&` characters silently break auth.
func TestWsURL_AppendsTokenQuery(t *testing.T) {
	c := &Client{cfg: &config.FolderConfig{
		ServerURL:   "https://lumi.example.com",
		ServerToken: "abc+xyz/123=",
	}}
	got, err := c.wsURL()
	if err != nil {
		t.Fatalf("wsURL: %v", err)
	}

	u, perr := url.Parse(got)
	if perr != nil {
		t.Fatalf("parse: %v", perr)
	}
	if u.Scheme != "wss" {
		t.Errorf("scheme = %q, want wss", u.Scheme)
	}
	if u.Path != "/ws" {
		t.Errorf("path = %q, want /ws", u.Path)
	}
	if got := u.Query().Get("token"); got != "abc+xyz/123=" {
		t.Errorf("token = %q, want abc+xyz/123=", got)
	}
}

// No token configured → no `?token=` on the URL. We can't smuggle the
// empty string as a credential.
func TestWsURL_OmitsEmptyToken(t *testing.T) {
	c := &Client{cfg: &config.FolderConfig{
		ServerURL:   "http://localhost:8080",
		ServerToken: "   ", // whitespace only
	}}
	got, _ := c.wsURL()
	if strings.Contains(got, "token=") {
		t.Errorf("unexpected token in url: %s", got)
	}
	if !strings.HasPrefix(got, "ws://") {
		t.Errorf("expected ws:// scheme, got %s", got)
	}
}

// Trailing slash on ServerURL must not produce `//ws`.
func TestWsURL_TrailingSlash(t *testing.T) {
	c := &Client{cfg: &config.FolderConfig{
		ServerURL: "http://localhost:8080/",
	}}
	got, _ := c.wsURL()
	if strings.Contains(got, "//ws") {
		t.Errorf("double slash in path: %s", got)
	}
	if got != "ws://localhost:8080/ws" {
		t.Errorf("url = %q, want ws://localhost:8080/ws", got)
	}
}

// Empty ServerURL means no sync is configured — wsURL returns the
// empty pair, and connect() bails. NewClient already screens for this,
// but the URL builder must not crash if it's called separately.
func TestWsURL_EmptyServer(t *testing.T) {
	c := &Client{cfg: &config.FolderConfig{}}
	got, err := c.wsURL()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "" {
		t.Errorf("expected empty string, got %q", got)
	}
}

// Backoff stays bounded across many attempts. Without the cap we'd
// overflow into negative durations after ~63 attempts.
func TestBackoffFor_Bounded(t *testing.T) {
	for attempt := 0; attempt < 200; attempt++ {
		d := backoffFor(attempt)
		if d <= 0 {
			t.Fatalf("backoffFor(%d) = %v, want > 0", attempt, d)
		}
		// 1.2× max accounts for the +20% jitter ceiling.
		if d > maxBackoff*12/10 {
			t.Fatalf("backoffFor(%d) = %v, exceeds cap %v", attempt, d, maxBackoff)
		}
	}
}

// Backoff grows for the first few attempts (we're not always at the
// cap). Floor below initialBackoff would mean negative jitter bit too
// hard.
func TestBackoffFor_Grows(t *testing.T) {
	for attempt := 0; attempt < 20; attempt++ {
		d := backoffFor(attempt)
		if d < initialBackoff {
			t.Fatalf("backoffFor(%d) = %v, below floor %v", attempt, d, initialBackoff)
		}
	}
}

// Stop is idempotent — calling it twice (or from multiple goroutines)
// must not panic on close-of-closed-channel.
func TestStop_Idempotent(t *testing.T) {
	c := NewClient(&config.FolderConfig{ServerURL: "http://x"})
	if c == nil {
		t.Fatal("NewClient returned nil")
	}
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c.Stop()
		}()
	}
	wg.Wait()
}

// sendStatus is non-blocking when the channel is full and stamps the
// current LostEvents count onto the outbound message.
func TestSendStatus_NonBlockingAndStamps(t *testing.T) {
	c := &Client{
		statusCh: make(chan StatusMsg, 1),
	}
	c.lost.Store(7)
	c.sendStatus(StatusMsg{Kind: StatusConnected}) // fills the buffer
	// Second send must not block even though the channel is full.
	done := make(chan struct{})
	go func() {
		c.sendStatus(StatusMsg{Kind: StatusDisconnected})
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("sendStatus blocked")
	}

	got := <-c.statusCh
	if got.LostEvents != 7 {
		t.Errorf("LostEvents = %d, want 7 (stamped from atomic)", got.LostEvents)
	}
}

// LostEvents stays in sync with the atomic the connect loop bumps.
func TestLostEvents_Accessor(t *testing.T) {
	c := &Client{}
	if got := c.LostEvents(); got != 0 {
		t.Fatalf("initial LostEvents = %d, want 0", got)
	}
	c.lost.Add(3)
	if got := c.LostEvents(); got != 3 {
		t.Errorf("LostEvents = %d, want 3", got)
	}
}

// Race smoke test for LostEvents: concurrent writers + readers via the
// atomic must not data-race (run with `go test -race`).
func TestLostEvents_Race(t *testing.T) {
	c := &Client{}
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c.lost.Add(1)
		}()
	}
	var readerDone atomic.Bool
	go func() {
		for !readerDone.Load() {
			_ = c.LostEvents()
		}
	}()
	wg.Wait()
	readerDone.Store(true)
	if got := c.LostEvents(); got != 50 {
		t.Errorf("LostEvents = %d, want 50", got)
	}
}
