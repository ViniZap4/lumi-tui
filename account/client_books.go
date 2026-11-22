package account

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
)

// Federation is the `federation` object SPEC-LIVROS §3.5 adds to each
// vault in `GET /api/vaults`. Role is "home", "follower" or "" (null).
type Federation struct {
	Role    string `json:"role"`
	HomeURL string `json:"home_url"`
	Status  string `json:"status"`
}

// ChangedNote is one entry of `GET /api/vaults/:vault/changes`.
type ChangedNote struct {
	RemoteNote
	ChangeSeq int64 `json:"change_seq"`
}

// DeletedNote is a tombstone from `GET /api/vaults/:vault/changes`.
type DeletedNote struct {
	ID  string `json:"id"`
	Seq int64  `json:"seq"`
}

// ChangesPage is the body of `GET /api/vaults/:vault/changes?after=N`.
type ChangesPage struct {
	Notes   []ChangedNote `json:"notes"`
	Deleted []DeletedNote `json:"deleted"`
	Head    int64         `json:"head"`
	More    bool          `json:"more"`
}

// ResyncRequiredError is returned by Changes on `410 resync_required`:
// the cursor fell below the server's tombstone floor. The client must
// record Head first, relist every note, then set cursor = Head.
type ResyncRequiredError struct {
	Head int64
}

func (e *ResyncRequiredError) Error() string {
	return fmt.Sprintf("resync required (head %d)", e.Head)
}

// ErrFeedUnsupported marks a server that does not serve the wave-2 feed
// endpoints yet (404/405/501). Callers degrade instead of retrying hot.
var ErrFeedUnsupported = errors.New("server does not support the book feed yet")

// Changes calls `GET /api/vaults/:vault/changes?after=N&limit=L`.
func (c *Client) Changes(ctx context.Context, vaultID string, after int64, limit int) (*ChangesPage, error) {
	if vaultID == "" {
		return nil, fmt.Errorf("Changes: vaultID is required")
	}
	q := url.Values{}
	q.Set("after", strconv.FormatInt(after, 10))
	if limit > 0 {
		q.Set("limit", strconv.Itoa(limit))
	}
	path := fmt.Sprintf("/api/vaults/%s/changes?%s", url.PathEscape(vaultID), q.Encode())
	var page ChangesPage
	err := c.do(ctx, "GET", path, nil, true, &page)
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		switch apiErr.Status {
		case http.StatusGone:
			var body struct {
				Head int64 `json:"head"`
			}
			_ = json.Unmarshal(apiErr.Raw, &body)
			return nil, &ResyncRequiredError{Head: body.Head}
		case http.StatusNotFound, http.StatusMethodNotAllowed, http.StatusNotImplemented:
			// An unknown route answers without the JSON error envelope
			// (Fiber's plain "Cannot GET"); a real missing vault has one.
			if apiErr.Code == "" {
				return nil, fmt.Errorf("%w: %v", ErrFeedUnsupported, err)
			}
		}
	}
	if err != nil {
		return nil, err
	}
	return &page, nil
}

// GetVault calls `GET /api/vaults/:vault`.
func (c *Client) GetVault(ctx context.Context, vaultID string) (*RemoteVault, error) {
	if vaultID == "" {
		return nil, fmt.Errorf("GetVault: vaultID is required")
	}
	var v RemoteVault
	if err := c.do(ctx, "GET", "/api/vaults/"+url.PathEscape(vaultID), nil, true, &v); err != nil {
		return nil, err
	}
	return &v, nil
}

// RenameVault calls `PATCH /api/vaults/:vault {name}` (requires
// `vault.manage`). Renames the book for every member — there is no local
// alias (SPEC-LIVROS §2.3, owner decision 22/11/2026).
func (c *Client) RenameVault(ctx context.Context, vaultID, name string) (*RemoteVault, error) {
	if vaultID == "" || name == "" {
		return nil, fmt.Errorf("RenameVault: vaultID and name are required")
	}
	body := struct {
		Name string `json:"name"`
	}{Name: name}
	var v RemoteVault
	if err := c.do(ctx, "PATCH", "/api/vaults/"+url.PathEscape(vaultID), body, true, &v); err != nil {
		return nil, err
	}
	return &v, nil
}
