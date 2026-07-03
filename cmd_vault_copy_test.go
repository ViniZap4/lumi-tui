package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestParseCopyArgs(t *testing.T) {
	cases := []struct {
		name    string
		args    []string
		wantErr bool
		want    copyOpts
	}{
		{
			name: "url slug recipient",
			args: []string{"https://lumi.work.com/myvault", "bob"},
			want: copyOpts{Server: "https://lumi.work.com", Slug: "myvault", Recipient: "bob"},
		},
		{
			name:    "missing recipient",
			args:    []string{"https://lumi.work.com/myvault"},
			wantErr: true,
		},
		{
			name:    "no scheme",
			args:    []string{"lumi.work.com/v", "bob"},
			wantErr: true,
		},
		{
			name:    "unknown flag",
			args:    []string{"https://x.example/v", "bob", "--yes"},
			wantErr: true,
		},
		{
			name:    "extra positional",
			args:    []string{"https://x.example/v", "bob", "carol"},
			wantErr: true,
		},
		{
			name:    "no args",
			args:    nil,
			wantErr: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseCopyArgs(tc.args)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error")
				}
				return
			}
			if err != nil {
				t.Fatalf("err = %v", err)
			}
			if got != tc.want {
				t.Fatalf("got %+v, want %+v", got, tc.want)
			}
		})
	}
}

// copyTestServer serves the vault list plus the copies endpoint.
// `status`+`response` control the copies reply; `copyBody` captures the
// POSTed JSON.
func copyTestServer(t *testing.T, status int, response string, copyBody *string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Lumi-Token") != "tok" {
			http.Error(w, "no token", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/api/vaults":
			_, _ = w.Write([]byte(`{"vaults":[{"id":"v1","slug":"work","name":"Work","created_by":"u1","created_at":"2026-05-25T10:00:00Z","owner_user_id":"u1"}]}`))
		case r.URL.Path == "/api/vaults/v1/copies" && r.Method == http.MethodPost:
			raw, _ := io.ReadAll(r.Body)
			if copyBody != nil {
				*copyBody = string(raw)
			}
			w.WriteHeader(status)
			_, _ = w.Write([]byte(response))
		default:
			http.NotFound(w, r)
		}
	}))
}

func TestRunVaultCopyCmdHappyPath(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	var gotBody string
	srv := copyTestServer(t, http.StatusCreated,
		`{"id":"v9","slug":"work-copy","name":"Work","created_by":"u1","created_at":"2026-07-02T10:00:00Z","owner_user_id":"u2","copied_from":{"vault_id":"v1"}}`,
		&gotBody)
	defer srv.Close()
	seedTransferTestAccount(t, srv.URL)

	var stdout, stderr bytes.Buffer
	code := runVaultCopyCmd([]string{srv.URL + "/work", "bob"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, stderr.String())
	}

	var sent map[string]string
	if err := json.Unmarshal([]byte(gotBody), &sent); err != nil {
		t.Fatalf("decode copy body: %v", err)
	}
	if sent["recipient_username"] != "bob" {
		t.Errorf("recipient_username = %q, want bob", sent["recipient_username"])
	}
	out := stdout.String()
	if !strings.Contains(out, "work-copy") {
		t.Errorf("fork slug missing from stdout: %s", out)
	}
	if !strings.Contains(out, "bob") {
		t.Errorf("new owner missing from stdout: %s", out)
	}
	if !strings.Contains(out, "independent") {
		t.Errorf("divergence notice missing from stdout: %s", out)
	}
}

func TestRunVaultCopyCmdRecipientNotFound(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	srv := copyTestServer(t, http.StatusBadRequest, `{"error":"recipient_not_found"}`, nil)
	defer srv.Close()
	seedTransferTestAccount(t, srv.URL)

	var stdout, stderr bytes.Buffer
	code := runVaultCopyCmd([]string{srv.URL + "/work", "ghost"}, &stdout, &stderr)
	if code == 0 {
		t.Fatalf("expected non-zero exit for unknown recipient")
	}
	msg := stderr.String()
	if !strings.Contains(msg, "no user named \"ghost\"") {
		t.Errorf("recipient_not_found not mapped to a clear message: %s", msg)
	}
}

func TestRunVaultCopyCmdForbidden(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	srv := copyTestServer(t, http.StatusForbidden, `{"error":"forbidden"}`, nil)
	defer srv.Close()
	seedTransferTestAccount(t, srv.URL)

	var stdout, stderr bytes.Buffer
	code := runVaultCopyCmd([]string{srv.URL + "/work", "bob"}, &stdout, &stderr)
	if code == 0 {
		t.Fatalf("expected non-zero exit for forbidden")
	}
	if !strings.Contains(stderr.String(), "vault.export") {
		t.Errorf("forbidden not mapped to capability hint: %s", stderr.String())
	}
}

func TestRunVaultCopyCmdSlugNotFound(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	srv := copyTestServer(t, http.StatusCreated, `{}`, nil)
	defer srv.Close()
	seedTransferTestAccount(t, srv.URL)

	var stdout, stderr bytes.Buffer
	code := runVaultCopyCmd([]string{srv.URL + "/nope", "bob"}, &stdout, &stderr)
	if code == 0 {
		t.Fatalf("expected non-zero exit for unknown slug")
	}
	if !strings.Contains(stderr.String(), "not found") {
		t.Errorf("slug-not-found message missing: %s", stderr.String())
	}
}

func TestRunVaultCopyCmdMissingAccount(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	var stdout, stderr bytes.Buffer
	code := runVaultCopyCmd([]string{"https://no-account.example/whatever", "bob"}, &stdout, &stderr)
	if code == 0 {
		t.Fatalf("expected non-zero exit, got 0")
	}
	if !strings.Contains(stderr.String(), "no signed-in account") {
		t.Errorf("error message missing 'no signed-in account': %s", stderr.String())
	}
}
