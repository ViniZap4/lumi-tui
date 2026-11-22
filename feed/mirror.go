package feed

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/vinizap/lumi/tui-client/account"
	"github.com/vinizap/lumi/tui-client/domain"
	"github.com/vinizap/lumi/tui-client/filesystem"
)

// Mirror applies server changes to a book's local folder (SPEC-LIVROS §6
// T3: "pull changed notes into the mirror folder only when the note is
// not dirty").
//
// Dirty rule: a local file is clean only when its current body hashes to
// what this client last synced (BookCache.Notes). Clean notes are
// overwritten with the server text and that snapshot becomes their base.
// A dirty note whose server text also changed is merged through
// Syncer.Push (3-way on the server, or "keep both" when there is no
// usable base) — unless NoPush is set, in which case it is reported and
// left alone. A file with no record is dirty unless its body already
// equals the server text, so the first catch-up after a clone never
// clobbers anything.
type Mirror struct {
	*Syncer

	index map[string]string // note id -> absolute path
}

// MirrorStats summarises one apply pass.
type MirrorStats struct {
	Written, Deleted, Moved int
	Merged                  int      // dirty notes merged with the server (3-way)
	Dirty                   []string // relative paths left alone
	Conflicts               []Conflict
	Errors                  []string // per-note push failures (note left dirty)
}

// Conflict is a note whose local version had to be set aside.
type Conflict struct {
	Path string // the note (now holding the server text)
	Copy string // the saved local version
}

// Changed reports whether any file was touched.
func (s MirrorStats) Changed() bool { return s.Written+s.Deleted+s.Moved > 0 }

func (m *Mirror) buildIndex() {
	if m.index != nil {
		return
	}
	m.index = map[string]string{}
	_ = filepath.WalkDir(m.Root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if p != m.Root && strings.HasPrefix(d.Name(), ".") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(strings.ToLower(d.Name()), ".md") {
			return nil
		}
		if n, _ := filesystem.ReadNote(p); n != nil && n.ID != "" {
			if _, dup := m.index[n.ID]; !dup {
				m.index[n.ID] = p
			}
		}
		return nil
	})
}

func safeRel(p string) (string, error) {
	if p == "" {
		return "", fmt.Errorf("empty path")
	}
	clean := filepath.Clean(filepath.FromSlash(p))
	if filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("unsafe path %q", p)
	}
	if !strings.HasSuffix(strings.ToLower(clean), ".md") {
		clean += ".md"
	}
	return clean, nil
}

// ApplyNote brings one changed note into the folder.
func (m *Mirror) ApplyNote(ctx context.Context, rn account.RemoteNote, st *MirrorStats) error {
	m.buildIndex()
	rel, err := safeRel(rn.Path)
	if err != nil {
		return err
	}
	dest := filepath.Join(m.Root, rel)
	localPath, exists := m.index[rn.ID]
	var local *domain.Note
	if exists {
		local, _ = filesystem.ReadNote(localPath)
		exists = local != nil
	}
	if exists && !m.IsClean(rn.ID, local) {
		snap, err := m.fetch(ctx, rn.ID)
		if err != nil {
			return err
		}
		if sameBody(snap.Text, local.Content) {
			// Same text, no record: adopt it as clean.
			return m.Record(rn.ID, localPath, snap)
		}
		if rec, ok := m.Cache.Notes[rn.ID]; ok && rec.Hash == BodyHash(snap.Text) {
			// Only the local side changed: nothing to pull. Pushing it is
			// the pending flush's job, not the mirror's.
			if m.NoPush {
				st.Dirty = append(st.Dirty, relOrSelf(m.Root, localPath))
			}
			return nil
		}
		if m.NoPush {
			st.Dirty = append(st.Dirty, relOrSelf(m.Root, localPath))
			return nil
		}
		res, err := m.Push(ctx, localPath)
		if err != nil {
			if isNetworkErr(err) || ctx.Err() != nil {
				return err
			}
			st.Dirty = append(st.Dirty, relOrSelf(m.Root, localPath))
			st.Errors = append(st.Errors, relOrSelf(m.Root, localPath)+": "+err.Error())
			return nil
		}
		switch res.Outcome {
		case PushMerged:
			st.Written++
			st.Merged++
		case PushConflict:
			st.Written++
			st.Conflicts = append(st.Conflicts, Conflict{Path: res.Path, Copy: res.ConflictPath})
		}
		return nil
	}
	snap, err := m.fetch(ctx, rn.ID)
	if err != nil {
		return err
	}
	text := snap.Text
	if exists {
		if localPath != dest {
			if _, err := os.Stat(dest); err == nil {
				// Target occupied by another file: update in place.
				dest = localPath
			} else {
				if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
					return err
				}
				if err := os.Rename(localPath, dest); err != nil {
					return err
				}
				st.Moved++
			}
		}
		local.Path = dest
		local.Title = firstNonEmpty(rn.Title, local.Title)
		local.Content = text
		if t, err := time.Parse(time.RFC3339, rn.UpdatedAt); err == nil {
			local.UpdatedAt = t
		}
		if err := filesystem.WriteNote(local); err != nil {
			return err
		}
	} else {
		if _, err := os.Stat(dest); err == nil {
			// An unrelated local file sits at the path: never clobber.
			st.Dirty = append(st.Dirty, rel)
			return nil
		}
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			return err
		}
		n := &domain.Note{ID: rn.ID, Title: rn.Title, Path: dest, Content: text, HadFrontmatter: true}
		if t, err := time.Parse(time.RFC3339, rn.CreatedAt); err == nil {
			n.CreatedAt = t
		}
		if t, err := time.Parse(time.RFC3339, rn.UpdatedAt); err == nil {
			n.UpdatedAt = t
		}
		if err := filesystem.WriteNote(n); err != nil {
			return err
		}
	}
	m.index[rn.ID] = dest
	st.Written++
	return m.Record(rn.ID, dest, snap)
}

// ApplyDelete removes a tombstoned note when it is clean.
func (m *Mirror) ApplyDelete(id string, st *MirrorStats) error {
	m.buildIndex()
	p, ok := m.index[id]
	if !ok {
		return m.Forget(id)
	}
	n, _ := filesystem.ReadNote(p)
	if n == nil {
		return nil
	}
	if !m.IsClean(id, n) {
		st.Dirty = append(st.Dirty, relOrSelf(m.Root, p))
		return m.Forget(id) // now a local-only note
	}
	if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
		return err
	}
	delete(m.index, id)
	st.Deleted++
	return m.Forget(id)
}

func relOrSelf(root, p string) string {
	if r, err := filepath.Rel(root, p); err == nil {
		return filepath.ToSlash(r)
	}
	return p
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
