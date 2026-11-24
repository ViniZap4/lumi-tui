//go:build windows

package account

import (
	"errors"
	"os"
	"time"
)

// Windows has no flock; an O_EXCL lockfile older than the timeout is
// treated as stale (a crashed writer) and taken over.
func acquireLock(path string, timeout time.Duration) (func(), error) {
	path += ".excl"
	deadline := time.Now().Add(timeout)
	for {
		f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err == nil {
			_ = f.Close()
			return func() { _ = os.Remove(path) }, nil
		}
		if !errors.Is(err, os.ErrExist) {
			return nil, err
		}
		if st, serr := os.Stat(path); serr == nil && time.Since(st.ModTime()) > timeout {
			_ = os.Remove(path)
			continue
		}
		if time.Now().After(deadline) {
			return nil, errors.New("timed out waiting for another lumi process")
		}
		time.Sleep(15 * time.Millisecond)
	}
}

// pidAlive: Lumi.app does not exist on Windows; no peer is ever alive.
func pidAlive(pid int) bool { return false }
