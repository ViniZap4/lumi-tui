package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/vinizap/lumi/tui-client/account"
)

func TestParseTransferArgs(t *testing.T) {
	cases := []struct {
		name    string
		args    []string
		wantErr bool
		want    transferOpts
	}{
		{
			name: "url slug username",
			args: []string{"https://lumi.work.com/myvault", "bob"},
			want: transferOpts{Server: "https://lumi.work.com", Slug: "myvault", Username: "bob"},
		},
		{
			name: "with --yes",
			args: []string{"https://lumi.work.com/myvault", "bob", "--yes"},
			want: transferOpts{Server: "https://lumi.work.com", Slug: "myvault", Username: "bob", Yes: true},
		},
		{
			name: "yes flag before positionals",
			args: []string{"--yes", "https://x.example/v", "carol"},
			want: transferOpts{Server: "https://x.example", Slug: "v", Username: "carol", Yes: true},
		},
		{
			name:    "missing username",
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
			args:    []string{"https://x.example/v", "bob", "--force"},
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
			got, err := parseTransferArgs(tc.args)
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

func TestPromptYesNo(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"y\n", true},
		{"Y\n", true},
		{"yes\n", true},
		{"YES\n", true},
		{"n\n", false},
		{"\n", false},
		{"", false}, // EOF without input → no
		{"whatever\n", false},
	}
	for _, tc := range cases {
		var out bytes.Buffer
		got, err := promptYesNo(strings.NewReader(tc.in), &out, "sure? ")
		if err != nil {
			t.Errorf("promptYesNo(%q): %v", tc.in, err)
			continue
		}
		if got != tc.want {
			t.Errorf("promptYesNo(%q) = %v, want %v", tc.in, got, tc.want)
		}
		if !strings.Contains(out.String(), "sure?") {
			t.Errorf("prompt not written: %q", out.String())
		}
	}
}

// seedTransferTestAccount writes an accounts.yaml row for `serverURL`
// under the test's fake $HOME so the command-under-test can resolve a
// token. Mirrors the setup in cmd_vault_sync_test.go.
func seedTransferTestAccount(t *testing.T, serverURL string) {
	t.Helper()
	a := account.Account{
		Server:    serverURL,
		Username:  "alice",
		Token:     "tok",
		AddedAt:   time.Now(),
		ExpiresAt: time.Now().Add(time.Hour),
	}
	file, _ := account.Load()
	if file == nil {
		file = &account.File{}
	}
	file.Upsert(a)
	if err := file.Save(); err != nil {
		t.Fatalf("save accounts: %v", err)
	}
}

// transferTestServer serves the three endpoints the transfer command
// touches: vault list, member list, transfer-ownership. `transferBody`
// captures the POSTed JSON.
func transferTestServer(t *testing.T, transferBody *string, transferCalled *bool) *httptest.Server {
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
		case r.URL.Path == "/api/vaults/v1/members":
			_, _ = w.Write([]byte(`{"members":[
				{"vault_id":"v1","user_id":"u1","username":"alice","display_name":"Alice","role_id":"r1","role_name":"Admin"},
				{"vault_id":"v1","user_id":"u2","username":"bob","display_name":"Bob","role_id":"r2","role_name":"Editor"}
			]}`))
		case r.URL.Path == "/api/vaults/v1/transfer-ownership" && r.Method == http.MethodPost:
			if transferCalled != nil {
				*transferCalled = true
			}
			raw, _ := io.ReadAll(r.Body)
			if transferBody != nil {
				*transferBody = string(raw)
			}
			_, _ = w.Write([]byte(`{"id":"v1","slug":"work","name":"Work","created_by":"u1","created_at":"2026-05-25T10:00:00Z","owner_user_id":"u2"}`))
		default:
			http.NotFound(w, r)
		}
	}))
}

func TestRunVaultTransferCmdHappyPath(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	var gotBody string
	srv := transferTestServer(t, &gotBody, nil)
	defer srv.Close()
	seedTransferTestAccount(t, srv.URL)

	var stdout, stderr bytes.Buffer
	code := runVaultTransferCmd(
		[]string{srv.URL + "/work", "bob", "--yes"},
		strings.NewReader(""), &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, stderr.String())
	}

	var sent map[string]string
	if err := json.Unmarshal([]byte(gotBody), &sent); err != nil {
		t.Fatalf("decode transfer body: %v", err)
	}
	if sent["user_id"] != "u2" {
		t.Errorf("user_id = %q, want u2 (bob)", sent["user_id"])
	}
	out := stdout.String()
	if !strings.Contains(out, "alice") || !strings.Contains(out, "bob") {
		t.Errorf("old->new owner confirmation missing from stdout: %s", out)
	}
	if !strings.Contains(out, "transferred") {
		t.Errorf("confirmation missing from stdout: %s", out)
	}
}

func TestRunVaultTransferCmdUsernameNotMember(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	transferCalled := false
	srv := transferTestServer(t, nil, &transferCalled)
	defer srv.Close()
	seedTransferTestAccount(t, srv.URL)

	var stdout, stderr bytes.Buffer
	code := runVaultTransferCmd(
		[]string{srv.URL + "/work", "mallory", "--yes"},
		strings.NewReader(""), &stdout, &stderr)
	if code == 0 {
		t.Fatalf("expected non-zero exit for non-member target")
	}
	if transferCalled {
		t.Error("transfer endpoint must not be hit for a non-member target")
	}
	msg := stderr.String()
	if !strings.Contains(msg, "not a member") {
		t.Errorf("error message missing 'not a member': %s", msg)
	}
	if !strings.Contains(msg, "mallory") {
		t.Errorf("error message missing the username: %s", msg)
	}
}

// TestRunVaultTransferCmdRefusesWithoutYesNonTTY: piped/scripted stdin
// without --yes must refuse rather than transfer or silently consume
// input. (Unit tests never run on a real TTY, so the non-interactive
// branch is the one exercised here.)
func TestRunVaultTransferCmdRefusesWithoutYesNonTTY(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	transferCalled := false
	srv := transferTestServer(t, nil, &transferCalled)
	defer srv.Close()
	seedTransferTestAccount(t, srv.URL)

	var stdout, stderr bytes.Buffer
	code := runVaultTransferCmd(
		[]string{srv.URL + "/work", "bob"},
		strings.NewReader("y\n"), &stdout, &stderr)
	if code == 0 {
		t.Fatalf("expected non-zero exit without --yes on non-TTY stdin")
	}
	if transferCalled {
		t.Error("transfer endpoint must not be hit without confirmation")
	}
	if !strings.Contains(stderr.String(), "--yes") {
		t.Errorf("error should point at --yes: %s", stderr.String())
	}
}

// TestRunVaultTransferCmdInteractiveConfirm forces the TTY branch via
// the stdinIsTerminal seam and drives the y/N prompt from a reader.
func TestRunVaultTransferCmdInteractiveConfirm(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	orig := stdinIsTerminal
	stdinIsTerminal = func() bool { return true }
	defer func() { stdinIsTerminal = orig }()

	t.Run("accepts y", func(t *testing.T) {
		transferCalled := false
		srv := transferTestServer(t, nil, &transferCalled)
		defer srv.Close()
		seedTransferTestAccount(t, srv.URL)

		var stdout, stderr bytes.Buffer
		code := runVaultTransferCmd(
			[]string{srv.URL + "/work", "bob"},
			strings.NewReader("y\n"), &stdout, &stderr)
		if code != 0 {
			t.Fatalf("exit = %d, stderr = %s", code, stderr.String())
		}
		if !transferCalled {
			t.Error("transfer endpoint not hit after y")
		}
		if !strings.Contains(stdout.String(), "[y/N]") {
			t.Errorf("prompt missing from stdout: %s", stdout.String())
		}
	})

	t.Run("aborts on n", func(t *testing.T) {
		transferCalled := false
		srv := transferTestServer(t, nil, &transferCalled)
		defer srv.Close()
		seedTransferTestAccount(t, srv.URL)

		var stdout, stderr bytes.Buffer
		code := runVaultTransferCmd(
			[]string{srv.URL + "/work", "bob"},
			strings.NewReader("n\n"), &stdout, &stderr)
		if code == 0 {
			t.Fatalf("expected non-zero exit on declined confirmation")
		}
		if transferCalled {
			t.Error("transfer endpoint must not be hit after n")
		}
		if !strings.Contains(stdout.String(), "Aborted") {
			t.Errorf("abort notice missing: %s", stdout.String())
		}
	})
}

func TestRunVaultTransferCmdAlreadyOwner(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	transferCalled := false
	srv := transferTestServer(t, nil, &transferCalled)
	defer srv.Close()
	seedTransferTestAccount(t, srv.URL)

	var stdout, stderr bytes.Buffer
	code := runVaultTransferCmd(
		[]string{srv.URL + "/work", "alice", "--yes"},
		strings.NewReader(""), &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, stderr.String())
	}
	if transferCalled {
		t.Error("transfer endpoint must not be hit when target already owns the vault")
	}
	if !strings.Contains(stdout.String(), "already owns") {
		t.Errorf("no-op notice missing: %s", stdout.String())
	}
}

func TestRunVaultTransferCmdMissingAccount(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	var stdout, stderr bytes.Buffer
	code := runVaultTransferCmd(
		[]string{"https://no-account.example/whatever", "bob", "--yes"},
		strings.NewReader(""), &stdout, &stderr)
	if code == 0 {
		t.Fatalf("expected non-zero exit, got 0")
	}
	if !strings.Contains(stderr.String(), "no signed-in account") {
		t.Errorf("error message missing 'no signed-in account': %s", stderr.String())
	}
}

func TestDescribeTransferError(t *testing.T) {
	forbidden := &account.APIError{Status: 403, Code: "forbidden"}
	if got := describeTransferError(forbidden); !strings.Contains(got, "owner") {
		t.Errorf("403 mapping = %q, want owner-only message", got)
	}
	badReq := &account.APIError{Status: 400, Code: "not_a_member", Detail: "target is not a member"}
	if got := describeTransferError(badReq); got != "target is not a member" {
		t.Errorf("400 mapping = %q, want detail passthrough", got)
	}
	if got := describeTransferError(account.ErrUnauthorized); !strings.Contains(got, "login") {
		t.Errorf("unauthorized mapping = %q, want login hint", got)
	}
}
