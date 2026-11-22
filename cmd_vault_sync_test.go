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
	"github.com/vinizap/lumi/tui-client/domain"
	"github.com/vinizap/lumi/tui-client/filesystem"
	"github.com/vinizap/lumi/tui-client/internal/fakeserver"
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

func seedAccount(t *testing.T, server string) {
	t.Helper()
	file, _ := account.Load()
	if file == nil {
		file = &account.File{}
	}
	file.Upsert(account.Account{Server: server, Username: "alice", Token: "tok",
		AddedAt: time.Now(), ExpiresAt: time.Now().Add(time.Hour)})
	if err := file.Save(); err != nil {
		t.Fatalf("save accounts: %v", err)
	}
}

func readBody(t *testing.T, path string) *domain.Note {
	t.Helper()
	n, err := filesystem.ReadNote(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return n
}

func writeBody(t *testing.T, path, body string) {
	t.Helper()
	n := readBody(t, path)
	n.Content = body
	if err := filesystem.WriteNote(n); err != nil {
		t.Fatal(err)
	}
}

// TestRunVaultSyncCmdThreeWay drives clone → concurrent remote + local
// edits → sync against a fake server with real snapshot/diff semantics.
// The remote edit must survive (vector V9a was the old LWW bug), a clean
// note picks up its remote change, a new remote note is pulled, a file
// without id is left alone, and a second sync is a no-op.
func TestRunVaultSyncCmdThreeWay(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	srv := fakeserver.New("v1", "work")
	defer srv.Close()
	seedAccount(t, srv.URL)
	srv.AddNote("n1", "existing.md", "hello world")
	srv.AddNote("n3", "clean.md", "clean v1")

	vaultDir := filepath.Join(tmp, "work")
	var stdout, stderr bytes.Buffer
	if code := runVaultCloneCmd([]string{srv.URL + "/work", vaultDir}, &stdout, &stderr); code != 0 {
		t.Fatalf("clone exit = %d, stderr = %s", code, stderr.String())
	}
	if strings.Contains(stderr.String(), "no sync base") {
		t.Fatalf("clone did not record bases: %s", stderr.String())
	}

	srv.RemoteEdit("n1", "# hello world") // web edits while we edit locally
	srv.RemoteEdit("n3", "clean v2")
	srv.AddNote("n2", "new.md", "# New from server")
	writeBody(t, filepath.Join(vaultDir, "existing.md"), "hello brave world")
	orphan := "# Just a markdown file with no frontmatter id\n"
	if err := os.WriteFile(filepath.Join(vaultDir, "orphan.md"), []byte(orphan), 0o644); err != nil {
		t.Fatal(err)
	}

	stdout.Reset()
	stderr.Reset()
	if code := runVaultSyncCmd([]string{srv.URL + "/work", vaultDir}, &stdout, &stderr); code != 0 {
		t.Fatalf("sync exit = %d, stderr = %s", code, stderr.String())
	}
	if got := srv.Text("n1"); got != "# hello brave world" {
		t.Fatalf("server text = %q (remote edit lost)", got)
	}
	ex := readBody(t, filepath.Join(vaultDir, "existing.md"))
	if ex.Content != "# hello brave world" || ex.ID != "n1" {
		t.Fatalf("existing.md = %+v", ex)
	}
	raw, _ := os.ReadFile(filepath.Join(vaultDir, "existing.md"))
	if !strings.HasPrefix(string(raw), "---\n") {
		t.Fatalf("frontmatter dropped:\n%s", raw)
	}
	if readBody(t, filepath.Join(vaultDir, "clean.md")).Content != "clean v2" {
		t.Fatal("clean note did not pick up the remote change")
	}
	if readBody(t, filepath.Join(vaultDir, "new.md")).Content != "# New from server" {
		t.Fatal("new remote note not pulled")
	}
	if o, _ := os.ReadFile(filepath.Join(vaultDir, "orphan.md")); string(o) != orphan {
		t.Fatalf("orphan.md modified:\n%s", o)
	}
	d := srv.Diffs()
	if len(d) != 1 || d[0].Origin != "tui" || d[0].BaseState == "" {
		t.Fatalf("diffs = %+v", d)
	}
	if !strings.Contains(stdout.String(), "Pushed 1; pulled 2") {
		t.Fatalf("summary: %s", stdout.String())
	}
	// No base or cache file landed in the notes folder.
	if _, err := os.Stat(filepath.Join(vaultDir, ".lumi")); err == nil {
		t.Fatal("sync state written into the notes folder")
	}

	stdout.Reset()
	stderr.Reset()
	if code := runVaultSyncCmd([]string{srv.URL + "/work", vaultDir}, &stdout, &stderr); code != 0 {
		t.Fatalf("second sync exit = %d, stderr = %s", code, stderr.String())
	}
	if len(srv.Diffs()) != 1 || !strings.Contains(stdout.String(), "Pushed 0; pulled 0") {
		t.Fatalf("second sync was not a no-op: %s", stdout.String())
	}
}

// TestRunVaultSyncCmdConflictKeepsBoth: a folder with no recorded base
// (synced by an older lumi) where both sides changed. Nothing is pushed;
// the server version lands in the note and the local one is kept.
func TestRunVaultSyncCmdConflictKeepsBoth(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	srv := fakeserver.New("v1", "work")
	defer srv.Close()
	seedAccount(t, srv.URL)
	srv.AddNote("n1", "existing.md", "server body")

	vaultDir := filepath.Join(tmp, "work")
	_ = os.MkdirAll(vaultDir, 0o755)
	existing := "---\nid: n1\ntitle: Existing\n---\n\nlocal body before sync\n"
	if err := os.WriteFile(filepath.Join(vaultDir, "existing.md"), []byte(existing), 0o644); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if code := runVaultSyncCmd([]string{srv.URL + "/work", vaultDir}, &stdout, &stderr); code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, stderr.String())
	}
	if len(srv.Diffs()) != 0 || srv.Text("n1") != "server body" {
		t.Fatalf("server overwritten: %q", srv.Text("n1"))
	}
	if readBody(t, filepath.Join(vaultDir, "existing.md")).Content != "server body" {
		t.Fatal("note does not hold the server version")
	}
	matches, _ := filepath.Glob(filepath.Join(vaultDir, "existing.conflict-*.md"))
	if len(matches) != 1 || readBody(t, matches[0]).Content != "local body before sync" || readBody(t, matches[0]).ID != "" {
		t.Fatalf("conflict copy = %v", matches)
	}
	if !strings.Contains(stderr.String(), "Conflict: existing.md") || !strings.Contains(stdout.String(), "1 conflict(s)") {
		t.Fatalf("conflict not surfaced:\nstdout %s\nstderr %s", stdout.String(), stderr.String())
	}
	// The local edit now sits in a local-only note; the next sync is clean.
	stdout.Reset()
	stderr.Reset()
	runVaultSyncCmd([]string{srv.URL + "/work", vaultDir}, &stdout, &stderr)
	if strings.Contains(stderr.String(), "Conflict") || len(srv.Diffs()) != 0 {
		t.Fatalf("second sync: %s", stderr.String())
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
