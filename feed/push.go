package feed

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/vinizap/lumi/tui-client/account"
	"github.com/vinizap/lumi/tui-client/domain"
	"github.com/vinizap/lumi/tui-client/filesystem"
)

// NoteAPI is the REST surface a safe push needs (satisfied by
// *account.Client).
type NoteAPI interface {
	GetNoteSnapshot(ctx context.Context, vaultID, noteID string) (*account.NoteSnapshot, error)
	PushEdit(ctx context.Context, vaultID, noteID string, base account.EditBase, edited string) (*account.NoteSnapshot, error)
}

// Syncer is the one place that moves note bodies between a book folder
// and its server vault without ever losing an edit on either side. The
// mirror (feed catch-up), the pending-edit flush and `lumi vault sync`
// all go through it.
//
// Invariant: for every note in Cache.Notes, Hash is the body hash of the
// stored Base text (when a base exists). "Modified since base" — the
// pending-edits input of the sync state (SPEC-LIVROS §4.2) — is therefore
// BodyHash(file body) != Cache.Notes[id].Hash.
type Syncer struct {
	API     NoteAPI
	VaultID string
	Root    string
	Cache   *BookCache
	Bases   BaseStore
	// NoPush makes the mirror leave dirty notes alone instead of merging
	// them (read-only books, or an API that cannot push).
	NoPush bool
	Now    func() time.Time
}

// PushOutcome is what a Push did.
type PushOutcome int

const (
	// PushNoop: the file already matches the server (nothing to send).
	PushNoop PushOutcome = iota
	// PushMerged: the local edit was merged 3-way on the server and the
	// merged text written back.
	PushMerged
	// PushConflict: the edit could not be merged (no usable base, and
	// the remote changed too). The remote text is now in the note and the
	// local version was kept in ConflictPath.
	PushConflict
	// PushPulled: the file was clean and the server text had changed;
	// the server text was written (SyncNote only).
	PushPulled
)

func (o PushOutcome) String() string {
	return [...]string{"noop", "merged", "conflict", "pulled"}[o]
}

// PushResult reports one Push.
type PushResult struct {
	Outcome      PushOutcome
	Path         string // the note, relative to Root
	ConflictPath string // the saved local copy, relative to Root (PushConflict)
}

func (s *Syncer) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func (s *Syncer) rel(p string) string { return relOrSelf(s.Root, p) }

func sameBody(a, b string) bool { return strings.TrimSpace(a) == strings.TrimSpace(b) }

// Record makes snap the base of the note at absPath: the cache entry's
// hash and the stored base both point at snap.Text. Call it every time a
// server text is written to (or found in) the file. A snapshot without
// State drops the stored base; the next push then re-derives one.
func (s *Syncer) Record(noteID, absPath string, snap *account.NoteSnapshot) error {
	s.Cache.Notes[noteID] = NoteEntry{Path: s.rel(absPath), Hash: BodyHash(snap.Text)}
	if snap.State == "" {
		return s.Bases.Delete(noteID)
	}
	return s.Bases.Save(Base{NoteID: noteID, Text: snap.Text, VectorClock: snap.VectorClock,
		State: snap.State, SavedAt: s.now().UTC()})
}

// Forget drops everything known about a note (deleted remotely).
func (s *Syncer) Forget(noteID string) error {
	delete(s.Cache.Notes, noteID)
	return s.Bases.Delete(noteID)
}

// IsClean reports whether the body still equals what was last synced.
func (s *Syncer) IsClean(noteID string, n *domain.Note) bool {
	rec, ok := s.Cache.Notes[noteID]
	return ok && rec.Hash == BodyHash(n.Content)
}

// Pending lists the notes (relative paths, sorted) whose file was
// modified since its base and not yet pushed. Notes the server never
// knew (no cache entry) are not pending: sync does not create notes.
func (s *Syncer) Pending() []string {
	var out []string
	if s.Root == "" {
		return nil
	}
	for id, e := range s.Cache.Notes {
		rel, err := safeRel(e.Path)
		if err != nil {
			continue
		}
		n, _ := filesystem.ReadNote(filepath.Join(s.Root, rel))
		if n == nil || n.ID != id {
			continue
		}
		if BodyHash(n.Content) != e.Hash {
			out = append(out, filepath.ToSlash(rel))
		}
	}
	sort.Strings(out)
	return out
}

// PendingPaths returns absolute paths of Pending notes.
func (s *Syncer) PendingPaths() []string {
	rels := s.Pending()
	out := make([]string, len(rels))
	for i, r := range rels {
		out[i] = filepath.Join(s.Root, filepath.FromSlash(r))
	}
	return out
}

func (s *Syncer) fetch(ctx context.Context, id string) (*account.NoteSnapshot, error) {
	return s.API.GetNoteSnapshot(ctx, s.VaultID, id)
}

func baseFromSnap(id string, snap *account.NoteSnapshot) *Base {
	return &Base{NoteID: id, Text: snap.Text, VectorClock: snap.VectorClock, State: snap.State}
}

// Push sends the note at absPath to the server without overwriting
// remote edits (CRDT-CLIENT-PROTOCOL §7.1/§7.2):
//
//  1. File body equals its base → nothing to do.
//  2. No stored base: fetch the snapshot. Same text → adopt it as base.
//     If the remote text is still what this client last wrote (cache
//     hash), the local edit was made on it → use the snapshot as base.
//     Otherwise both sides changed with no common base → conflict.
//  3. POST /diff {text, base_state}: the server merges base→edited into
//     its current doc, so concurrent remote edits survive. The merged
//     text is written back (frontmatter preserved) and becomes the base.
//  4. 409 base_required / base_mismatch → re-pull. If the remote text is
//     unchanged since the base, retry once on the fresh snapshot (same
//     text, so the same diff). If both changed → conflict.
//
// Conflict ("keep both"): the local file is first copied to
// `<note>.conflict-<timestamp>.md` (without the note id, so it stays a
// local-only note), then the remote text is written into the note and
// recorded as its base. Nothing is ever dropped.
//
// Network and other errors leave the file and its base untouched: the
// note stays pending, nothing is queued.
func (s *Syncer) Push(ctx context.Context, absPath string) (PushResult, error) {
	local, err := filesystem.ReadNote(absPath)
	if local == nil {
		return PushResult{}, err
	}
	res := PushResult{Path: s.rel(absPath)}
	id := local.ID
	if id == "" {
		return res, fmt.Errorf("%s: note has no id", res.Path)
	}
	base, hasBase := s.Bases.Load(id)
	if hasBase && sameBody(local.Content, base.Text) {
		if !s.IsClean(id, local) {
			s.Cache.Notes[id] = NoteEntry{Path: res.Path, Hash: BodyHash(base.Text)}
		}
		return res, nil
	}
	if !hasBase {
		snap, err := s.fetch(ctx, id)
		if err != nil {
			return res, err
		}
		if sameBody(snap.Text, local.Content) {
			return res, s.Record(id, absPath, snap)
		}
		entry, ok := s.Cache.Notes[id]
		if !ok || entry.Hash != BodyHash(snap.Text) || snap.State == "" {
			return s.conflict(local, snap, res)
		}
		base = baseFromSnap(id, snap)
	}
	return s.pushOn(ctx, local, base, res, false)
}

// SyncNote is Push for a note the caller wants fully up to date (lumi
// vault sync): when there was nothing to push, a server text that moved
// on is pulled into the (clean) file and becomes the base.
func (s *Syncer) SyncNote(ctx context.Context, absPath string) (PushResult, error) {
	res, err := s.Push(ctx, absPath)
	if err != nil || res.Outcome != PushNoop {
		return res, err
	}
	local, err := filesystem.ReadNote(absPath)
	if local == nil {
		return res, err
	}
	if !s.IsClean(local.ID, local) {
		return res, nil // edited since Push looked; leave it pending
	}
	snap, err := s.fetch(ctx, local.ID)
	if err != nil {
		return res, err
	}
	if !sameBody(snap.Text, local.Content) {
		local.Content = snap.Text
		if err := filesystem.WriteNote(local); err != nil {
			return res, err
		}
		res.Outcome = PushPulled
	}
	return res, s.Record(local.ID, absPath, snap)
}

// sendText re-attaches the base's surrounding whitespace to the edited
// body (ReadNote trims it), so an unchanged trailing newline is not sent
// as a deletion.
func sendText(baseText, body string) string {
	trimmed := strings.TrimSpace(baseText)
	if trimmed == "" {
		return body
	}
	i := strings.Index(baseText, trimmed)
	return baseText[:i] + strings.TrimSpace(body) + baseText[i+len(trimmed):]
}

func (s *Syncer) pushOn(ctx context.Context, local *domain.Note, base *Base, res PushResult, retried bool) (PushResult, error) {
	sent := sendText(base.Text, local.Content)
	merged, err := s.API.PushEdit(ctx, s.VaultID, local.ID, base.EditBase(), sent)
	if account.IsBaseConflict(err) {
		snap, ferr := s.fetch(ctx, local.ID)
		if ferr != nil {
			return res, ferr
		}
		if sameBody(snap.Text, local.Content) {
			return res, s.Record(local.ID, local.Path, snap)
		}
		if !retried && sameBody(snap.Text, base.Text) && snap.State != "" {
			return s.pushOn(ctx, local, baseFromSnap(local.ID, snap), res, true)
		}
		return s.conflict(local, snap, res)
	}
	if err != nil {
		return res, err
	}
	// The /diff reply has no `state`; the next base comes from a fresh
	// snapshot (it may already include later remote edits, which is
	// fine: the file gets them too). Without it, keep the merged text and
	// drop the base — the next push re-derives it via the cache hash.
	snap, ferr := s.fetch(ctx, local.ID)
	if ferr != nil {
		snap = &account.NoteSnapshot{ID: merged.ID, Text: merged.Text, VectorClock: merged.VectorClock}
	}
	res.Outcome = PushMerged
	return s.writeBack(local, snap, res)
}

// writeBack writes the merged server text into the note and records it
// as the base. If the file changed while the push was in flight, that
// newer local text is kept as a conflict copy first.
func (s *Syncer) writeBack(pushed *domain.Note, snap *account.NoteSnapshot, res PushResult) (PushResult, error) {
	cur, _ := filesystem.ReadNote(pushed.Path)
	if cur == nil {
		cur = pushed
	}
	if BodyHash(cur.Content) != BodyHash(pushed.Content) {
		cp, err := s.writeConflictCopy(cur)
		if err != nil {
			return res, err
		}
		res.Outcome, res.ConflictPath = PushConflict, s.rel(cp)
	}
	if !sameBody(cur.Content, snap.Text) {
		cur.Content = snap.Text
		if err := filesystem.WriteNote(cur); err != nil {
			return res, err
		}
	}
	return res, s.Record(pushed.ID, pushed.Path, snap)
}

func (s *Syncer) conflict(local *domain.Note, snap *account.NoteSnapshot, res PushResult) (PushResult, error) {
	cp, err := s.writeConflictCopy(local)
	if err != nil {
		return res, err
	}
	res.Outcome, res.ConflictPath = PushConflict, s.rel(cp)
	note := *local
	note.Content = snap.Text
	if err := filesystem.WriteNote(&note); err != nil {
		return res, err
	}
	return res, s.Record(local.ID, local.Path, snap)
}

// ConflictSuffix marks conflict copies: `<stem>.conflict-<ts>.md`.
const ConflictSuffix = ".conflict-"

// writeConflictCopy saves n next to itself as a local-only note: same
// body and tags, a "(conflito …)" title and no `id`, so neither sync nor
// the mirror ever mistakes it for the server note.
func (s *Syncer) writeConflictCopy(n *domain.Note) (string, error) {
	ts := s.now().Format("20060102-150405")
	dir, stem := filepath.Dir(n.Path), strings.TrimSuffix(filepath.Base(n.Path), filepath.Ext(n.Path))
	dest := filepath.Join(dir, stem+ConflictSuffix+ts+".md")
	for i := 2; ; i++ {
		if _, err := os.Stat(dest); errors.Is(err, os.ErrNotExist) {
			break
		}
		dest = filepath.Join(dir, fmt.Sprintf("%s%s%s-%d.md", stem, ConflictSuffix, ts, i))
	}
	cp := *n
	cp.Path = dest
	cp.ID = ""
	if cp.Title != "" {
		cp.Title += " (conflito " + s.now().Format("2006-01-02 15:04") + ")"
	}
	if len(cp.RawFrontmatter) > 0 {
		cp.RawFrontmatter = stripIDLine(cp.RawFrontmatter)
	}
	if err := filesystem.WriteNote(&cp); err != nil {
		return "", fmt.Errorf("save conflict copy: %w", err)
	}
	return dest, nil
}

func stripIDLine(fm []byte) []byte {
	var out [][]byte
	for _, line := range bytes.Split(fm, []byte("\n")) {
		if bytes.HasPrefix(bytes.TrimSpace(line), []byte("id:")) && !bytes.HasPrefix(line, []byte(" ")) && !bytes.HasPrefix(line, []byte("\t")) {
			continue
		}
		out = append(out, line)
	}
	return bytes.Join(out, []byte("\n"))
}
