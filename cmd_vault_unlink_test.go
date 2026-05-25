package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vinizap/lumi/tui-client/account"
)

// seedTwoVaults writes a tempdir vaults.yaml with two known entries
// and returns (id1, path1, id2, path2). HOME is also overridden to the
// tempdir so account.VaultsPath resolves into our sandbox.
func seedTwoVaults(t *testing.T) (string, string, string, string) {
	t.Helper()
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	vault1 := filepath.Join(tmp, "work")
	vault2 := filepath.Join(tmp, "personal")
	for _, d := range []string{vault1, vault2} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", d, err)
		}
	}
	id1 := "aaaaaaaa-0000-4000-8000-000000000000"
	id2 := "bbbbbbbb-0000-4000-8000-000000000000"
	now := time.Now().UTC().Truncate(time.Second)
	entries := []account.VaultEntry{
		{ID: id1, Name: "Work", Path: vault1, RawPath: vault1, AddedAt: now},
		{ID: id2, Name: "Personal", Path: vault2, RawPath: vault2, AddedAt: now.Add(-time.Hour)},
	}
	for _, e := range entries {
		if _, err := account.UpsertVault(e); err != nil {
			t.Fatalf("seed upsert: %v", err)
		}
	}
	return id1, vault1, id2, vault2
}

func TestRunVaultUnlinkByID(t *testing.T) {
	id1, _, id2, _ := seedTwoVaults(t)

	var stdout, stderr bytes.Buffer
	code := runVaultUnlinkCmd([]string{id1}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "Unlinked vault") {
		t.Errorf("missing success line:\n%s", stdout.String())
	}

	entries, _ := account.LoadVaults()
	if len(entries) != 1 {
		t.Fatalf("got %d entries, want 1", len(entries))
	}
	if !strings.EqualFold(entries[0].ID, id2) {
		t.Errorf("wrong vault left: %+v", entries[0])
	}
}

func TestRunVaultUnlinkByPath(t *testing.T) {
	_, vault1, _, _ := seedTwoVaults(t)

	var stdout, stderr bytes.Buffer
	// Trailing slash should normalize.
	code := runVaultUnlinkCmd([]string{vault1 + "/"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, stderr.String())
	}

	entries, _ := account.LoadVaults()
	if len(entries) != 1 {
		t.Fatalf("got %d entries, want 1", len(entries))
	}
	if entries[0].Name != "Personal" {
		t.Errorf("wrong vault left: %+v", entries[0])
	}
}

func TestRunVaultUnlinkByRelativePath(t *testing.T) {
	_, vault1, _, _ := seedTwoVaults(t)

	// chdir into tmp's parent so a relative path resolves correctly
	cwd, _ := os.Getwd()
	t.Cleanup(func() { _ = os.Chdir(cwd) })
	parent := filepath.Dir(vault1)
	if err := os.Chdir(parent); err != nil {
		t.Fatalf("chdir: %v", err)
	}

	var stdout, stderr bytes.Buffer
	code := runVaultUnlinkCmd([]string{"./" + filepath.Base(vault1)}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, stderr.String())
	}
	entries, _ := account.LoadVaults()
	if len(entries) != 1 || entries[0].Name != "Personal" {
		t.Fatalf("unlink by relative path failed: %+v", entries)
	}
}

func TestRunVaultUnlinkNotFoundExits2(t *testing.T) {
	seedTwoVaults(t)
	var stdout, stderr bytes.Buffer
	code := runVaultUnlinkCmd([]string{"deadbeef-0000-4000-8000-000000000000"}, &stdout, &stderr)
	if code != 2 {
		t.Errorf("exit = %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), "No vault with id") {
		t.Errorf("stderr missing not-found line:\n%s", stderr.String())
	}
}

func TestRunVaultUnlinkRequiresArg(t *testing.T) {
	seedTwoVaults(t)
	var stdout, stderr bytes.Buffer
	code := runVaultUnlinkCmd(nil, &stdout, &stderr)
	if code != 64 {
		t.Errorf("exit = %d, want 64", code)
	}
}

func TestRunVaultUnlinkRejectsExtraArg(t *testing.T) {
	id1, _, _, _ := seedTwoVaults(t)
	var stdout, stderr bytes.Buffer
	code := runVaultUnlinkCmd([]string{id1, "extra"}, &stdout, &stderr)
	if code != 64 {
		t.Errorf("exit = %d, want 64", code)
	}
}

func TestRunVaultUnlinkHelp(t *testing.T) {
	seedTwoVaults(t)
	var stdout, stderr bytes.Buffer
	code := runVaultUnlinkCmd([]string{"--help"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("--help exit = %d, want 0", code)
	}
	if !strings.Contains(stdout.String(), "unlink") {
		t.Errorf("usage missing 'unlink':\n%s", stdout.String())
	}
}

func TestLooksLikeUUID(t *testing.T) {
	cases := map[string]bool{
		"":                                       false,
		"aaaaaaaa-0000-4000-8000-000000000000":  true,
		"AAAAAAAA-0000-4000-8000-000000000000":  true, // case-insensitive
		"not-a-uuid":                             false,
		"aaaaaaaa-0000-4000-8000":                false,
	}
	for in, want := range cases {
		if got := looksLikeUUID(in); got != want {
			t.Errorf("looksLikeUUID(%q) = %v, want %v", in, got, want)
		}
	}
}
