package account

import (
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// lockTimeout bounds how long a writer waits for another process (Lumi.app
// or a second TUI) to finish its read-modify-write.
var lockTimeout = 10 * time.Second

// withLock runs fn while holding an exclusive advisory lock on lockPath
// (SPEC-LIVROS §8.3): flock(LOCK_EX) on Unix — the same primitive
// Lumi.app uses on `accounts.yaml.lock` — and an O_EXCL lockfile on
// Windows. The lock file is created 0600 next to the data file.
func withLock(lockPath string, fn func() error) error {
	if err := os.MkdirAll(filepath.Dir(lockPath), 0700); err != nil {
		return fmt.Errorf("mkdir %s: %w", filepath.Dir(lockPath), err)
	}
	unlock, err := acquireLock(lockPath, lockTimeout)
	if err != nil {
		return fmt.Errorf("lock %s: %w", lockPath, err)
	}
	defer unlock()
	return fn()
}

// writeFileAtomic writes data to path via a 0600 temp file in the same
// directory, fsync, rename, then forces 0600 on the result. Readers never
// see a partial file, and the content is never world-readable.
func writeFileAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("mkdir %s: %w", dir, err)
	}
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+"-*.tmp")
	if err != nil {
		return fmt.Errorf("temp file: %w", err)
	}
	tmpPath := tmp.Name()
	defer func() { _ = os.Remove(tmpPath) }() // no-op after rename
	if err := tmp.Chmod(0600); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("chmod temp: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write temp: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("sync temp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("rename %s -> %s: %w", tmpPath, path, err)
	}
	if err := os.Chmod(path, 0600); err != nil {
		return fmt.Errorf("chmod %s: %w", path, err)
	}
	if d, err := os.Open(dir); err == nil { // persist the rename (best effort)
		_ = d.Sync()
		_ = d.Close()
	}
	return nil
}
