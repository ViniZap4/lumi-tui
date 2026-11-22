package feed

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"strings"
	"time"

	"github.com/vinizap/lumi/tui-client/account"
	"github.com/vinizap/lumi/tui-client/books"
)

// Defaults from SPEC-LIVROS §3.3 / §4.2.
const (
	DefaultIdleTimeout   = 60 * time.Second // no bytes for 60 s = dead (pings every 25 s)
	DefaultBaseBackoff   = 1 * time.Second
	DefaultMaxBackoff    = 30 * time.Second
	DefaultDisabledRetry = 5 * time.Minute // server without /api/events: probe rarely
	maxEventBytes        = 1 << 20
)

// ConnConfig configures one account's stream.
type ConnConfig struct {
	ServerURL string
	Token     string
	// HTTP must not set Timeout (it would cut the stream); the idle
	// watchdog replaces it. Nil uses a dedicated client.
	HTTP          *http.Client
	IdleTimeout   time.Duration
	BaseBackoff   time.Duration
	MaxBackoff    time.Duration
	DisabledRetry time.Duration
	// Jitter maps a delay to a jittered delay. Nil = uniform in [d/2, d].
	Jitter func(time.Duration) time.Duration
}

func (c *ConnConfig) defaults() {
	if c.HTTP == nil {
		c.HTTP = &http.Client{}
	}
	if c.IdleTimeout <= 0 {
		c.IdleTimeout = DefaultIdleTimeout
	}
	if c.BaseBackoff <= 0 {
		c.BaseBackoff = DefaultBaseBackoff
	}
	if c.MaxBackoff <= 0 {
		c.MaxBackoff = DefaultMaxBackoff
	}
	if c.DisabledRetry <= 0 {
		c.DisabledRetry = DefaultDisabledRetry
	}
	if c.Jitter == nil {
		c.Jitter = func(d time.Duration) time.Duration {
			if d <= 1 {
				return d
			}
			return d/2 + time.Duration(rand.Int63n(int64(d/2)+1))
		}
	}
}

// Update is what a running Conn reports: a state change, an event, or
// both. Payload is the decoded event (see Decode); nil for state-only
// updates.
type Update struct {
	State   books.FeedState
	Event   string
	Payload any
	Err     error         // last connection error, for the details panel
	RetryIn time.Duration // next attempt delay while in backoff/disabled
}

// Backoff computes the n-th (0-based) retry delay before jitter:
// base·2ⁿ capped at max.
func Backoff(n int, base, max time.Duration) time.Duration {
	d := base
	for i := 0; i < n && d < max; i++ {
		d *= 2
	}
	if d > max {
		d = max
	}
	return d
}

// errAuthDead / errDisabled / errReconnect steer the run loop.
var (
	errAuthDead = errors.New("session ended")
	errDisabled = errors.New("server has no /api/events")
)

type reconnectAfter struct{ d time.Duration }

func (r reconnectAfter) Error() string { return fmt.Sprintf("server asked to reconnect in %s", r.d) }

type retryAfterErr struct {
	status int
	after  time.Duration
}

func (r retryAfterErr) Error() string { return fmt.Sprintf("HTTP %d", r.status) }

// Run keeps one account's stream open until ctx ends, the session dies
// (401 / session.ended), sending every Update to out. It never returns
// early on transient errors.
func Run(ctx context.Context, cfg ConnConfig, out chan<- Update) {
	cfg.defaults()
	send := func(u Update) bool {
		select {
		case out <- u:
			return true
		case <-ctx.Done():
			return false
		}
	}
	attempt := 0
	for ctx.Err() == nil {
		if !send(Update{State: books.FeedConnecting}) {
			return
		}
		opened, err := stream(ctx, cfg, send)
		if ctx.Err() != nil {
			return
		}
		if opened {
			attempt = 0
		}
		var wait time.Duration
		state := books.FeedBackoff
		var ra reconnectAfter
		var rae retryAfterErr
		switch {
		case errors.Is(err, errAuthDead):
			send(Update{State: books.FeedAuthDead, Err: err})
			return
		case errors.Is(err, errDisabled):
			state, wait = books.FeedDisabled, cfg.DisabledRetry
		case errors.As(err, &ra):
			// A server-requested reconnect is "connecting", not a failure
			// (web resolution): no backoff growth, no Offline flash cause.
			state, wait = books.FeedConnecting, ra.d+cfg.Jitter(time.Second)
		default:
			wait = cfg.Jitter(Backoff(attempt, cfg.BaseBackoff, cfg.MaxBackoff))
			attempt++
			if errors.As(err, &rae) && rae.after > wait {
				wait = rae.after
			}
		}
		if !send(Update{State: state, Err: err, RetryIn: wait}) {
			return
		}
		t := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			t.Stop()
			return
		case <-t.C:
		}
	}
}

// stream performs one connection. opened reports whether `hello` arrived.
func stream(ctx context.Context, cfg ConnConfig, send func(Update) bool) (opened bool, err error) {
	reqCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	base := strings.TrimRight(cfg.ServerURL, "/")
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, base+"/api/events", nil)
	if err != nil {
		return false, err
	}
	// Token only in a header — never in the URL (§3.2).
	req.Header.Set("Authorization", "Bearer "+cfg.Token)
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Cache-Control", "no-store")

	// Idle watchdog covers the handshake too.
	idle := time.AfterFunc(cfg.IdleTimeout, cancel)
	defer idle.Stop()

	resp, err := cfg.HTTP.Do(req)
	if err != nil {
		if reqCtx.Err() != nil && ctx.Err() == nil {
			return false, fmt.Errorf("no response in %s", cfg.IdleTimeout)
		}
		return false, fmt.Errorf("network: %w", err)
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusUnauthorized:
		return false, errAuthDead
	case resp.StatusCode == http.StatusNotFound, resp.StatusCode == http.StatusMethodNotAllowed,
		resp.StatusCode == http.StatusNotImplemented:
		return false, errDisabled
	case resp.StatusCode == http.StatusTooManyRequests, resp.StatusCode == http.StatusServiceUnavailable:
		return false, retryAfterErr{status: resp.StatusCode, after: account.ParseRetryAfter(resp.Header.Get("Retry-After"), time.Now())}
	case resp.StatusCode != http.StatusOK:
		return false, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		// An SPA fallback or proxy page answering 200 with HTML.
		return false, errDisabled
	}

	var p Parser
	buf := make([]byte, 32*1024)
	for {
		n, rerr := resp.Body.Read(buf)
		if n > 0 {
			idle.Reset(cfg.IdleTimeout)
			for _, ev := range p.Feed(buf[:n]) {
				payload, derr := Decode(ev)
				if derr != nil {
					return opened, derr
				}
				if payload == nil {
					continue
				}
				if !opened {
					if ev.Name != EvHello {
						return false, fmt.Errorf("first event was %q, want hello", ev.Name)
					}
					opened = true
				}
				switch t := payload.(type) {
				case *SessionEnded:
					send(Update{State: books.FeedOpen, Event: ev.Name, Payload: t})
					return opened, errAuthDead
				case *Reconnect:
					return opened, reconnectAfter{d: time.Duration(t.AfterMS) * time.Millisecond}
				}
				if !send(Update{State: books.FeedOpen, Event: ev.Name, Payload: payload}) {
					return opened, ctx.Err()
				}
			}
			if len(p.buf) > maxEventBytes {
				return opened, errors.New("event too large")
			}
		}
		if rerr != nil {
			if ctx.Err() != nil {
				return opened, ctx.Err()
			}
			if reqCtx.Err() != nil {
				return opened, fmt.Errorf("no bytes for %s", cfg.IdleTimeout)
			}
			if rerr == io.EOF {
				return opened, errors.New("stream closed by server")
			}
			return opened, fmt.Errorf("network: %w", rerr)
		}
	}
}
