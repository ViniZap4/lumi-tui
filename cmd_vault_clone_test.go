package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vinizap/lumi/tui-client/account"
)

func TestParseCloneArgs(t *testing.T) {
	cases := []struct {
		name    string
		args    []string
		wantErr bool
		want    cloneOpts
	}{
		{
			name: "shorthand server/slug",
			args: []string{"https://lumi.work.com/myvault"},
			want: cloneOpts{Server: "https://lumi.work.com", Slug: "myvault"},
		},
		{
			name: "shorthand + path",
			args: []string{"https://lumi.work.com/myvault", "./work"},
			want: cloneOpts{Server: "https://lumi.work.com", Slug: "myvault", Path: "./work"},
		},
		{
			name: "explicit flags",
			args: []string{"--server", "https://x.example", "--slug", "personal"},
			want: cloneOpts{Server: "https://x.example", Slug: "personal"},
		},
		{
			name: "explicit flags + path",
			args: []string{"--server", "https://x.example", "--slug", "personal", "./p"},
			want: cloneOpts{Server: "https://x.example", Slug: "personal", Path: "./p"},
		},
		{
			name: "force flag",
			args: []string{"https://lumi.work.com/v", "--force"},
			want: cloneOpts{Server: "https://lumi.work.com", Slug: "v", Force: true},
		},
		{
			name: "name flag",
			args: []string{"https://lumi.work.com/v", "--name", "Cool"},
			want: cloneOpts{Server: "https://lumi.work.com", Slug: "v", Name: "Cool"},
		},
		{
			name:    "missing slug",
			args:    []string{"https://lumi.work.com/"},
			wantErr: true,
		},
		{
			name:    "no scheme",
			args:    []string{"lumi.work.com/v"},
			wantErr: true,
		},
		{
			name:    "no args",
			args:    nil,
			wantErr: true,
		},
		{
			name:    "unknown flag",
			args:    []string{"https://x.example/v", "--bogus"},
			wantErr: true,
		},
		{
			name:    "server mismatch shorthand vs flag",
			args:    []string{"--server", "https://a.example", "https://b.example/v", "./p"},
			wantErr: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseCloneArgs(tc.args)
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

func TestSplitServerAndSlug(t *testing.T) {
	cases := map[string][2]string{
		"https://lumi.work.com/work":             {"https://lumi.work.com", "work"},
		"http://localhost:8080/personal":         {"http://localhost:8080", "personal"},
		"https://lumi.work.com/api/path/myslug":  {"https://lumi.work.com/api/path", "myslug"},
	}
	for in, want := range cases {
		s, sl, err := splitServerAndSlug(in)
		if err != nil {
			t.Errorf("splitServerAndSlug(%q): %v", in, err)
			continue
		}
		if s != want[0] || sl != want[1] {
			t.Errorf("splitServerAndSlug(%q) = (%q, %q), want %v", in, s, sl, want)
		}
	}
}

// TestRunVaultCloneCmd drives the full clone end-to-end against an
// httptest server. The server pretends to host one vault with two
// notes; the test asserts both notes hit disk + the registry entry is
// created.
func TestRunVaultCloneCmdHappyPath(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Lumi-Token") != "tok" {
			http.Error(w, "no token", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/vaults":
			_, _ = w.Write([]byte(`{"vaults":[{"id":"v1","slug":"work","name":"Work","created_by":"u1","created_at":"2026-05-25T10:00:00Z"}]}`))
		case "/api/vaults/v1/notes":
			_, _ = w.Write([]byte(`{"notes":[
				{"id":"n1","vault_id":"v1","path":"hello.md","title":"Hello","created_at":"","updated_at":""},
				{"id":"n2","vault_id":"v1","path":"sub/world.md","title":"World","created_at":"","updated_at":""}
			],"limit":200,"offset":0}`))
		case "/api/vaults/v1/notes/n1/content":
			_, _ = w.Write([]byte(`{"id":"n1","path":"hello.md","body":"# Hello\n"}`))
		case "/api/vaults/v1/notes/n2/content":
			_, _ = w.Write([]byte(`{"id":"n2","path":"sub/world.md","body":"# World\n"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	// Seed an account row for the test server.
	a := account.Account{
		Server:    srv.URL,
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

	target := filepath.Join(tmp, "work-clone")

	var stdout, stderr bytes.Buffer
	code := runVaultCloneCmd([]string{srv.URL + "/work", target}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, stderr.String())
	}

	for _, rel := range []string{"hello.md", "sub/world.md"} {
		path := filepath.Join(target, rel)
		body, err := os.ReadFile(path)
		if err != nil {
			t.Errorf("missing %s: %v", path, err)
			continue
		}
		if !strings.Contains(string(body), "#") {
			t.Errorf("file %s body looks wrong: %q", path, body)
		}
	}

	// Registry row created with the server binding.
	entries, _ := account.LoadVaults()
	if len(entries) != 1 {
		t.Fatalf("got %d vault rows, want 1", len(entries))
	}
	if entries[0].Server != srv.URL || entries[0].Account != "alice" {
		t.Errorf("registry row missing server binding: %+v", entries[0])
	}
}

func TestRunVaultCloneCmdMissingAccount(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	var stdout, stderr bytes.Buffer
	code := runVaultCloneCmd([]string{"https://no-account.example/whatever"}, &stdout, &stderr)
	if code == 0 {
		t.Fatalf("expected non-zero exit")
	}
	if !strings.Contains(stderr.String(), "no signed-in account") {
		t.Errorf("stderr missing missing-account hint:\n%s", stderr.String())
	}
}

func TestRunVaultCloneCmdSlugNotFound(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"vaults":[]}`))
	}))
	defer srv.Close()
	file := &account.File{}
	file.Upsert(account.Account{
		Server:    srv.URL,
		Username:  "alice",
		Token:     "tok",
		AddedAt:   time.Now(),
		ExpiresAt: time.Now().Add(time.Hour),
	})
	_ = file.Save()

	var stdout, stderr bytes.Buffer
	code := runVaultCloneCmd([]string{srv.URL + "/ghost"}, &stdout, &stderr)
	if code == 0 {
		t.Fatalf("expected non-zero exit")
	}
	if !strings.Contains(stderr.String(), "not found") {
		t.Errorf("stderr missing not-found hint:\n%s", stderr.String())
	}
}

func TestRunVaultCloneSkipsExistingFiles(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/vaults":
			_, _ = w.Write([]byte(`{"vaults":[{"id":"v1","slug":"v","name":"V","created_by":"u","created_at":""}]}`))
		case "/api/vaults/v1/notes":
			_, _ = w.Write([]byte(`{"notes":[{"id":"n1","vault_id":"v1","path":"x.md","title":"x","created_at":"","updated_at":""}],"limit":200,"offset":0}`))
		case "/api/vaults/v1/notes/n1/content":
			_, _ = w.Write([]byte(`{"id":"n1","path":"x.md","body":"REMOTE\n"}`))
		}
	}))
	defer srv.Close()
	file := &account.File{}
	file.Upsert(account.Account{
		Server: srv.URL, Username: "alice", Token: "tok",
		AddedAt: time.Now(), ExpiresAt: time.Now().Add(time.Hour),
	})
	_ = file.Save()

	target := filepath.Join(tmp, "v-clone")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(target, "x.md"), []byte("LOCAL\n"), 0o644); err != nil {
		t.Fatalf("seed local file: %v", err)
	}

	var stdout, stderr bytes.Buffer
	code := runVaultCloneCmd([]string{srv.URL + "/v", target}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, stderr.String())
	}
	body, _ := os.ReadFile(filepath.Join(target, "x.md"))
	if !strings.Contains(string(body), "LOCAL") {
		t.Fatalf("local file overwritten without --force: %q", body)
	}
	if !strings.Contains(stdout.String(), "skipped 1") {
		t.Errorf("stdout missing skip count: %s", stdout.String())
	}

	// With --force, the file should now be overwritten.
	stdout.Reset()
	stderr.Reset()
	code = runVaultCloneCmd([]string{srv.URL + "/v", target, "--force"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, stderr.String())
	}
	body, _ = os.ReadFile(filepath.Join(target, "x.md"))
	if !strings.Contains(string(body), "REMOTE") {
		t.Fatalf("--force did not overwrite: %q", body)
	}
}

func TestRunVaultCloneRejectsUnsafeNotePath(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/vaults":
			_, _ = w.Write([]byte(`{"vaults":[{"id":"v1","slug":"v","name":"V","created_by":"u","created_at":""}]}`))
		case "/api/vaults/v1/notes":
			// A malicious server tries to write outside the target dir.
			_, _ = w.Write([]byte(`{"notes":[{"id":"bad","vault_id":"v1","path":"../escape.md","title":"x","created_at":"","updated_at":""}],"limit":200,"offset":0}`))
		}
	}))
	defer srv.Close()
	file := &account.File{}
	file.Upsert(account.Account{
		Server: srv.URL, Username: "alice", Token: "tok",
		AddedAt: time.Now(), ExpiresAt: time.Now().Add(time.Hour),
	})
	_ = file.Save()

	var stdout, stderr bytes.Buffer
	code := runVaultCloneCmd([]string{srv.URL + "/v"}, &stdout, &stderr)
	if code == 0 {
		t.Fatalf("expected non-zero exit for traversal note path")
	}
	if !strings.Contains(stderr.String(), "unsafe note path") {
		t.Errorf("stderr missing traversal rejection:\n%s", stderr.String())
	}
}
