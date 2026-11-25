package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

// `lumi login` against a locked account (right password) prints the
// pt-BR lock line with the wait in minutes and saves nothing.
func TestLoginCmdLockedAccount(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/auth/login" {
			w.WriteHeader(404)
			return
		}
		w.Header().Set("Retry-After", "601")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusLocked)
		_, _ = w.Write([]byte(`{"error":"account_locked","retry_after":601}`))
	}))
	defer srv.Close()
	var out, errb bytes.Buffer
	code := runLoginCmd([]string{srv.URL}, strings.NewReader("ana\nright-password\n"), &out, &errb)
	if code != 1 {
		t.Fatalf("exit %d", code)
	}
	if got := errb.String(); !strings.Contains(got, "conta bloqueada — 11 min") {
		t.Fatalf("stderr %q", got)
	}
	if strings.Contains(out.String(), "Signed in") {
		t.Fatal("must not sign in")
	}
}
