package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vinizap/lumi/tui-client/account"
)

func TestParseSyncArgs(t *testing.T) {
	cases := []struct {
		name    string
		args    []string
		wantErr bool
		want    syncOpts
	}{
		{
			name: "shorthand server/slug",
			args: []string{"https://lumi.work.com/myvault"},
			want: syncOpts{Server: "https://lumi.work.com", Slug: "myvault"},
		},
		{
			name: "shorthand + path",
			args: []string{"https://lumi.work.com/myvault", "./work"},
			want: syncOpts{Server: "https://lumi.work.com", Slug: "myvault", Path: "./work"},
		},
		{
			name: "explicit flags",
			args: []string{"--server", "https://x.example", "--slug", "personal"},
			want: syncOpts{Server: "https://x.example", Slug: "personal"},
		},
		{
			name: "dry-run flag",
			args: []string{"https://x.example/v", "--dry-run"},
			want: syncOpts{Server: "https://x.example", Slug: "v", DryRun: true},
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
			name:    "unknown flag",
			args:    []string{"https://x.example/v", "--bogus"},
			wantErr: true,
		},
		{
			name:    "no args",
			args:    nil,
			wantErr: true,
		},
		{
			name:    "server flag mismatch",
			args:    []string{"--server", "https://a.example", "https://b.example/v", "./p"},
			wantErr: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseSyncArgs(tc.args)
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

// TestAssertSafeRelPath locks the defensive check for server-supplied
// paths. The server should never emit traversal paths, but the sync
// loop refuses them as a belt-and-suspenders guard. This is a security
// boundary — keep it tested.
func TestAssertSafeRelPath(t *testing.T) {
	good := []string{"hello.md", "sub/world.md", "deeply/nested/file.md"}
	bad := []string{"", "../escape.md", "/absolute.md", "../../etc/passwd", "..", "sub/../../escape.md"}
	for _, p := range good {
		if err := assertSafeRelPath(p); err != nil {
			t.Errorf("safe path %q rejected: %v", p, err)
		}
	}
	for _, p := range bad {
		if err := assertSafeRelPath(p); err == nil {
			t.Errorf("unsafe path %q accepted", p)
		}
	}
}

// TestRunVaultSyncCmdEndToEnd exercises the full sync against an
// httptest server. Setup:
//   - The local vault directory contains:
//   - existing.md with id=n1 (server has matching id) -> push
//   - orphan.md with no frontmatter id -> skipped
//   - The server has:
//   - n1 (matches existing.md, server returns a "merged" body)
//   - n2 (not on disk) -> pulled
//
// After running we expect:
//   - existing.md's body replaced with the server's merged text
//   - new.md created on disk with the server snapshot
//   - orphan.md untouched
func TestRunVaultSyncCmdEndToEnd(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	var (
		gotDiffBody    string
		snapshotCalled bool
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Lumi-Token") != "tok" {
			http.Error(w, "no token", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/api/vaults":
			_, _ = w.Write([]byte(`{"vaults":[{"id":"v1","slug":"work","name":"Work","created_by":"u1","created_at":"2026-05-25T10:00:00Z"}]}`))
		case r.URL.Path == "/api/vaults/v1/notes":
			_, _ = w.Write([]byte(`{"notes":[
				{"id":"n1","vault_id":"v1","path":"existing.md","title":"Existing","created_at":"2026-05-01T10:00:00Z","updated_at":"2026-05-25T10:00:00Z"},
				{"id":"n2","vault_id":"v1","path":"new.md","title":"New","created_at":"2026-05-20T10:00:00Z","updated_at":"2026-05-22T10:00:00Z"}
			],"limit":200,"offset":0}`))
		case r.URL.Path == "/api/vaults/v1/notes/n1/diff" && r.Method == http.MethodPost:
			raw, _ := io.ReadAll(r.Body)
			gotDiffBody = string(raw)
			_, _ = w.Write([]byte(`{"id":"n1","path":"existing.md","text":"server-merged body\n","vector_clock":"YWJjZA=="}`))
		case r.URL.Path == "/api/vaults/v1/notes/n2/snapshot":
			snapshotCalled = true
			_, _ = w.Write([]byte(`{"id":"n2","path":"new.md","text":"# New from server\n","vector_clock":"eHl6"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

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

	vaultDir := filepath.Join(tmp, "work")
	if err := os.MkdirAll(vaultDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	existing := "---\nid: n1\ntitle: Existing\ncreated_at: 2026-05-01T10:00:00Z\nupdated_at: 2026-05-25T10:00:00Z\n---\n\nlocal body before sync\n"
	if err := os.WriteFile(filepath.Join(vaultDir, "existing.md"), []byte(existing), 0o644); err != nil {
		t.Fatalf("seed existing.md: %v", err)
	}
	orphan := "# Just a markdown file with no frontmatter id\n"
	if err := os.WriteFile(filepath.Join(vaultDir, "orphan.md"), []byte(orphan), 0o644); err != nil {
		t.Fatalf("seed orphan.md: %v", err)
	}

	var stdout, stderr bytes.Buffer
	code := runVaultSyncCmd([]string{srv.URL + "/work", vaultDir}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, stderr.String())
	}

	// existing.md: frontmatter preserved, body replaced with server's merge.
	got, err := os.ReadFile(filepath.Join(vaultDir, "existing.md"))
	if err != nil {
		t.Fatalf("read existing.md: %v", err)
	}
	if !strings.Contains(string(got), "server-merged body") {
		t.Errorf("existing.md body not replaced; got:\n%s", got)
	}
	if !strings.HasPrefix(string(got), "---\n") {
		t.Errorf("existing.md frontmatter dropped:\n%s", got)
	}

	// new.md: pulled and written.
	if _, err := os.Stat(filepath.Join(vaultDir, "new.md")); err != nil {
		t.Errorf("new.md not pulled: %v", err)
	}

	// orphan.md untouched.
	o2, _ := os.ReadFile(filepath.Join(vaultDir, "orphan.md"))
	if string(o2) != orphan {
		t.Errorf("orphan.md modified:\n%s", o2)
	}

	// Verify the diff payload was sent with the local body + origin label.
	var sent map[string]string
	if err := json.Unmarshal([]byte(gotDiffBody), &sent); err != nil {
		t.Fatalf("decode diff body: %v", err)
	}
	if !strings.Contains(sent["text"], "local body before sync") {
		t.Errorf("diff text didn't carry local body: %q", sent["text"])
	}
	if sent["origin"] != "tui-diff" {
		t.Errorf("origin = %q, want tui-diff", sent["origin"])
	}
	if !snapshotCalled {
		t.Error("snapshot endpoint never hit — pull pass skipped n2")
	}
}

// TestRunVaultSyncCmdDryRun confirms --dry-run doesn't write to disk
// and doesn't POST anything to the server.
func TestRunVaultSyncCmdDryRun(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	postSeen := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			postSeen = true
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/vaults":
			_, _ = w.Write([]byte(`{"vaults":[{"id":"v1","slug":"work","name":"Work","created_by":"u1","created_at":""}]}`))
		case "/api/vaults/v1/notes":
			_, _ = w.Write([]byte(`{"notes":[{"id":"n1","vault_id":"v1","path":"a.md","title":"A","created_at":"","updated_at":""}],"limit":200,"offset":0}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	a := account.Account{Server: srv.URL, Username: "u", Token: "tok", AddedAt: time.Now(), ExpiresAt: time.Now().Add(time.Hour)}
	file, _ := account.Load()
	if file == nil {
		file = &account.File{}
	}
	file.Upsert(a)
	if err := file.Save(); err != nil {
		t.Fatalf("save: %v", err)
	}

	vaultDir := filepath.Join(tmp, "work")
	_ = os.MkdirAll(vaultDir, 0o755)

	var stdout, stderr bytes.Buffer
	code := runVaultSyncCmd([]string{srv.URL + "/work", vaultDir, "--dry-run"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, stderr.String())
	}
	if postSeen {
		t.Error("dry-run should not POST anything")
	}
	if _, err := os.Stat(filepath.Join(vaultDir, "a.md")); err == nil {
		t.Error("dry-run should not write files")
	}
	if !strings.Contains(stdout.String(), "Dry-run") {
		t.Errorf("dry-run summary missing from stdout: %s", stdout.String())
	}
}

func TestRunVaultSyncCmdMissingAccount(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	var stdout, stderr bytes.Buffer
	code := runVaultSyncCmd([]string{"https://no-account.example/whatever"}, &stdout, &stderr)
	if code == 0 {
		t.Fatalf("expected non-zero exit, got 0")
	}
	if !strings.Contains(stderr.String(), "no signed-in account") {
		t.Errorf("error message missing 'no signed-in account': %s", stderr.String())
	}
}
