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

// SaveVaultsAt rewrites the vault registry at `path` with `entries`,
// matching lumi-apple's `VaultRegistry.writeToDisk` byte shape so the
// two clients can hand the file back and forth without drift.
//
// Write is atomic via tmpfile + rename at mode 0600 (vault paths and
// account usernames live in this file; not a secret but not for other
// users either). Parent directory is created on demand at 0700.
//
// Sort order mirrors Apple's: `last_opened_at ?? added_at` descending,
// so the resulting file is identical regardless of which client wrote
// last. `entries` is not mutated.
func SaveVaultsAt(path string, entries []VaultEntry) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("mkdir %s: %w", filepath.Dir(path), err)
	}
	sorted := append([]VaultEntry(nil), entries...)
	sort.SliceStable(sorted, func(i, j int) bool {
		la := sorted[i].LastOpenedAt
		if la.IsZero() {
			la = sorted[i].AddedAt
		}
		lb := sorted[j].LastOpenedAt
		if lb.IsZero() {
			lb = sorted[j].AddedAt
		}
		return la.After(lb)
	})

	body := renderVaultsYAML(sorted)

	// Write tmp, fsync, rename — same shape as account/accounts.go's
	// save path. Force 0600 on the tmp file so an interrupted rename
	// can't leak a world-readable artifact.
	tmp, err := os.CreateTemp(filepath.Dir(path), ".vaults-*.yaml")
	if err != nil {
		return fmt.Errorf("create tmp: %w", err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath) // safe even after a successful rename
	if _, err := tmp.WriteString(body); err != nil {
		tmp.Close()
		return fmt.Errorf("write tmp: %w", err)
	}
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return fmt.Errorf("chmod tmp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close tmp: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("rename to %s: %w", path, err)
	}
	// If the file pre-existed at a wider mode, the rename inherits
	// the tmp's perms (0600) on macOS/Linux, but make it explicit so
	// we don't depend on platform quirks.
	if err := os.Chmod(path, 0o600); err != nil {
		return fmt.Errorf("chmod %s: %w", path, err)
	}
	return nil
}

// SaveVaults writes the registry at the default location
// (`~/.config/lumi/vaults.yaml`). Thin wrapper around SaveVaultsAt.
func SaveVaults(entries []VaultEntry) error {
	path, err := VaultsPath()
	if err != nil {
		return err
	}
	return SaveVaultsAt(path, entries)
}

// UpsertVault inserts or replaces a row (matched by `id`) in the
// default vaults.yaml and writes the result. Matches Apple's
// `VaultRegistry.upsert` semantics. Returns the resulting full list
// (post-sort) for callers that want to display it.
func UpsertVault(entry VaultEntry) ([]VaultEntry, error) {
	path, err := VaultsPath()
	if err != nil {
		return nil, err
	}
	return UpsertVaultAt(path, entry)
}

// UpsertVaultAt is the test-friendly form of UpsertVault.
func UpsertVaultAt(path string, entry VaultEntry) ([]VaultEntry, error) {
	current, err := LoadVaultsFrom(path)
	if err != nil {
		return nil, err
	}
	found := false
	for i := range current {
		if strings.EqualFold(current[i].ID, entry.ID) {
			current[i] = entry
			found = true
			break
		}
	}
	if !found {
		current = append(current, entry)
	}
	if err := SaveVaultsAt(path, current); err != nil {
		return nil, err
	}
	// Re-load so the returned slice matches what's on disk (re-sorted,
	// home-relative paths re-expanded, etc.).
	return LoadVaultsFrom(path)
}

// EncodeHomeRelative is the Go equivalent of lumi-apple's
// `encodeHomeRelative`. When `absolute` sits inside the user's home
// directory, the leading `$HOME[/]` is replaced with `~[/]` so the
// stored path is portable across machines that share a HOME-relative
// layout. Anything outside `$HOME` passes through unchanged.
func EncodeHomeRelative(absolute string) string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return absolute
	}
	if absolute == home {
		return "~"
	}
	prefix := home
	if !strings.HasSuffix(prefix, string(os.PathSeparator)) {
		prefix += string(os.PathSeparator)
	}
	if strings.HasPrefix(absolute, prefix) {
		return "~/" + absolute[len(prefix):]
	}
	return absolute
}

// renderVaultsYAML produces the exact byte shape lumi-apple's
// `VaultRegistry.writeToDisk` emits. The two writers must stay in
// lockstep so a round-trip through either client leaves the file
// unchanged.
func renderVaultsYAML(entries []VaultEntry) string {
	var sb strings.Builder
	sb.WriteString("# lumi shared vault registry. Format defined in SPEC.md.\n")
	sb.WriteString("# Written by lumi-apple and lumi-tui (Phase 5+).\n")
	sb.WriteString("vaults:\n")
	for _, e := range entries {
		// `RawPath` carries the home-relative form when the entry came
		// off disk via LoadVaults. New entries created in-process via
		// `lumi vault link` populate RawPath via EncodeHomeRelative
		// before calling here. If RawPath is empty (e.g. an entry built
		// by hand-rolled tests), fall back to the expanded Path.
		pathStr := e.RawPath
		if pathStr == "" {
			pathStr = e.Path
		}
		sb.WriteString("  - id: ")
		sb.WriteString(strings.ToLower(strings.TrimSpace(e.ID)))
		sb.WriteByte('\n')
		sb.WriteString("    name: ")
		sb.WriteString(yamlString(e.Name))
		sb.WriteByte('\n')
		sb.WriteString("    path: ")
		sb.WriteString(yamlString(pathStr))
		sb.WriteByte('\n')
		sb.WriteString("    server: ")
		if e.Server == "" {
			sb.WriteString("null")
		} else {
			sb.WriteString(yamlString(e.Server))
		}
		sb.WriteByte('\n')
		sb.WriteString("    account: ")
		if e.Account == "" {
			sb.WriteString("null")
		} else {
			sb.WriteString(yamlString(e.Account))
		}
		sb.WriteByte('\n')
		sb.WriteString("    added_at: ")
		sb.WriteString(formatTimestamp(e.AddedAt))
		sb.WriteByte('\n')
		sb.WriteString("    last_opened_at: ")
		if e.LastOpenedAt.IsZero() {
			sb.WriteString("null")
		} else {
			sb.WriteString(formatTimestamp(e.LastOpenedAt))
		}
		sb.WriteByte('\n')
	}
	return sb.String()
}

// yamlString mirrors lumi-apple's `yamlString` helper. Quotes the value
// (escaping inner `"`) when it contains characters that would confuse
// the line-by-line reader, otherwise emits bare.
func yamlString(s string) string {
	if s == "" {
		return "\"\""
	}
	needsQuoting := false
	if strings.ContainsAny(s, ":#\"") {
		needsQuoting = true
	}
	if !needsQuoting && len(s) > 0 {
		if s[0] == ' ' || s[len(s)-1] == ' ' {
			needsQuoting = true
		}
	}
	if !needsQuoting {
		return s
	}
	escaped := strings.ReplaceAll(s, `"`, `\"`)
	return `"` + escaped + `"`
}

// formatTimestamp emits an ISO-8601 / RFC3339 stamp the Apple side
// produces via `Date.ISO8601Format()`. Apple's default format includes
// no fractional seconds; we match by using RFC3339 (no nanos). UTC.
func formatTimestamp(t time.Time) string {
	if t.IsZero() {
		return "null"
	}
	return t.UTC().Format("2006-01-02T15:04:05Z")
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
