package feed

import (
	"context"
	"errors"

	"github.com/vinizap/lumi/tui-client/account"
)

// ChangesAPI is the REST surface catch-up needs (satisfied by
// *account.Client).
type ChangesAPI interface {
	Changes(ctx context.Context, vaultID string, after int64, limit int) (*account.ChangesPage, error)
	ListAllNotes(ctx context.Context, vaultID string) ([]account.RemoteNote, error)
	GetNoteSnapshot(ctx context.Context, vaultID, noteID string) (*account.NoteSnapshot, error)
}

// ChangesPageSize is the `limit` sent to `GET .../changes`.
const ChangesPageSize = 500

// CatchUpResult reports one catch-up pass.
type CatchUpResult struct {
	Head   int64
	Resync bool // a 410 forced a full relist
	Stats  MirrorStats
}

// CatchUp advances cache.Cursor to the server head for one book
// (SPEC-LIVROS §3.5):
//
//   - pages `GET .../changes?after=cursor` until `more` is false, applying
//     notes and tombstones to the mirror folder (when root != "");
//   - the cursor only moves after a page is fully applied, so a failure
//     mid-page replays it next time (applying is idempotent);
//   - on `410 resync_required` it records the reported head FIRST, relists
//     every note, applies them, deletes clean notes the server no longer
//     has, and sets cursor = that head; changes that land in between come
//     back in the next `changes` call.
//
// This form never pushes: dirty notes are reported and left alone, and no
// bases are stored. The feed manager uses Syncer.CatchUp.
func CatchUp(ctx context.Context, api ChangesAPI, vaultID, root string, cache *BookCache) (CatchUpResult, error) {
	s := &Syncer{API: pullOnly{api}, VaultID: vaultID, Root: root, Cache: cache, NoPush: true}
	return s.CatchUp(ctx, api)
}

// pullOnly adapts a ChangesAPI that cannot push.
type pullOnly struct{ ChangesAPI }

func (pullOnly) PushEdit(context.Context, string, string, account.EditBase, string) (*account.NoteSnapshot, error) {
	return nil, errors.New("push not supported")
}

// CatchUp is CatchUp with this syncer's bases and push policy: clean
// notes are pulled and their snapshot stored as base; dirty notes the
// server also changed are merged (Syncer.Push) unless NoPush.
func (s *Syncer) CatchUp(ctx context.Context, api ChangesAPI) (CatchUpResult, error) {
	var res CatchUpResult
	var m *Mirror
	vaultID, cache := s.VaultID, s.Cache
	if s.Root != "" {
		m = &Mirror{Syncer: s}
	}
	resyncs := 0
	for {
		page, err := api.Changes(ctx, vaultID, cache.Cursor, ChangesPageSize)
		var resync *account.ResyncRequiredError
		if errors.As(err, &resync) {
			if resyncs++; resyncs > 1 {
				return res, errors.New("changes: 410 again right after a resync")
			}
			res.Resync = true
			head := resync.Head
			notes, err := api.ListAllNotes(ctx, vaultID)
			if err != nil {
				return res, err
			}
			if m != nil {
				live := map[string]bool{}
				for _, n := range notes {
					live[n.ID] = true
					if err := m.ApplyNote(ctx, n, &res.Stats); err != nil {
						return res, err
					}
				}
				for id := range cache.Notes {
					if !live[id] {
						if err := m.ApplyDelete(id, &res.Stats); err != nil {
							return res, err
						}
					}
				}
			}
			cache.Cursor = head
			res.Head = head
			// Pick up whatever changed while we were relisting.
			continue
		}
		if err != nil {
			return res, err
		}
		if m != nil {
			for _, n := range page.Notes {
				if err := m.ApplyNote(ctx, n.RemoteNote, &res.Stats); err != nil {
					return res, err
				}
			}
			for _, d := range page.Deleted {
				if err := m.ApplyDelete(d.ID, &res.Stats); err != nil {
					return res, err
				}
			}
		}
		next := cache.Cursor
		for _, n := range page.Notes {
			if n.ChangeSeq > next {
				next = n.ChangeSeq
			}
		}
		for _, d := range page.Deleted {
			if d.Seq > next {
				next = d.Seq
			}
		}
		if !page.More && page.Head > next {
			next = page.Head
		}
		res.Head = page.Head
		if next <= cache.Cursor && page.More {
			return res, errors.New("changes: server returned more=true without progress")
		}
		cache.Cursor = next
		if !page.More {
			return res, nil
		}
	}
}
