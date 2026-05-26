package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// FolderConfig holds per-folder settings loaded from <rootDir>/.lumi/config.yaml.
// The file contains `server_token` — a credential — so writes are atomic
// and the file is forced to 0600 on every write. The enclosing
// `.lumi/` directory is created at 0700 to hide the token file's
// existence from other users on shared systems. SPEC Phase 5 audit fix.
type FolderConfig struct {
	ServerURL   string // URL of the lumi server (e.g. "http://localhost:8080")
	ServerToken string // auth token for the server
}

// LoadFolderConfig reads <rootDir>/.lumi/config.yaml and returns the folder config.
// Returns a zero-value FolderConfig if the file doesn't exist.
//
// Migration: if the file exists with broader perms than 0600 (e.g.
// 0644 from older lumi versions), force-chmod to 0600 on load — the
// file holds `server_token`, and leaving it world-readable is a
// real leak on shared systems. Silent because the user shouldn't
// need to think about it; the chmod is the safe direction (tightens,
// never loosens).
func LoadFolderConfig(rootDir string) *FolderConfig {
	cfg := &FolderConfig{}

	configPath := filepath.Join(rootDir, ".lumi", "config.yaml")
	info, statErr := os.Stat(configPath)
	if statErr != nil {
		return cfg
	}
	if info.Mode().Perm() != 0o600 {
		// Best-effort tighten — a chmod failure (read-only FS, foreign
		// owner) shouldn't block loading the config; we still read what
		// we can. A user with that scenario is unlikely to have a token
		// in there anyway.
		_ = os.Chmod(configPath, 0o600)
	}
	data, err := os.ReadFile(configPath)
	if err != nil {
		return cfg
	}

	lines := strings.Split(string(data), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "#") || line == "" {
			continue
		}

		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			continue
		}

		key := strings.TrimSpace(parts[0])
		value := unquoteValue(parts[1])

		switch key {
		case "server_url":
			cfg.ServerURL = value
		case "server_token":
			cfg.ServerToken = value
		}
	}

	return cfg
}

// SaveFolderConfig writes the folder config to <rootDir>/.lumi/config.yaml.
// Atomic via tmpfile + rename so a crash mid-write never leaves a
// half-written token file. The file is forced to 0600 on every save
// (including overwrite) so a previously broader mode never lingers.
// The parent `.lumi/` directory is created at 0700 on demand.
func SaveFolderConfig(rootDir string, cfg *FolderConfig) error {
	lumiDir := filepath.Join(rootDir, ".lumi")
	if err := os.MkdirAll(lumiDir, 0o700); err != nil {
		return fmt.Errorf("create .lumi dir: %w", err)
	}

	content := fmt.Sprintf(`# Lumi Folder Config
# Server connection (for TUI real-time sync)
server_url: %s
server_token: %s
`, cfg.ServerURL, cfg.ServerToken)

	finalPath := filepath.Join(lumiDir, "config.yaml")
	tmp, err := os.CreateTemp(lumiDir, ".config.yaml.tmp-*")
	if err != nil {
		return fmt.Errorf("create temp: %w", err)
	}
	tmpPath := tmp.Name()
	cleanup := func() {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
	}
	if _, err := tmp.Write([]byte(content)); err != nil {
		cleanup()
		return fmt.Errorf("write temp: %w", err)
	}
	// Force 0600 on the tmp file (CreateTemp creates at 0600 already
	// on most platforms but we don't trust it). Doing it before close
	// + rename also tightens before any reader can race.
	if err := tmp.Chmod(0o600); err != nil {
		cleanup()
		return fmt.Errorf("chmod temp: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		cleanup()
		return fmt.Errorf("sync temp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("close temp: %w", err)
	}
	if err := os.Rename(tmpPath, finalPath); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("rename: %w", err)
	}
	// Force 0600 again after rename: if a pre-existing target had
	// broader perms, the rename inherits those on some filesystems.
	if err := os.Chmod(finalPath, 0o600); err != nil {
		return fmt.Errorf("chmod final: %w", err)
	}
	return nil
}

// EnsureLumiDir creates the .lumi/ directory in rootDir if it doesn't
// exist. Created at 0700 (token-file parent).
func EnsureLumiDir(rootDir string) error {
	return os.MkdirAll(filepath.Join(rootDir, ".lumi"), 0o700)
}
