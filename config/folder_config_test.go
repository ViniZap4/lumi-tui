package config

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestSaveFolderConfigCreatesAt0600 — fresh save creates the file at
// 0600. The file holds server_token (a credential), so any broader
// mode would be a leak on shared systems.
func TestSaveFolderConfigCreatesAt0600(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX mode bits don't apply on Windows")
	}
	root := t.TempDir()
	cfg := &FolderConfig{ServerURL: "https://lumi.test", ServerToken: "secret-token"}
	if err := SaveFolderConfig(root, cfg); err != nil {
		t.Fatalf("save: %v", err)
	}
	info, err := os.Stat(filepath.Join(root, ".lumi", "config.yaml"))
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Errorf("file mode = %o, want 0600", got)
	}
}

// TestSaveFolderConfigOverwriteForces0600 — even when an existing
// file has broader perms (a v1-era 0644 lingering), the save
// re-tightens it to 0600. The chmod-after-rename branch is what
// makes this test meaningful.
func TestSaveFolderConfigOverwriteForces0600(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX mode bits don't apply on Windows")
	}
	root := t.TempDir()
	lumiDir := filepath.Join(root, ".lumi")
	if err := os.MkdirAll(lumiDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	pre := filepath.Join(lumiDir, "config.yaml")
	if err := os.WriteFile(pre, []byte("# stale\n"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	cfg := &FolderConfig{ServerURL: "https://lumi.test", ServerToken: "tok"}
	if err := SaveFolderConfig(root, cfg); err != nil {
		t.Fatalf("save: %v", err)
	}
	info, _ := os.Stat(pre)
	if got := info.Mode().Perm(); got != 0o600 {
		t.Errorf("post-overwrite mode = %o, want 0600", got)
	}
}

// TestSaveFolderConfigCreatesParentAt0700 — the .lumi/ dir is the
// token's neighbour; making it 0700 hides config.yaml's existence
// from other users on a shared host.
func TestSaveFolderConfigCreatesParentAt0700(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX mode bits don't apply on Windows")
	}
	root := t.TempDir()
	cfg := &FolderConfig{ServerURL: "https://lumi.test", ServerToken: "tok"}
	if err := SaveFolderConfig(root, cfg); err != nil {
		t.Fatalf("save: %v", err)
	}
	info, err := os.Stat(filepath.Join(root, ".lumi"))
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o700 {
		t.Errorf(".lumi mode = %o, want 0700", got)
	}
}

// TestSaveFolderConfigDoesNotLeakTmp — atomic-write tmp files
// should not survive a successful save. A leaked .config.yaml.tmp-*
// would also be a leak (the tmp is also at 0600 but contains the
// token).
func TestSaveFolderConfigDoesNotLeakTmp(t *testing.T) {
	root := t.TempDir()
	cfg := &FolderConfig{ServerURL: "https://lumi.test", ServerToken: "tok"}
	if err := SaveFolderConfig(root, cfg); err != nil {
		t.Fatalf("save: %v", err)
	}
	entries, _ := os.ReadDir(filepath.Join(root, ".lumi"))
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".config.yaml.tmp-") {
			t.Errorf("leftover tmp file: %s", e.Name())
		}
	}
}

// TestSaveFolderConfigRoundTrips — verify the parser still
// recognises the fields the writer emits.
func TestSaveFolderConfigRoundTrips(t *testing.T) {
	root := t.TempDir()
	cfg := &FolderConfig{ServerURL: "https://lumi.test", ServerToken: "tok"}
	if err := SaveFolderConfig(root, cfg); err != nil {
		t.Fatalf("save: %v", err)
	}
	got := LoadFolderConfig(root)
	if got.ServerURL != cfg.ServerURL {
		t.Errorf("ServerURL = %q, want %q", got.ServerURL, cfg.ServerURL)
	}
	if got.ServerToken != cfg.ServerToken {
		t.Errorf("ServerToken = %q, want %q", got.ServerToken, cfg.ServerToken)
	}
}

// TestLoadFolderConfigChmodsBroaderFile — migration path: a pre-
// existing file at 0644 (older lumi versions wrote it that way)
// gets tightened to 0600 on first load. The token has already been
// readable up to this point; closing the door for every future read
// is the value here.
func TestLoadFolderConfigChmodsBroaderFile(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX mode bits don't apply on Windows")
	}
	root := t.TempDir()
	lumiDir := filepath.Join(root, ".lumi")
	if err := os.MkdirAll(lumiDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	configPath := filepath.Join(lumiDir, "config.yaml")
	body := []byte("server_url: https://lumi.test\nserver_token: tok\n")
	if err := os.WriteFile(configPath, body, 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	got := LoadFolderConfig(root)
	if got.ServerToken != "tok" {
		t.Errorf("ServerToken = %q, want tok", got.ServerToken)
	}
	info, _ := os.Stat(configPath)
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Errorf("post-load mode = %o, want 0600 (migration chmod failed)", mode)
	}
}

// TestLoadFolderConfigMissingFile — loading from a directory with
// no config file returns a zero-value FolderConfig (no error). This
// is the common case for vaults that aren't server-bound.
func TestLoadFolderConfigMissingFile(t *testing.T) {
	root := t.TempDir()
	got := LoadFolderConfig(root)
	if got.ServerURL != "" || got.ServerToken != "" {
		t.Errorf("expected zero-value, got %+v", got)
	}
}
