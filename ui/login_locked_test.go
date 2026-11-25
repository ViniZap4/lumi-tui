package ui

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The accounts card's sign-in against a locked account (right password,
// HTTP 423) shows "conta bloqueada — N min" inline and keeps the card.
func TestAccountsSignInLockedAccount(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/auth/login" {
			w.WriteHeader(404)
			return
		}
		w.Header().Set("Retry-After", "840")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusLocked)
		_, _ = w.Write([]byte(`{"error":"account_locked","retry_after":840}`))
	}))
	defer srv.Close()
	m := newTreeModel(t, notesVault(t))
	m = keys(t, m, "g", "a", "a")
	m = typeText(t, m, srv.URL)
	m = keys(t, m, "tab")
	m = typeText(t, m, "ana")
	m = keys(t, m, "tab")
	m = typeText(t, m, "right-password")
	m = keys(t, m, "enter")
	if m.acct == nil || m.acct.form == nil {
		t.Fatal("the card stays open")
	}
	if got := m.acct.form.err; got != "conta bloqueada — 14 min" {
		t.Fatalf("form error %q", got)
	}
	if v := stripAnsi(m.View()); !strings.Contains(v, "conta bloqueada — 14 min") || strings.Contains(v, "right-password") {
		t.Fatalf("screen:\n%s", v)
	}
}
