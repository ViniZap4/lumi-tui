package account

import (
	"context"
	"fmt"
	"net/url"
)

// NoteSnapshot is the wire shape returned by the server's snapshot and
// diff endpoints. `VectorClock` is base64-encoded; callers carry it as
// an opaque string and round-trip it back to the server on the next
// diff. Slice 2.2 treats `base_clock` as advisory — it'll become
// load-bearing once strict-conflict mode lands server-side.
type NoteSnapshot struct {
	ID          string `json:"id"`
	Path        string `json:"path"`
	Text        string `json:"text"`
	VectorClock string `json:"vector_clock"`
}

// GetNoteSnapshot pulls the CRDT-anchored snapshot for one note. The
// returned `Text` is the merged body; pair it with `VectorClock` on
// the next `ApplyNoteDiff` so the diff anchors against the right CRDT
// position.
func (c *Client) GetNoteSnapshot(ctx context.Context, vaultID, noteID string) (*NoteSnapshot, error) {
	if vaultID == "" || noteID == "" {
		return nil, fmt.Errorf("GetNoteSnapshot: vaultID and noteID are required")
	}
	path := fmt.Sprintf("/api/vaults/%s/notes/%s/snapshot",
		url.PathEscape(vaultID), url.PathEscape(noteID))
	var resp NoteSnapshot
	if err := c.do(ctx, "GET", path, nil, true, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// ApplyDiffRequest is the body shape for `POST /api/vaults/:vault/notes/:id/diff`.
// `BaseClock` is omitted when empty so the server's `omitempty` JSON tag
// round-trips cleanly — important for the first-save-after-create case
// where no prior snapshot has been observed.
type applyDiffRequest struct {
	BaseClock string `json:"base_clock,omitempty"`
	Text      string `json:"text"`
	Origin    string `json:"origin,omitempty"`
}

// ApplyNoteDiff sends the full new body to the server's diff endpoint.
// The server CRDT computes the minimal (remove, insert) op against its
// current state, preserves concurrent edits, and returns the post-
// merge snapshot. Pass `baseClock` from the last snapshot or "" when
// none has been observed. `origin` labels the source for audit; the
// canonical value for this client is "tui-diff".
func (c *Client) ApplyNoteDiff(
	ctx context.Context,
	vaultID, noteID, baseClock, text, origin string,
) (*NoteSnapshot, error) {
	if vaultID == "" || noteID == "" {
		return nil, fmt.Errorf("ApplyNoteDiff: vaultID and noteID are required")
	}
	if origin == "" {
		origin = "tui-diff"
	}
	path := fmt.Sprintf("/api/vaults/%s/notes/%s/diff",
		url.PathEscape(vaultID), url.PathEscape(noteID))
	body := applyDiffRequest{BaseClock: baseClock, Text: text, Origin: origin}
	var resp NoteSnapshot
	if err := c.do(ctx, "POST", path, body, true, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}
