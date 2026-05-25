package main

import (
	"bytes"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/vinizap/lumi/tui-client/account"
)

func TestParseVaultLinkArgs(t *testing.T) {
	cases := []struct {
		name    string
		args    []string
		wantErr bool
		want    vaultLinkOpts
	}{
		{
			name: "bare path",
			args: []string{"/tmp/foo"},
			want: vaultLinkOpts{Path: "/tmp/foo"},
		},
		{
			name: "with name + server + account",
			args: []string{"/tmp/foo", "--name", "Foo", "--server", "https://x.example", "--account", "alice"},
			want: vaultLinkOpts{
				Path:    "/tmp/foo",
				Name:    "Foo",
				Server:  "https://x.example",
				Account: "alice",
			},
		},
		{
			name:    "no path",
			args:    []string{"--name", "Foo"},
			wantErr: true,
		},
		{
			name:    "account without server",
			args:    []string{"/tmp/foo", "--account", "alice"},
			wantErr: true,
		},
		{
			name:    "unknown flag",
			args:    []string{"/tmp/foo", "--bogus"},
			wantErr: true,
		},
		{
			name:    "extra positional",
			args:    []string{"/tmp/foo", "/tmp/bar"},
			wantErr: true,
		},
		{
			name:    "name flag missing value",
			args:    []string{"/tmp/foo", "--name"},
			wantErr: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseVaultLinkArgs(tc.args)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("got nil error, want one")
				}
				return
			}
			if err != nil {
				t.Fatalf("err = %v, want nil", err)
			}
			if got != tc.want {
				t.Fatalf("got %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestParseVaultLinkArgsHelp(t *testing.T) {
	if _, err := parseVaultLinkArgs([]string{"--help"}); err != errVaultLinkHelp {
		t.Fatalf("err = %v, want errVaultLinkHelp", err)
	}
}

// TestRunVaultLinkCmdHappyPath drives the link command end-to-end with
// XDG_CONFIG_HOME redirected to a tempdir so we don't touch the real
// ~/.config/lumi/vaults.yaml. Asserts that the directory was registered
// and that the row is loadable via the same package's reader.
func TestRunVaultLinkCmdHappyPath(t *testing.T) {
	// `account.VaultsPath` resolves via os.UserHomeDir(). Redirect HOME
	// for the duration of the test so vaults.yaml lands in our tempdir.
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	vaultDir := filepath.Join(tmp, "notes-test")
	if err := os.MkdirAll(vaultDir, 0o755); err != nil {
		t.Fatalf("seed: %v", err)
	}

	var stdout, stderr bytes.Buffer
	code := runVaultLinkCmd([]string{vaultDir, "--name", "Test Vault"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit = %d, stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "Linked vault") {
		t.Errorf("stdout missing success line:\n%s", stdout.String())
	}

	entries, err := account.LoadVaults()
	if err != nil {
		t.Fatalf("LoadVaults: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("got %d entries, want 1", len(entries))
	}
	got := entries[0]
	if got.Name != "Test Vault" {
		t.Errorf("Name = %q, want Test Vault", got.Name)
	}
	if got.Path != vaultDir {
		t.Errorf("Path = %q, want %q", got.Path, vaultDir)
	}
	// `vaultDir` lives under $HOME, so the on-disk RawPath should be ~/-prefixed.
	if !strings.HasPrefix(got.RawPath, "~/") {
		t.Errorf("RawPath = %q, want ~/-prefixed", got.RawPath)
	}
	if got.Server != "" || got.Account != "" {
		t.Errorf("expected local-only vault, got server=%q account=%q", got.Server, got.Account)
	}
	// UUID shape sanity.
	uuidRe := regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
	if !uuidRe.MatchString(got.ID) {
		t.Errorf("ID = %q is not a v4 UUID", got.ID)
	}
}

func TestRunVaultLinkCmdRejectsMissingDir(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	var stdout, stderr bytes.Buffer
	code := runVaultLinkCmd([]string{filepath.Join(tmp, "does-not-exist")}, &stdout, &stderr)
	if code == 0 {
		t.Fatalf("expected non-zero exit for missing dir")
	}
	if !strings.Contains(stderr.String(), "cannot stat") {
		t.Errorf("stderr = %q, want 'cannot stat'", stderr.String())
	}
}

func TestRunVaultLinkCmdRejectsRegularFile(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	file := filepath.Join(tmp, "afile.md")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	var stdout, stderr bytes.Buffer
	code := runVaultLinkCmd([]string{file}, &stdout, &stderr)
	if code == 0 {
		t.Fatalf("expected non-zero exit for non-dir")
	}
	if !strings.Contains(stderr.String(), "not a directory") {
		t.Errorf("stderr = %q, want 'not a directory'", stderr.String())
	}
}

// TestNewVaultIDFormat: assert v4 shape + uniqueness across many calls
// (sanity that crypto/rand isn't returning constant bytes).
func TestNewVaultIDFormat(t *testing.T) {
	seen := map[string]struct{}{}
	uuidRe := regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
	for i := 0; i < 200; i++ {
		id, err := newVaultID()
		if err != nil {
			t.Fatalf("newVaultID: %v", err)
		}
		if !uuidRe.MatchString(id) {
			t.Fatalf("malformed id: %q", id)
		}
		if _, dup := seen[id]; dup {
			t.Fatalf("duplicate id at iter %d: %s", i, id)
		}
		seen[id] = struct{}{}
	}
}

func TestRunVaultCmdUsage(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := runVaultCmd(nil, &stdout, &stderr)
	if code != 64 {
		t.Errorf("no-subcommand exit = %d, want 64", code)
	}
	if !strings.Contains(stderr.String(), "lumi vault") {
		t.Errorf("usage missing from stderr:\n%s", stderr.String())
	}

	stdout.Reset()
	stderr.Reset()
	code = runVaultCmd([]string{"bogus"}, &stdout, &stderr)
	if code != 64 {
		t.Errorf("bogus-subcommand exit = %d, want 64", code)
	}

	stdout.Reset()
	stderr.Reset()
	code = runVaultCmd([]string{"--help"}, &stdout, &stderr)
	if code != 0 {
		t.Errorf("--help exit = %d, want 0", code)
	}
	if !strings.Contains(stdout.String(), "lumi vault") {
		t.Errorf("usage missing from stdout:\n%s", stdout.String())
	}
}
