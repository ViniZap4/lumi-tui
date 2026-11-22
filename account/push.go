package account

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
)

// EditBase is what the user's edit was based on: the snapshot fetched
// before the edit started (CRDT-CLIENT-PROTOCOL §7.1 step 1).
//
// State is the base64 full Yjs state from GET /snapshot `state`. It is
// what makes the push a real 3-way merge: the server rebuilds the base
// document from it, diffs Text → edited on that base and merges the
// resulting ops into the current document, so concurrent remote edits
// survive (protocol §7.2, vector V9b).
type EditBase struct {
	Text        string
	VectorClock string
	State       string
}

// PushOrigin is the origin label the TUI's 3-way pushes carry.
const PushOrigin = "tui"

// ErrBaseRequired is returned by PushEdit when the server answers
// 409 base_required (no usable base), and locally when the caller has no
// base state at all — the TUI never sends bare text, because the server
// would have nothing to merge against (vector V9a).
var ErrBaseRequired = errors.New("base_required")

// ErrBaseMismatch is returned by PushEdit on 409 base_mismatch: the base
// state carries changes the server does not descend from (server reset,
// switched replica). The caller must re-pull before pushing again.
var ErrBaseMismatch = errors.New("base_mismatch")

// ErrBaseTooOld is returned by PushEdit on 409 base_too_old: the server
// can no longer reconstruct the base state (history compacted). Like
// ErrBaseMismatch, the caller must re-pull the snapshot and redo the
// edit on it.
var ErrBaseTooOld = errors.New("base_too_old")

// IsBaseConflict reports whether err is one of the 409s that mean
// "re-pull the snapshot before pushing again".
func IsBaseConflict(err error) bool {
	return errors.Is(err, ErrBaseRequired) || errors.Is(err, ErrBaseMismatch) ||
		errors.Is(err, ErrBaseTooOld)
}

// baseConflictError keeps the server's *APIError reachable (errors.As)
// while matching the sentinel (errors.Is).
type baseConflictError struct {
	sentinel error
	api      *APIError
}

func (e *baseConflictError) Error() string {
	if e.api == nil {
		return e.sentinel.Error()
	}
	return e.api.Error()
}
func (e *baseConflictError) Is(target error) bool { return target == e.sentinel }
func (e *baseConflictError) Unwrap() error {
	if e.api == nil {
		return nil
	}
	return e.api
}

type pushEditRequest struct {
	Text      string `json:"text"`
	BaseState string `json:"base_state"`
	Origin    string `json:"origin"`
}

// PushEdit is the single seam through which the TUI sends an edited note
// body to the server. Every push path (lumi vault sync, the feed's
// pending-edit flush, the mirror's dirty-note merge) goes through here.
//
// It sends POST /diff {text, base_state, origin: "tui"}: the server does
// the 3-way merge of CRDT-CLIENT-PROTOCOL §7.1 steps 4–6 itself (§7.2,
// §10 S1), so the TUI needs no CRDT library. The returned snapshot holds
// the merged text, the new vector clock and — on current servers — the
// merged `state`, which is the next base as is. Older servers omit
// `state`; callers then re-fetch GET /snapshot for the next base.
//
// 409 base_required / base_mismatch / base_too_old come back as
// ErrBaseRequired / ErrBaseMismatch / ErrBaseTooOld. A base without State
// never reaches the wire.
func (c *Client) PushEdit(ctx context.Context, vaultID, noteID string, base EditBase, edited string) (*NoteSnapshot, error) {
	if vaultID == "" || noteID == "" {
		return nil, fmt.Errorf("PushEdit: vaultID and noteID are required")
	}
	if base.State == "" {
		return nil, &baseConflictError{sentinel: ErrBaseRequired}
	}
	path := fmt.Sprintf("/api/vaults/%s/notes/%s/diff",
		url.PathEscape(vaultID), url.PathEscape(noteID))
	var resp NoteSnapshot
	err := c.do(ctx, "POST", path, pushEditRequest{Text: edited, BaseState: base.State, Origin: PushOrigin}, true, &resp)
	var apiErr *APIError
	if errors.As(err, &apiErr) && apiErr.Status == http.StatusConflict {
		switch apiErr.Code {
		case "base_required":
			return nil, &baseConflictError{sentinel: ErrBaseRequired, api: apiErr}
		case "base_mismatch":
			return nil, &baseConflictError{sentinel: ErrBaseMismatch, api: apiErr}
		case "base_too_old":
			return nil, &baseConflictError{sentinel: ErrBaseTooOld, api: apiErr}
		}
	}
	if err != nil {
		return nil, err
	}
	return &resp, nil
}
