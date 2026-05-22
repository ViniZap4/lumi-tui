package account

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// VaultEntry mirrors one row of the shared `~/.config/lumi/vaults.yaml`
// file. The file is the canonical cross-client registry of vault
// locations — the Apple client owns the writer; this reader is the TUI
// side of the contract.
//
// Schema (matches lumi-apple Sources/LumiKit/Persistence/VaultRegistry.swift):
//
//	vaults:
//	  - id: <uuid>
//	    name: <string>
//	    path: <string>            # may be ~/-prefixed for home-relative
//	    server: <url|null>
//	    account: <string|null>    # account username on `server`, when bound
//	    added_at: <iso8601>
//	    last_opened_at: <iso8601|null>
type VaultEntry struct {
	ID           string    // canonical lowercase UUID
	Name         string
	Path         string    // expanded (~ replaced with $HOME)
	RawPath      string    // as-written (may still carry ~/)
	Server       string    // empty when this is a local-only vault
	Account      string    // empty when not bound
	AddedAt      time.Time
	LastOpenedAt time.Time // zero when never opened
}

// IsServerBound is a convenience predicate: true if the entry references
// a remote server.
func (v VaultEntry) IsServerBound() bool { return v.Server != "" }

// VaultsPath resolves to `~/.config/lumi/vaults.yaml`.
func VaultsPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("locate home dir: %w", err)
	}
	return filepath.Join(home, ".config", "lumi", "vaults.yaml"), nil
}

// LoadVaults reads the shared vaults registry. Missing file returns no
// entries and no error (fresh-user case). Malformed file returns the
// error so the user knows something is off.
func LoadVaults() ([]VaultEntry, error) {
	path, err := VaultsPath()
	if err != nil {
		return nil, err
	}
	return LoadVaultsFrom(path)
}

// LoadVaultsFrom reads the vaults registry from an explicit path. Used by
// tests. Tolerant of hand-edits — unknown top-level keys are ignored;
// rows missing required fields are skipped (rather than failing the load).
func LoadVaultsFrom(path string) ([]VaultEntry, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	entries := parseVaultsYAML(string(data))
	// Sort most-recently-opened first — matches the Apple-side ordering
	// so the two clients show the same list.
	sort.SliceStable(entries, func(i, j int) bool {
		la := entries[i].LastOpenedAt
		if la.IsZero() {
			la = entries[i].AddedAt
		}
		lb := entries[j].LastOpenedAt
		if lb.IsZero() {
			lb = entries[j].AddedAt
		}
		return la.After(lb)
	})
	return entries, nil
}

// parseVaultsYAML implements the same hand-rolled parser style as
// lumi-apple's VaultRegistry.readFromDisk. We don't use yaml.v3 here
// because the Apple writer is hand-rolled too, and aligning on a tolerant
// line-by-line reader makes the two ends easy to reconcile when fields
// drift. Unknown keys pass through, list items start at `  - key: value`.
func parseVaultsYAML(text string) []VaultEntry {
	var entries []VaultEntry
	var current map[string]string
	inList := false

	flush := func() {
		if len(current) == 0 {
			return
		}
		if e, ok := entryFromVaultFields(current); ok {
			entries = append(entries, e)
		}
		current = nil
	}

	for _, raw := range strings.Split(text, "\n") {
		// Detect "top-level key" by lack of leading whitespace.
		trimmed := strings.TrimLeft(raw, " \t")
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		isTopLevel := raw == trimmed
		if isTopLevel {
			flush()
			inList = strings.HasPrefix(trimmed, "vaults:")
			continue
		}
		if !inList {
			continue
		}
		// List-item start: `  - key: value`
		if strings.HasPrefix(trimmed, "- ") {
			flush()
			current = map[string]string{}
			rest := strings.TrimPrefix(trimmed, "- ")
			if k, v, ok := splitYAMLKV(rest); ok {
				current[k] = v
			}
			continue
		}
		// Continuation: `    key: value`
		if k, v, ok := splitYAMLKV(trimmed); ok {
			if current == nil {
				current = map[string]string{}
			}
			current[k] = v
		}
	}
	flush()
	return entries
}

func splitYAMLKV(line string) (key, value string, ok bool) {
	idx := strings.IndexByte(line, ':')
	if idx <= 0 {
		return "", "", false
	}
	key = strings.TrimSpace(line[:idx])
	value = strings.TrimSpace(line[idx+1:])
	// Unquote single or double quotes for tolerance with the Apple writer's
	// `yamlString` helper (which quotes when the value contains a colon
	// or hash).
	if len(value) >= 2 {
		first, last := value[0], value[len(value)-1]
		if (first == '"' && last == '"') || (first == '\'' && last == '\'') {
			value = value[1 : len(value)-1]
		}
	}
	return key, value, true
}

func entryFromVaultFields(f map[string]string) (VaultEntry, bool) {
	id := strings.ToLower(strings.TrimSpace(f["id"]))
	name := f["name"]
	path := f["path"]
	if id == "" || name == "" || path == "" {
		return VaultEntry{}, false
	}
	server := nonNullValue(f["server"])
	account := nonNullValue(f["account"])
	added := parseISO(f["added_at"])
	lastOpened := parseISO(f["last_opened_at"])
	return VaultEntry{
		ID:           id,
		Name:         name,
		Path:         expandHomeRelative(path),
		RawPath:      path,
		Server:       server,
		Account:      account,
		AddedAt:      added,
		LastOpenedAt: lastOpened,
	}, true
}

func nonNullValue(s string) string {
	if s == "" || strings.EqualFold(s, "null") {
		return ""
	}
	return s
}

func parseISO(s string) time.Time {
	s = strings.TrimSpace(s)
	if s == "" || strings.EqualFold(s, "null") {
		return time.Time{}
	}
	// Apple writes via `Date.ISO8601Format()` which yields full ISO 8601
	// with optional fractional seconds and Z. Try a couple of layouts.
	for _, layout := range []string{
		time.RFC3339Nano,
		time.RFC3339,
		"2006-01-02T15:04:05Z",
	} {
		if t, err := time.Parse(layout, s); err == nil {
			return t
		}
	}
	return time.Time{}
}

func expandHomeRelative(p string) string {
	if p == "~" {
		if home, err := os.UserHomeDir(); err == nil {
			return home
		}
		return p
	}
	if strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, p[2:])
		}
	}
	return p
}
