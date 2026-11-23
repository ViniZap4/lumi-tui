package feed

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/vinizap/lumi/tui-client/account"
)

// Base is the version of a note the local file was last in sync with: the
// GET /snapshot taken when the mirror, clone or sync last wrote the file,
// or right after a successful push (CRDT-CLIENT-PROTOCOL §7.1 step 1).
// State is the base64 full Yjs document; it is what POST /diff needs as
// `base_state` to merge the local edit 3-way instead of overwriting.
//
// CRDTEpoch is the lineage the state belongs to (CRDT-CLIENT-PROTOCOL
// §12.5): a push never merges a base into a different history. Empty
// for bases saved before the server had epochs (fallback check).
type Base struct {
	NoteID      string    `json:"note_id"`
	CRDTEpoch   string    `json:"crdt_epoch,omitempty"`
	Text        string    `json:"text"`
	VectorClock string    `json:"vector_clock"`
	State       string    `json:"state"`
	SavedAt     time.Time `json:"saved_at"`
}

// EditBase converts the stored base to the push seam's shape.
func (b *Base) EditBase() account.EditBase {
	return account.EditBase{Text: b.Text, VectorClock: b.VectorClock, State: b.State}
}

// BaseStore keeps one base file per note under the book's cache:
// `<UserCacheDir>/lumi/books/<bookID>.bases/<sha256(noteID)>.json`, 0600.
// Never in the notes folder (it would travel with the notes through
// iCloud / Syncthing / Git) and never in books.yaml. A zero BaseStore
// (Dir "") stores nothing.
type BaseStore struct {
	Dir string
}

// BasesFor returns the base store of one book.
func BasesFor(cacheDir, bookID string) BaseStore {
	if cacheDir == "" || bookID == "" {
		return BaseStore{}
	}
	return BaseStore{Dir: filepath.Join(cacheDir, bookID+".bases")}
}

func (s BaseStore) file(noteID string) string {
	h := sha256.Sum256([]byte(noteID))
	return filepath.Join(s.Dir, hex.EncodeToString(h[:20])+".json")
}

// Load returns the stored base for noteID. A missing, unreadable or
// foreign file is "no base" — the cache is derived data.
func (s BaseStore) Load(noteID string) (*Base, bool) {
	if s.Dir == "" {
		return nil, false
	}
	data, err := os.ReadFile(s.file(noteID))
	if err != nil {
		return nil, false
	}
	var b Base
	if json.Unmarshal(data, &b) != nil || b.NoteID != noteID || b.State == "" {
		return nil, false
	}
	return &b, true
}

// Save writes the base atomically at 0600.
func (s BaseStore) Save(b Base) error {
	if s.Dir == "" {
		return nil
	}
	if err := os.MkdirAll(s.Dir, 0o700); err != nil {
		return err
	}
	data, err := json.Marshal(b)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(s.Dir, ".base-*.json")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp.Name(), s.file(b.NoteID)); err != nil {
		return fmt.Errorf("save base: %w", err)
	}
	return nil
}

// Delete removes a note's base (missing is fine).
func (s BaseStore) Delete(noteID string) error {
	if s.Dir == "" {
		return nil
	}
	if err := os.Remove(s.file(noteID)); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}
