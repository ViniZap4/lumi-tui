package account

import (
	"fmt"
	"os"
	"sort"
	"time"

	"gopkg.in/yaml.v3"
)

// tokens.yaml (SPEC-LIVROS §8.3) holds the TUI's own session tokens, one
// per account, 0600. It is read and written only by the TUI: Lumi.app
// keeps its own token in the Keychain, and the two share a sign-in
// through device links (§8.4), never a secret. On macOS this stays a
// file (not the Keychain) because an ad-hoc-signed, often rebuilt binary
// would hit a Keychain ACL prompt after every build.

// TokenEntry is one row of tokens.yaml.
type TokenEntry struct {
	Server     string    `yaml:"server"`
	Username   string    `yaml:"username"`
	Token      string    `yaml:"token"`
	ExpiresAt  time.Time `yaml:"expires_at,omitempty"`
	SignedInAt time.Time `yaml:"signed_in_at"`
	// Linked: the session shares a device-link group with Lumi.app's
	// (SPEC-LIVROS §8.4); UI only.
	Linked bool `yaml:"linked,omitempty"`
}

// TokenFile is the in-memory tokens.yaml.
type TokenFile struct {
	Version int          `yaml:"version"`
	Tokens  []TokenEntry `yaml:"tokens"`
	// OptOut lists "server#username" keys the user signed out of in the
	// TUI only (or denied a link for): never ask Lumi.app for a link on
	// its own for these.
	OptOut []string `yaml:"opt_out"`
}

const tokensHeader = "# lumi TUI session tokens. DO NOT share: each token signs in as its account.\n" +
	"# Account metadata lives in accounts.yaml (shared with Lumi.app). SPEC-LIVROS §8.3.\n"

func loadTokens(path string) (*TokenFile, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return &TokenFile{Version: 1}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	var t TokenFile
	if err := yaml.Unmarshal(data, &t); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	for i := range t.Tokens {
		t.Tokens[i].Server = normalizeOrSelf(t.Tokens[i].Server)
		t.Tokens[i].Username = lowerTrim(t.Tokens[i].Username)
	}
	return &t, nil
}

func (t *TokenFile) save(path string) error {
	t.Version = 1
	sort.SliceStable(t.Tokens, func(i, j int) bool {
		a, b := t.Tokens[i], t.Tokens[j]
		if a.Server != b.Server {
			return a.Server < b.Server
		}
		return a.Username < b.Username
	})
	sort.Strings(t.OptOut)
	if t.OptOut == nil {
		t.OptOut = []string{}
	}
	out, err := yaml.Marshal(t)
	if err != nil {
		return fmt.Errorf("marshal tokens: %w", err)
	}
	return writeFileAtomic(path, append([]byte(tokensHeader), out...))
}

func (t *TokenFile) find(server, username string) int {
	key := accountKey(server, username)
	for i, e := range t.Tokens {
		if accountKey(e.Server, e.Username) == key {
			return i
		}
	}
	return -1
}

func (t *TokenFile) set(e TokenEntry) {
	e.Server = normalizeOrSelf(e.Server)
	e.Username = lowerTrim(e.Username)
	if i := t.find(e.Server, e.Username); i >= 0 {
		t.Tokens[i] = e
		return
	}
	t.Tokens = append(t.Tokens, e)
}

func (t *TokenFile) remove(server, username string) bool {
	if i := t.find(server, username); i >= 0 {
		t.Tokens = append(t.Tokens[:i], t.Tokens[i+1:]...)
		return true
	}
	return false
}

func (t *TokenFile) optedOut(server, username string) bool {
	key := accountKey(server, username)
	for _, k := range t.OptOut {
		if k == key {
			return true
		}
	}
	return false
}

func (t *TokenFile) setOptOut(server, username string, on bool) {
	key := accountKey(server, username)
	out := t.OptOut[:0]
	for _, k := range t.OptOut {
		if k != key {
			out = append(out, k)
		}
	}
	if on {
		out = append(out, key)
	}
	t.OptOut = out
}
