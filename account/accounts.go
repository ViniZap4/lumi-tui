// Package account manages signed-in lumi-server accounts and the HTTP
// surface required to obtain a session token.
//
// Storage (SPEC-LIVROS §8.3):
//   - `~/.config/lumi/accounts.yaml` v3 is the account registry shared
//     byte-for-byte with Lumi.app on the same Mac. It holds no secret.
//     Both clients write it read-modify-write under flock on
//     `accounts.yaml.lock`, atomically (temp + fsync + rename), 0600.
//   - `~/.config/lumi/tokens.yaml` (0600) holds this TUI's own session
//     tokens. Lumi.app keeps its token in the Keychain; the two share a
//     sign-in through device links (link.go), never a token.
//   - An account is the pair (server URL, username); several accounts per
//     server are allowed. Server URLs are normalised (§2.1), usernames
//     lower-cased.
//   - v1 (no version) and v2 files, which carried `token:` on each row,
//     are migrated on load: tokens move to tokens.yaml first, then the
//     registry is rewritten as v3. `accounts.v2.yaml` (left by Lumi.app
//     when it found a v2 file) has its tokens imported and is deleted.
//
// Callers keep the old shape: Load → mutate (Upsert / Remove / SignOutMac
// / clearing Token) → Save. Save is a three-way merge against the snapshot
// taken by Load, so a concurrent change made by Lumi.app (or another TUI)
// to other rows or fields is never clobbered.
package account

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Account binds one server URL to one user. Registry fields are shared
// with Lumi.app; Token and SignedInAt are this TUI's own session.
type Account struct {
	Server      string
	Username    string
	UserID      string
	DisplayName string
	// WebOrigin is the server's web client address, when known.
	WebOrigin string
	// Token is this TUI's session token ("" when the TUI has none).
	Token string
	// ExpiresAt is the last known session expiry (any client; UI only).
	ExpiresAt time.Time
	AddedAt   time.Time
	// SignedOutAt: every session on this machine obtained before it is
	// over ("sign out on this Mac", SPEC-LIVROS §8.5).
	SignedOutAt time.Time
	// SignedInAt is when this TUI obtained Token.
	SignedInAt time.Time
}

// IsExpired returns true when the account's `expires_at` is in the past.
// Accounts with a zero `expires_at` are never considered expired.
func (a Account) IsExpired() bool {
	if a.ExpiresAt.IsZero() {
		return false
	}
	return time.Now().After(a.ExpiresAt)
}

// File is a snapshot of the registry plus this TUI's tokens.
type File struct {
	// Version is the format version found on disk (0 = no file).
	Version  int
	Accounts []Account
	// ReadOnly is set when accounts.yaml is newer than this build; Save
	// refuses to write.
	ReadOnly bool
	// Dropped lists TUI tokens discarded on load because the account was
	// removed or signed out on this Mac by another client. Revoke them
	// best effort with RevokeDropped.
	Dropped []Account

	path   string
	base   map[string]Account
	optOut map[string]bool
	optOps map[string]bool
}

// FileVersion is the accounts.yaml format this build writes.
const FileVersion = RegistryVersion

// ErrNoAccount is returned by Resolve when no account matches.
var ErrNoAccount = errors.New("no signed-in account")

// ErrAmbiguousAccount is returned by Resolve when a server has more than
// one account and the caller did not say which one to use.
var ErrAmbiguousAccount = errors.New("more than one account on this server; pass --account <username>")

// Dir resolves to `~/.config/lumi`.
func Dir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("locate home dir: %w", err)
	}
	return filepath.Join(home, ".config", "lumi"), nil
}

// Path resolves to `~/.config/lumi/accounts.yaml`.
func Path() (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "accounts.yaml"), nil
}

func tokensPathFor(registry string) string {
	return filepath.Join(filepath.Dir(registry), "tokens.yaml")
}

func legacyV2PathFor(registry string) string {
	return filepath.Join(filepath.Dir(registry), "accounts.v2.yaml")
}

// Load reads the registry and this TUI's tokens, migrating older layouts.
// A missing file returns an empty File.
func Load() (*File, error) {
	path, err := Path()
	if err != nil {
		return nil, err
	}
	return LoadFrom(path)
}

// LoadFrom is Load with an explicit registry path; tokens.yaml is read
// from the same directory.
func LoadFrom(path string) (*File, error) {
	if err := migrateLegacy(path); err != nil {
		return nil, err
	}
	f := &File{path: path}
	accs, version, err := readRegistry(path)
	switch {
	case errors.Is(err, ErrRegistryTooNew):
		f.ReadOnly = true
	case err != nil:
		return nil, err
	}
	f.Version = version
	tokPath := tokensPathFor(path)
	var tf *TokenFile
	err = withLock(tokPath+".lock", func() error {
		var lerr error
		tf, lerr = loadTokens(tokPath)
		if lerr != nil {
			return lerr
		}
		if f.ReadOnly {
			return nil
		}
		// Re-read the registry under the tokens lock: writers update the
		// registry before tokens.yaml, so every token seen here has its
		// row already, unless that row was really removed.
		fresh, _, rerr := readRegistry(path)
		if rerr != nil {
			return rerr
		}
		accs = fresh
		changed := false
		keep := tf.Tokens[:0]
		for _, e := range tf.Tokens {
			row := findIn(accs, e.Server, e.Username)
			// Same rule as Lumi.app: a session whose sign-in second is not
			// after signed_out_at is over (a same-second sign-in loses).
			stale := row == nil ||
				(!row.SignedOutAt.IsZero() && !utcSeconds(e.SignedInAt).After(row.SignedOutAt))
			if stale {
				f.Dropped = append(f.Dropped, Account{Server: e.Server, Username: e.Username, Token: e.Token})
				changed = true
				continue
			}
			keep = append(keep, e)
		}
		tf.Tokens = keep
		if changed {
			return tf.save(tokPath)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	for i := range accs {
		if j := tf.find(accs[i].Server, accs[i].Username); j >= 0 {
			accs[i].Token = tf.Tokens[j].Token
			accs[i].SignedInAt = tf.Tokens[j].SignedInAt
			if !tf.Tokens[j].ExpiresAt.IsZero() {
				accs[i].ExpiresAt = tf.Tokens[j].ExpiresAt
			}
		}
	}
	f.Accounts = accs
	f.optOut = map[string]bool{}
	for _, k := range tf.OptOut {
		f.optOut[k] = true
	}
	f.snapshot()
	return f, nil
}

// readRegistry reads a v3 (or older) registry. Missing file → empty.
// A too-new file is returned leniently with ErrRegistryTooNew.
func readRegistry(path string) ([]Account, int, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, 0, nil
	}
	if err != nil {
		return nil, 0, fmt.Errorf("read %s: %w", path, err)
	}
	accs, version, err := DecodeRegistry(data)
	if errors.Is(err, ErrRegistryTooNew) {
		lenient, _, lerr := DecodeRegistry([]byte(strings.Replace(string(data), fmt.Sprintf("version: %d", version), "version: 3", 1)))
		if lerr == nil {
			accs = lenient
		}
		return accs, version, ErrRegistryTooNew
	}
	if err != nil {
		return nil, 0, fmt.Errorf("parse %s: %w", path, err)
	}
	for i := range accs {
		accs[i].Token = "" // a v1/v2 token is handled by migrateLegacy
	}
	return accs, version, nil
}

// migrateLegacy moves v1/v2 tokens out of accounts.yaml (tokens.yaml
// first, then the v3 registry, so a crash can duplicate a token but never
// lose it) and imports accounts.v2.yaml left behind by Lumi.app.
func migrateLegacy(path string) error {
	tokPath := tokensPathFor(path)
	needs := func() bool {
		data, err := os.ReadFile(path)
		if err != nil {
			return false
		}
		_, v, err := DecodeRegistry(data)
		return err == nil && v > 0 && v < RegistryVersion
	}
	if needs() {
		err := withLock(path+".lock", func() error {
			data, err := os.ReadFile(path)
			if err != nil {
				return nil
			}
			accs, v, err := DecodeRegistry(data)
			if err != nil || v == 0 || v >= RegistryVersion {
				return nil // someone else migrated meanwhile
			}
			if err := importTokens(tokPath, accs); err != nil {
				return err
			}
			return writeFileAtomic(path, EncodeRegistry(accs))
		})
		if err != nil {
			return fmt.Errorf("migrate %s to v3: %w", path, err)
		}
	}
	legacy := legacyV2PathFor(path)
	data, err := os.ReadFile(legacy)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read %s: %w", legacy, err)
	}
	accs, _, err := DecodeRegistry(data)
	if err != nil {
		return fmt.Errorf("parse %s: %w", legacy, err)
	}
	if err := importTokens(tokPath, accs); err != nil {
		return err
	}
	// Rows Lumi.app should already have carried over; add any missing.
	err = withLock(path+".lock", func() error {
		disk, _, rerr := readRegistry(path)
		if rerr != nil {
			return rerr
		}
		added := false
		for _, a := range accs {
			if findIn(disk, a.Server, a.Username) == nil {
				a.Token = ""
				disk = append(disk, a)
				added = true
			}
		}
		if added {
			return writeFileAtomic(path, EncodeRegistry(disk))
		}
		return nil
	})
	if err != nil {
		return err
	}
	return os.Remove(legacy)
}

// importTokens adds the tokens carried by legacy rows to tokens.yaml,
// never overwriting a token already there.
func importTokens(tokPath string, accs []Account) error {
	return withLock(tokPath+".lock", func() error {
		tf, err := loadTokens(tokPath)
		if err != nil {
			return err
		}
		changed := false
		for _, a := range accs {
			if a.Token == "" || tf.find(a.Server, a.Username) >= 0 {
				continue
			}
			signedIn := a.AddedAt
			if signedIn.IsZero() {
				signedIn = time.Now().UTC()
			}
			tf.set(TokenEntry{Server: a.Server, Username: a.Username, Token: a.Token,
				ExpiresAt: a.ExpiresAt, SignedInAt: signedIn})
			changed = true
		}
		if !changed {
			return nil
		}
		return tf.save(tokPath)
	})
}

func (f *File) snapshot() {
	f.base = make(map[string]Account, len(f.Accounts))
	for _, a := range f.Accounts {
		f.base[accountKey(a.Server, a.Username)] = a
	}
	f.optOps = nil
}

func findIn(accs []Account, server, username string) *Account {
	key := accountKey(server, username)
	for i := range accs {
		if accountKey(accs[i].Server, accs[i].Username) == key {
			return &accs[i]
		}
	}
	return nil
}

func accountKey(server, username string) string {
	return normalizeOrSelf(server) + "#" + lowerTrim(username)
}

func lowerTrim(s string) string { return strings.ToLower(strings.TrimSpace(s)) }

// Save writes this File's changes since Load: registry rows (merged with
// whatever another process wrote meanwhile) and this TUI's tokens.
func (f *File) Save() error {
	path := f.path
	if path == "" {
		p, err := Path()
		if err != nil {
			return err
		}
		path = p
	}
	return f.SaveTo(path)
}

// SaveTo is Save against an explicit registry path.
func (f *File) SaveTo(path string) error {
	if f.ReadOnly {
		return ErrRegistryTooNew
	}
	if err := migrateLegacy(path); err != nil {
		return err
	}
	f.path = path
	err := withLock(path+".lock", func() error {
		disk, _, err := readRegistry(path)
		if err != nil {
			return err
		}
		merged := mergeRegistry(f.base, f.Accounts, disk)
		out := EncodeRegistry(merged)
		if cur, err := os.ReadFile(path); err == nil && string(cur) == string(out) {
			return os.Chmod(path, 0600)
		}
		return writeFileAtomic(path, out)
	})
	if err != nil {
		return err
	}
	tokPath := tokensPathFor(path)
	err = withLock(tokPath+".lock", func() error {
		tf, err := loadTokens(tokPath)
		if err != nil {
			return err
		}
		changed := false
		mine := map[string]bool{}
		now := time.Now().UTC()
		for i := range f.Accounts {
			a := &f.Accounts[i]
			k := accountKey(a.Server, a.Username)
			mine[k] = true
			b, inBase := f.base[k]
			if inBase && b.Token == a.Token {
				continue
			}
			if a.Token == "" {
				if tf.remove(a.Server, a.Username) {
					changed = true
				}
				continue
			}
			if a.SignedInAt.IsZero() || (inBase && b.SignedInAt.Equal(a.SignedInAt)) {
				a.SignedInAt = now
			}
			tf.set(TokenEntry{Server: a.Server, Username: a.Username, Token: a.Token,
				ExpiresAt: a.ExpiresAt, SignedInAt: a.SignedInAt})
			changed = true
		}
		for k, b := range f.base {
			if !mine[k] && tf.remove(b.Server, b.Username) {
				changed = true
			}
		}
		for k, on := range f.optOps {
			parts := strings.SplitN(k, "#", 2)
			if len(parts) == 2 && tf.optedOut(parts[0], parts[1]) != on {
				tf.setOptOut(parts[0], parts[1], on)
				changed = true
			}
		}
		if !changed {
			if _, err := os.Stat(tokPath); err == nil {
				return os.Chmod(tokPath, 0600)
			}
			return nil
		}
		return tf.save(tokPath)
	})
	if err != nil {
		return err
	}
	f.Version = RegistryVersion
	for k, on := range f.optOps {
		if f.optOut == nil {
			f.optOut = map[string]bool{}
		}
		f.optOut[k] = on
	}
	f.snapshot()
	return nil
}

func registryFieldsEqual(a, b Account) bool {
	return a.UserID == b.UserID && a.DisplayName == b.DisplayName && a.WebOrigin == b.WebOrigin &&
		utcSeconds(a.AddedAt).Equal(utcSeconds(b.AddedAt)) &&
		utcSeconds(a.ExpiresAt).Equal(utcSeconds(b.ExpiresAt)) &&
		utcSeconds(a.SignedOutAt).Equal(utcSeconds(b.SignedOutAt))
}

// mergeRegistry applies mine's changes relative to base onto disk:
// field-level for rows both sides know, upsert for rows new to me, and
// removal for rows I dropped. Rows another process added are kept; a row
// another process removed stays removed unless I changed it.
func mergeRegistry(base map[string]Account, mine, disk []Account) []Account {
	out := make([]Account, 0, len(disk)+len(mine))
	idx := map[string]int{}
	for _, d := range disk {
		idx[accountKey(d.Server, d.Username)] = len(out)
		out = append(out, d)
	}
	mineKeys := map[string]bool{}
	for _, m := range mine {
		m = normalizeAccount(m)
		k := accountKey(m.Server, m.Username)
		mineKeys[k] = true
		b, inBase := base[k]
		i, onDisk := idx[k]
		switch {
		case !onDisk && inBase && registryFieldsEqual(b, m):
			// removed by another process and untouched here: stay removed
		case !onDisk:
			idx[k] = len(out)
			m.Token, m.SignedInAt = "", time.Time{}
			out = append(out, m)
		case inBase:
			d := out[i]
			if m.UserID != b.UserID {
				d.UserID = m.UserID
			}
			if m.DisplayName != b.DisplayName {
				d.DisplayName = m.DisplayName
			}
			if m.WebOrigin != b.WebOrigin {
				d.WebOrigin = m.WebOrigin
			}
			if !m.AddedAt.Equal(utcSeconds(b.AddedAt)) {
				d.AddedAt = m.AddedAt
			}
			if !m.ExpiresAt.Equal(utcSeconds(b.ExpiresAt)) {
				d.ExpiresAt = m.ExpiresAt
			}
			if !m.SignedOutAt.Equal(utcSeconds(b.SignedOutAt)) {
				d.SignedOutAt = laterOf(d.SignedOutAt, m.SignedOutAt)
			}
			out[i] = d
		default:
			// New to me, already on disk (same account added elsewhere).
			d := out[i]
			if m.UserID != "" {
				d.UserID = m.UserID
			}
			if m.DisplayName != "" {
				d.DisplayName = m.DisplayName
			}
			if m.WebOrigin != "" {
				d.WebOrigin = m.WebOrigin
			}
			if !m.ExpiresAt.IsZero() {
				d.ExpiresAt = m.ExpiresAt
			}
			if d.AddedAt.IsZero() || (!m.AddedAt.IsZero() && m.AddedAt.Before(d.AddedAt)) {
				d.AddedAt = m.AddedAt
			}
			d.SignedOutAt = laterOf(d.SignedOutAt, m.SignedOutAt)
			out[i] = d
		}
	}
	res := out[:0]
	for _, a := range out {
		k := accountKey(a.Server, a.Username)
		if _, inBase := base[k]; inBase && !mineKeys[k] {
			continue // I removed it
		}
		res = append(res, a)
	}
	return res
}

func laterOf(a, b time.Time) time.Time {
	if b.After(a) {
		return b
	}
	return a
}

// Upsert inserts or replaces an account keyed by (server, username).
// Re-signing in keeps the row's original added_at and signed_out_at.
// Returns true if an existing row was overwritten.
func (f *File) Upsert(a Account) bool {
	if a.AddedAt.IsZero() {
		a.AddedAt = time.Now().UTC()
	}
	a.Server = normalizeOrSelf(a.Server)
	a.Username = lowerTrim(a.Username)
	if existing := f.Find(a.Server, a.Username); existing != nil {
		if !existing.AddedAt.IsZero() {
			a.AddedAt = existing.AddedAt
		}
		a.SignedOutAt = laterOf(existing.SignedOutAt, a.SignedOutAt)
		if a.WebOrigin == "" {
			a.WebOrigin = existing.WebOrigin
		}
		if a.Token != "" && a.Token != existing.Token {
			a.SignedInAt = time.Now().UTC()
		}
		*existing = a
		return true
	}
	if a.Token != "" && a.SignedInAt.IsZero() {
		a.SignedInAt = time.Now().UTC()
	}
	f.Accounts = append(f.Accounts, a)
	sortRegistry(f.Accounts)
	return false
}

// Remove drops the account (server, username). Returns true if a row was
// deleted.
func (f *File) Remove(server, username string) bool {
	key := accountKey(server, username)
	for i, a := range f.Accounts {
		if accountKey(a.Server, a.Username) == key {
			f.Accounts = append(f.Accounts[:i], f.Accounts[i+1:]...)
			return true
		}
	}
	return false
}

// SignOutMac records "sign out on this Mac" for the account: the TUI's
// token goes, and signed_out_at tells Lumi.app to drop its own.
func (f *File) SignOutMac(server, username string, at time.Time) bool {
	a := f.Find(server, username)
	if a == nil {
		return false
	}
	a.Token = ""
	a.SignedOutAt = laterOf(a.SignedOutAt, utcSeconds(at))
	return true
}

// SetLinkOptOut marks (or clears) "don't ask Lumi.app for a link on its
// own" for an account; applied on Save.
func (f *File) SetLinkOptOut(server, username string, on bool) {
	if f.optOps == nil {
		f.optOps = map[string]bool{}
	}
	f.optOps[accountKey(server, username)] = on
}

// LinkOptedOut reports the opt-out flag (including unsaved changes).
func (f *File) LinkOptedOut(server, username string) bool {
	k := accountKey(server, username)
	if on, ok := f.optOps[k]; ok {
		return on
	}
	return f.optOut[k]
}

// Find returns the account (server, username), or nil if not present.
// Server URLs compare after normalisation; usernames case-insensitively.
func (f *File) Find(server, username string) *Account {
	return findIn(f.Accounts, server, username)
}

// ForServer returns every account on `server`.
func (f *File) ForServer(server string) []*Account {
	var out []*Account
	for i := range f.Accounts {
		if SameServer(f.Accounts[i].Server, server) {
			out = append(out, &f.Accounts[i])
		}
	}
	return out
}

// Resolve picks the account to use for `server`. With a username it is an
// exact lookup. Without one it succeeds only when the server has exactly
// one account — guessing between two identities would be a silent
// privilege mix-up.
func (f *File) Resolve(server, username string) (*Account, error) {
	if strings.TrimSpace(username) != "" {
		if a := f.Find(server, username); a != nil {
			return a, nil
		}
		return nil, ErrNoAccount
	}
	matches := f.ForServer(server)
	switch len(matches) {
	case 0:
		return nil, ErrNoAccount
	case 1:
		return matches[0], nil
	default:
		return nil, ErrAmbiguousAccount
	}
}

// RevokeDropped logs out (best effort) tokens Load discarded because
// another client removed the account or signed it out on this Mac.
func RevokeDropped(ctx context.Context, dropped []Account) {
	for _, a := range dropped {
		if a.Token == "" {
			continue
		}
		c, err := NewClient(a.Server)
		if err != nil {
			continue
		}
		c.SetToken(a.Token)
		_ = c.Logout(ctx)
	}
}
