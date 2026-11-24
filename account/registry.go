package account

import (
	"bytes"
	"fmt"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// RegistryVersion is the accounts.yaml format this build writes: the
// shared, secret-free registry of SPEC-LIVROS §8.3, byte-identical with
// Lumi.app (golden: testdata/accounts/accounts.v3.yaml).
const RegistryVersion = 3

const registryHeader = "# ~/.config/lumi/accounts.yaml — lumi account registry, no secrets. Format: SPEC-LIVROS §8.3."

// ErrRegistryTooNew is returned when accounts.yaml declares a version
// newer than this build understands. The file is then read-only so an
// old binary never strips fields it can't see.
var ErrRegistryTooNew = fmt.Errorf("accounts.yaml uses a newer format (> v%d); update lumi to change accounts", RegistryVersion)

type rawRow struct {
	Server      string `yaml:"server"`
	ServerURL   string `yaml:"server_url"`
	Username    string `yaml:"username"`
	UserID      string `yaml:"user_id"`
	DisplayName string `yaml:"display_name"`
	WebOrigin   string `yaml:"web_origin"`
	Token       string `yaml:"token"` // v1/v2 only; never written
	AddedAt     string `yaml:"added_at"`
	ExpiresAt   string `yaml:"expires_at"`
	SignedOutAt string `yaml:"signed_out_at"`
}

type rawFile struct {
	Version  int      `yaml:"version"`
	Accounts []rawRow `yaml:"accounts"`
}

// DecodeRegistry parses accounts.yaml of any version. The reader is
// tolerant (comments, CRLF, quoting styles, `~`, offsets, fractional
// seconds). v1/v2 rows keep their `token` in Account.Token so the caller
// can move it to tokens.yaml; v3 never carries one. version is 1 for a
// file without a `version:` line (the v1 layout), 0 for an empty file.
func DecodeRegistry(data []byte) ([]Account, int, error) {
	data = bytes.TrimPrefix(data, []byte("\xef\xbb\xbf"))
	var raw rawFile
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return nil, 0, err
	}
	version := raw.Version
	if version == 0 && len(bytes.TrimSpace(stripComments(data))) > 0 {
		version = 1
	}
	if version > RegistryVersion {
		return nil, version, ErrRegistryTooNew
	}
	out := make([]Account, 0, len(raw.Accounts))
	index := map[string]int{}
	for _, r := range raw.Accounts {
		server := r.Server
		if server == "" {
			server = r.ServerURL
		}
		if strings.TrimSpace(server) == "" || strings.TrimSpace(r.Username) == "" {
			continue
		}
		a := normalizeAccount(Account{
			Server:      server,
			Username:    r.Username,
			UserID:      r.UserID,
			DisplayName: r.DisplayName,
			WebOrigin:   strings.TrimSpace(r.WebOrigin),
			Token:       r.Token,
			AddedAt:     parseStamp(r.AddedAt),
			ExpiresAt:   parseStamp(r.ExpiresAt),
			SignedOutAt: parseStamp(r.SignedOutAt),
		})
		key := accountKey(a.Server, a.Username)
		if i, ok := index[key]; ok {
			// Duplicate: the most recently added row wins; a tie goes to
			// the later row in the file.
			if !a.AddedAt.Before(out[i].AddedAt) {
				out[i] = a
			}
			continue
		}
		index[key] = len(out)
		out = append(out, a)
	}
	sortRegistry(out)
	return out, version, nil
}

func stripComments(data []byte) []byte {
	var b bytes.Buffer
	for _, line := range bytes.Split(data, []byte("\n")) {
		t := bytes.TrimSpace(line)
		if len(t) == 0 || t[0] == '#' {
			continue
		}
		b.Write(line)
		b.WriteByte('\n')
	}
	return b.Bytes()
}

// normalizeAccount applies the registry's identity rules: normalised
// server, lower-case username, lower-case UUID user ids, UTC seconds.
func normalizeAccount(a Account) Account {
	a.Server = normalizeOrSelf(a.Server)
	a.Username = strings.ToLower(strings.TrimSpace(a.Username))
	a.UserID = normalizeID(a.UserID)
	a.AddedAt = utcSeconds(a.AddedAt)
	a.ExpiresAt = utcSeconds(a.ExpiresAt)
	a.SignedOutAt = utcSeconds(a.SignedOutAt)
	return a
}

func utcSeconds(t time.Time) time.Time {
	if t.IsZero() {
		return time.Time{}
	}
	return t.UTC().Truncate(time.Second)
}

func normalizeID(s string) string {
	s = strings.TrimSpace(s)
	if l := strings.ToLower(s); isCanonicalUUID(l) {
		return l
	}
	return s
}

func parseStamp(s string) time.Time {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}
	}
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02 15:04:05.999999999Z07:00", "2006-01-02 15:04:05Z07:00", "2006-01-02T15:04:05", "2006-01-02"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t.UTC()
		}
	}
	return time.Time{}
}

// sortRegistry orders rows by added_at, then server, then username.
func sortRegistry(accs []Account) {
	sort.SliceStable(accs, func(i, j int) bool {
		a, b := accs[i], accs[j]
		if !a.AddedAt.Equal(b.AddedAt) {
			return a.AddedAt.Before(b.AddedAt)
		}
		if a.Server != b.Server {
			return a.Server < b.Server
		}
		return a.Username < b.Username
	})
}

// EncodeRegistry renders the canonical v3 bytes (SPEC-LIVROS §8.3). It
// never writes a token.
func EncodeRegistry(accs []Account) []byte {
	rows := make([]Account, len(accs))
	for i, a := range accs {
		rows[i] = normalizeAccount(a)
	}
	sortRegistry(rows)
	var b strings.Builder
	b.WriteString(registryHeader + "\n")
	fmt.Fprintf(&b, "version: %d\n", RegistryVersion)
	if len(rows) == 0 {
		b.WriteString("accounts: []\n")
		return []byte(b.String())
	}
	b.WriteString("accounts:\n")
	for _, a := range rows {
		b.WriteString("  - server: " + quoted(a.Server) + "\n")
		b.WriteString("    username: " + quoted(a.Username) + "\n")
		b.WriteString("    user_id: " + idToken(a.UserID) + "\n")
		b.WriteString("    display_name: " + quoted(a.DisplayName) + "\n")
		b.WriteString("    web_origin: " + quotedOrNull(a.WebOrigin) + "\n")
		b.WriteString("    added_at: " + stamp(a.AddedAt) + "\n")
		b.WriteString("    expires_at: " + stamp(a.ExpiresAt) + "\n")
		b.WriteString("    signed_out_at: " + stamp(a.SignedOutAt) + "\n")
	}
	return []byte(b.String())
}

// The scalar rules below are the books.yaml ones (SPEC-LIVROS §2.3),
// duplicated here because package books imports account.

func stamp(t time.Time) string {
	if t.IsZero() {
		return "null"
	}
	return t.UTC().Format("2006-01-02T15:04:05Z")
}

func idToken(s string) string {
	switch {
	case s == "":
		return "null"
	case isCanonicalUUID(s):
		return s
	default:
		return quoted(s)
	}
}

func isCanonicalUUID(s string) bool {
	if len(s) != 36 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch i {
		case 8, 13, 18, 23:
			if c != '-' {
				return false
			}
		default:
			if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
				return false
			}
		}
	}
	return true
}

func quotedOrNull(s string) string {
	if s == "" {
		return "null"
	}
	return quoted(s)
}

func quoted(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '\\':
			b.WriteString(`\\`)
		case '"':
			b.WriteString(`\"`)
		case '\n':
			b.WriteString(`\n`)
		case '\t':
			b.WriteString(`\t`)
		case '\r':
			b.WriteString(`\r`)
		default:
			if r < 0x20 || r == 0x7f {
				fmt.Fprintf(&b, `\u%04X`, r)
			} else {
				b.WriteRune(r)
			}
		}
	}
	b.WriteByte('"')
	return b.String()
}
