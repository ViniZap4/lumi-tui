package books

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/vinizap/lumi/tui-client/account"
	"gopkg.in/yaml.v3"
)

// ImportVaults builds a registry from vaults.yaml rows (SPEC-LIVROS §2.5).
// It is a pure, read-only transformation: the caller writes books.yaml and
// leaves vaults.yaml in place for older clients.
//
//   - A row with `server` becomes `kind: bound`; vault_id comes from
//     `<path>/.lumi/vault.yaml` (`server.vault_id`) via readVaultID. When
//     that is missing the book keeps `vault_id: null` and shows the
//     "Erro: ligação incompleta" state until relinked.
//   - A row without `server` becomes `kind: local`.
//   - Ids, names, paths (as written, `~/` kept), accounts and timestamps
//     are copied verbatim; server URLs are normalised.
//   - `sort` follows the incoming order (most recently used first) in
//     steps of 10.
//   - At most one bound book per vault_id (§2.4): a second row resolving
//     to the same vault_id is imported as a local book so its folder is
//     not lost.
func ImportVaults(entries []account.VaultEntry, readVaultID func(absPath string) string) *Registry {
	r := &Registry{Version: FormatVersion}
	for i, e := range entries {
		raw := e.RawPath
		if raw == "" {
			raw = EncodeHome(e.Path)
		}
		b := Book{
			ID:           strings.ToLower(e.ID),
			Name:         e.Name,
			Kind:         KindLocal,
			Path:         raw,
			Sort:         (i + 1) * SortStep,
			AddedAt:      truncUTC(e.AddedAt),
			LastOpenedAt: truncUTC(e.LastOpenedAt),
		}
		if e.IsServerBound() {
			b.Kind = KindBound
			if n, err := account.NormalizeServerURL(e.Server); err == nil {
				b.ServerURL = n
			} else {
				b.ServerURL = e.Server
			}
			b.Account = e.Account
			if readVaultID != nil {
				b.VaultID = strings.ToLower(readVaultID(e.Path))
			}
			if b.VaultID != "" && r.ByVaultID(b.VaultID) != nil {
				b.Unbind()
			}
		}
		if r.Get(b.ID) != nil {
			continue // duplicate id in a hand-edited vaults.yaml
		}
		r.Books = append(r.Books, b)
	}
	r.sortBooks()
	return r
}

// ReadFolderVaultID returns `server.vault_id` from `<dir>/.lumi/vault.yaml`
// (the file the server and `lumi vault clone` leave in a vault folder), or
// "" when absent or unreadable.
func ReadFolderVaultID(dir string) string {
	if dir == "" {
		return ""
	}
	data, err := os.ReadFile(filepath.Join(dir, ".lumi", "vault.yaml"))
	if err != nil {
		return ""
	}
	var doc struct {
		Server *struct {
			VaultID string `yaml:"vault_id"`
		} `yaml:"server"`
	}
	if err := yaml.Unmarshal(data, &doc); err != nil || doc.Server == nil {
		return ""
	}
	return strings.TrimSpace(doc.Server.VaultID)
}

// Store bundles the registry with the paths it was loaded from, so callers
// can mutate and Save without re-resolving paths.
type Store struct {
	Path     string
	Registry *Registry
	// Migrated is true when this load created books.yaml from vaults.yaml.
	Migrated bool
}

// Save writes the registry back to Store.Path.
func (s *Store) Save() error { return s.Registry.Save(s.Path) }

// LoadOrMigrate loads books.yaml at booksPath. When it does not exist and
// vaultsPath has rows, it imports them once (§2.5) and writes books.yaml.
// When neither exists it returns an empty registry without writing.
func LoadOrMigrate(booksPath, vaultsPath string) (*Store, error) {
	reg, err := Load(booksPath)
	if err != nil {
		return nil, err
	}
	if reg != nil {
		return &Store{Path: booksPath, Registry: reg}, nil
	}
	st := &Store{Path: booksPath, Registry: &Registry{Version: FormatVersion}}
	if vaultsPath == "" {
		return st, nil
	}
	entries, err := account.LoadVaultsFrom(vaultsPath)
	if err != nil {
		return nil, fmt.Errorf("import vaults.yaml: %w", err)
	}
	if len(entries) == 0 {
		return st, nil
	}
	st.Registry = ImportVaults(entries, ReadFolderVaultID)
	if err := st.Save(); err != nil {
		return nil, fmt.Errorf("write migrated books.yaml: %w", err)
	}
	st.Migrated = true
	return st, nil
}

// Open is LoadOrMigrate at the default locations.
func Open() (*Store, error) {
	bp, err := DefaultPath()
	if err != nil {
		return nil, err
	}
	vp, err := account.VaultsPath()
	if err != nil {
		return nil, err
	}
	return LoadOrMigrate(bp, vp)
}

func truncUTC(t time.Time) time.Time {
	if t.IsZero() {
		return t
	}
	return t.UTC().Truncate(time.Second)
}
