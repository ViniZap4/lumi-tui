package account

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// lockedServer answers every sign-in with the server's 423 lock reply.
func lockedServer(t *testing.T, header string, body string) *Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if header != "" {
			w.Header().Set("Retry-After", header)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusLocked)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	c, err := NewClient(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestLoginLockedAccount(t *testing.T) {
	cases := []struct {
		name, header, body string
		wait               time.Duration
		msg                string
	}{
		{"header and body", "900", `{"error":"account_locked","retry_after":900}`, 15 * time.Minute, "conta bloqueada — 15 min"},
		{"body only, rounds up", "", `{"error":"account_locked","retry_after":61}`, 61 * time.Second, "conta bloqueada — 2 min"},
		{"header only", "120", `{"error":"account_locked"}`, 2 * time.Minute, "conta bloqueada — 2 min"},
		{"under a minute", "30", `{"error":"account_locked","retry_after":30}`, 30 * time.Second, "conta bloqueada — menos de 1 min"},
		{"no wait known", "", `{"error":"account_locked"}`, 0, "conta bloqueada"},
		{"bare 423", "", ``, 0, "conta bloqueada"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := lockedServer(t, tc.header, tc.body)
			_, err := c.Login(context.Background(), "ana", "right-password")
			wait, ok := Locked(err)
			if !ok {
				t.Fatalf("not seen as locked: %v", err)
			}
			if wait != tc.wait {
				t.Fatalf("wait %v want %v", wait, tc.wait)
			}
			if got := LockedMessage(wait); got != tc.msg {
				t.Fatalf("message %q want %q", got, tc.msg)
			}
		})
	}
}

func TestLockedIgnoresOtherErrors(t *testing.T) {
	for _, err := range []error{
		ErrUnauthorized,
		&APIError{Status: 429, Code: "rate_limited", RetryAfter: time.Minute},
		&APIError{Status: 403, Code: "account_disabled"},
	} {
		if _, ok := Locked(err); ok {
			t.Errorf("%v seen as locked", err)
		}
	}
}
