package feed

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/vinizap/lumi/tui-client/books"
)

const helloEv = "event: hello\ndata: {\"protocol\":1,\"vaults\":[{\"vault_id\":\"v\",\"seq\":3,\"can_edit\":true}]}\n\n"

func sseHeaders(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.WriteHeader(200)
	w.(http.Flusher).Flush()
}

func noJitter(d time.Duration) time.Duration { return d }

// collect runs Run until stop returns true or the deadline passes.
func collect(t *testing.T, cfg ConnConfig, stop func([]Update) bool) []Update {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out := make(chan Update, 64)
	done := make(chan struct{})
	go func() { Run(ctx, cfg, out); close(done) }()
	var got []Update
	for {
		select {
		case u := <-out:
			got = append(got, u)
			if stop(got) {
				cancel()
				<-done
				return got
			}
		case <-done:
			for {
				select {
				case u := <-out:
					got = append(got, u)
				default:
					return got
				}
			}
		case <-ctx.Done():
			t.Fatalf("timeout; updates so far: %+v", got)
		}
	}
}

func TestConnHeaderAuthEventsAndSessionEnded(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/lumi/api/events" || r.URL.RawQuery != "" {
			t.Errorf("bad URL %s (token must never be in the URL)", r.URL)
		}
		if r.Header.Get("Authorization") != "Bearer tok" || r.Header.Get("Accept") != "text/event-stream" {
			t.Errorf("headers %v", r.Header)
		}
		sseHeaders(w)
		fmt.Fprint(w, helloEv)
		fmt.Fprint(w, ": ping\n\nevent: vault.head\ndata: {\"vault_id\":\"V\",\"seq\":4}\n\n")
		fmt.Fprint(w, "event: session.ended\ndata: {\"reason\":\"revoked\"}\n\n")
	}))
	defer srv.Close()
	got := collect(t, ConnConfig{ServerURL: srv.URL + "/lumi/", Token: "tok"}, func([]Update) bool { return false })
	last := got[len(got)-1]
	if last.State != books.FeedAuthDead {
		t.Fatalf("want auth_dead last, got %+v", got)
	}
	var sawHello, sawHead bool
	for _, u := range got {
		if h, ok := u.Payload.(*Hello); ok && h.Vaults[0].Seq == 3 && u.State == books.FeedOpen {
			sawHello = true
		}
		if h, ok := u.Payload.(*VaultHead); ok && h.VaultID == "v" && h.Seq == 4 {
			sawHead = true
		}
	}
	if !sawHello || !sawHead {
		t.Fatalf("events missing: %+v", got)
	}
}

func TestConn401StopsWithoutRetry(t *testing.T) {
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.WriteHeader(401)
	}))
	defer srv.Close()
	got := collect(t, ConnConfig{ServerURL: srv.URL, Token: "x", BaseBackoff: time.Millisecond}, func([]Update) bool { return false })
	if got[len(got)-1].State != books.FeedAuthDead || atomic.LoadInt32(&hits) != 1 {
		t.Fatalf("401 must stop (hits=%d): %+v", hits, got)
	}
}

func TestConnMissingEndpointDegradesToDisabled(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	defer srv.Close()
	got := collect(t, ConnConfig{ServerURL: srv.URL, Token: "x", DisabledRetry: time.Hour}, func(u []Update) bool {
		return u[len(u)-1].State == books.FeedDisabled
	})
	if got[len(got)-1].RetryIn != time.Hour {
		t.Fatalf("%+v", got)
	}
}

func TestConnHTMLFallbackIsDisabled(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, "<html>")
	}))
	defer srv.Close()
	collect(t, ConnConfig{ServerURL: srv.URL, Token: "x"}, func(u []Update) bool {
		return u[len(u)-1].State == books.FeedDisabled
	})
}

func TestConnIdleTimeoutReconnectsWithBackoff(t *testing.T) {
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		sseHeaders(w)
		fmt.Fprint(w, helloEv)
		w.(http.Flusher).Flush()
		<-r.Context().Done() // then silence: no pings
	}))
	defer srv.Close()
	cfg := ConnConfig{ServerURL: srv.URL, Token: "x", IdleTimeout: 80 * time.Millisecond,
		BaseBackoff: 10 * time.Millisecond, Jitter: noJitter}
	got := collect(t, cfg, func(u []Update) bool { return atomic.LoadInt32(&hits) >= 2 && u[len(u)-1].State == books.FeedOpen })
	var backoffs []time.Duration
	for _, u := range got {
		if u.State == books.FeedBackoff {
			backoffs = append(backoffs, u.RetryIn)
		}
	}
	// hello resets the attempt counter, so every retry starts at base.
	if len(backoffs) == 0 || backoffs[0] != 10*time.Millisecond {
		t.Fatalf("backoffs %v", backoffs)
	}
}

func TestConnBackoffGrowsWithoutHello(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(500) }))
	defer srv.Close()
	cfg := ConnConfig{ServerURL: srv.URL, Token: "x", BaseBackoff: time.Millisecond, MaxBackoff: 4 * time.Millisecond, Jitter: noJitter}
	var backoffs []time.Duration
	collect(t, cfg, func(u []Update) bool {
		if l := u[len(u)-1]; l.State == books.FeedBackoff {
			backoffs = append(backoffs, l.RetryIn)
		}
		return len(backoffs) == 5
	})
	want := []time.Duration{1, 2, 4, 4, 4}
	for i := range want {
		if backoffs[i] != want[i]*time.Millisecond {
			t.Fatalf("backoffs %v", backoffs)
		}
	}
}

func TestConnRetryAfterAndReconnectEvent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "7")
		w.WriteHeader(429)
	}))
	defer srv.Close()
	got := collect(t, ConnConfig{ServerURL: srv.URL, Token: "x", Jitter: noJitter}, func(u []Update) bool {
		return u[len(u)-1].State == books.FeedBackoff
	})
	if got[len(got)-1].RetryIn != 7*time.Second {
		t.Fatalf("Retry-After not honoured: %+v", got[len(got)-1])
	}

	srv2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sseHeaders(w)
		fmt.Fprint(w, helloEv+"event: reconnect\ndata: {\"after_ms\":1500}\n\n")
	}))
	defer srv2.Close()
	got = collect(t, ConnConfig{ServerURL: srv2.URL, Token: "x", Jitter: func(time.Duration) time.Duration { return 0 }},
		func(u []Update) bool { return u[len(u)-1].State == books.FeedBackoff })
	if got[len(got)-1].RetryIn != 1500*time.Millisecond {
		t.Fatalf("reconnect after_ms: %+v", got[len(got)-1])
	}
}

func TestConnFirstEventMustBeHello(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sseHeaders(w)
		fmt.Fprint(w, "event: vault.head\ndata: {\"vault_id\":\"v\",\"seq\":1}\n\n")
	}))
	defer srv.Close()
	got := collect(t, ConnConfig{ServerURL: srv.URL, Token: "x"}, func(u []Update) bool {
		return u[len(u)-1].State == books.FeedBackoff
	})
	for _, u := range got {
		if u.State == books.FeedOpen {
			t.Fatal("must not open without hello")
		}
	}
}
