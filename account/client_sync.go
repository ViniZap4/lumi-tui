package account

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
)

// NoteSnapshot is the wire shape returned by the server's snapshot and
// diff endpoints. `VectorClock` and `State` are base64-encoded and
// carried as opaque strings. `State` (the full encoded Yjs document) is
// the base of a 3-way PushEdit.
//
// CRDTEpoch names the CRDT history ("lineage") the state belongs to
// (CRDT-CLIENT-PROTOCOL §12). Opaque, compared only for equality;
// empty on servers that predate migration 0008.
type NoteSnapshot struct {
	ID          string `json:"id"`
	Path        string `json:"path"`
	CRDTEpoch   string `json:"crdt_epoch,omitempty"`
	Text        string `json:"text"`
	VectorClock string `json:"vector_clock"`
	State       string `json:"state,omitempty"`
}

// NoteStateVector is the body of `GET …/notes/:id/state-vector`
// (protocol §12.4): the lineage and the lib0-v1 state vector (base64),
// without text or full state — a fraction of a snapshot's cost.
type NoteStateVector struct {
	ID          string `json:"id"`
	CRDTEpoch   string `json:"crdt_epoch"`
	StateVector string `json:"state_vector"`
}

// ErrStateVectorUnsupported marks a server without the /state-vector
// route (404/405/501 without the JSON error envelope). Callers fall
// back to GET /snapshot (§12.4).
var ErrStateVectorUnsupported = errors.New("server does not serve /state-vector")

// epochRetries bounds the retries on `409 conflict` from the read
// endpoints: the server answers it when a new CRDT history started
// while it was reading (§12.3, "Retry").
const epochRetries = 2

// isEpochUnstable reports the §12.3 "lineage changed mid-read" 409.
// The base_* 409s of /diff carry their own codes and never match.
func isEpochUnstable(err error) bool {
	var apiErr *APIError
	return errors.As(err, &apiErr) && apiErr.Status == http.StatusConflict && apiErr.Code == "conflict"
}

// GetNoteSnapshot pulls the CRDT-anchored snapshot for one note: the
// merged body, its state vector, the full state (the next push's base)
// and the lineage they belong to.
func (c *Client) GetNoteSnapshot(ctx context.Context, vaultID, noteID string) (*NoteSnapshot, error) {
	if vaultID == "" || noteID == "" {
		return nil, fmt.Errorf("GetNoteSnapshot: vaultID and noteID are required")
	}
	path := fmt.Sprintf("/api/vaults/%s/notes/%s/snapshot",
		url.PathEscape(vaultID), url.PathEscape(noteID))
	var err error
	for i := 0; i <= epochRetries; i++ {
		var resp NoteSnapshot
		if err = c.do(ctx, "GET", path, nil, true, &resp); err == nil {
			return &resp, nil
		}
		if !isEpochUnstable(err) {
			return nil, err
		}
	}
	return nil, err
}

// GetStateVector calls `GET …/notes/:id/state-vector`. It returns
// ErrStateVectorUnsupported on servers without the route; a missing
// note is the usual *APIError 404 with code "not_found".
func (c *Client) GetStateVector(ctx context.Context, vaultID, noteID string) (*NoteStateVector, error) {
	if vaultID == "" || noteID == "" {
		return nil, fmt.Errorf("GetStateVector: vaultID and noteID are required")
	}
	path := fmt.Sprintf("/api/vaults/%s/notes/%s/state-vector",
		url.PathEscape(vaultID), url.PathEscape(noteID))
	var err error
	for i := 0; i <= epochRetries; i++ {
		var resp NoteStateVector
		err = c.do(ctx, "GET", path, nil, true, &resp)
		if err == nil {
			return &resp, nil
		}
		var apiErr *APIError
		if errors.As(err, &apiErr) && apiErr.Code == "" {
			switch apiErr.Status {
			case http.StatusNotFound, http.StatusMethodNotAllowed, http.StatusNotImplemented:
				return nil, fmt.Errorf("%w: %v", ErrStateVectorUnsupported, err)
			}
		}
		if !isEpochUnstable(err) {
			return nil, err
		}
	}
	return nil, err
}
