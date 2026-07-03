package account

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
)

// RemoteVault mirrors the server's vault DTO (see
// server/internal/vaults/handlers.go's vaultDTO). Only the fields used
// by `lumi vault clone` and the future TUI multi-vault surface are
// captured here; the role/member arrays come from separate endpoints.
//
// v3 adds ownership (`owner_user_id`) and share-a-copy provenance
// (`copied_from`). CopiedFrom is kept as raw JSON — the CLI only needs
// to know whether provenance exists, and the server-side JSONB shape
// may still grow fields.
type RemoteVault struct {
	ID          string          `json:"id"`
	Slug        string          `json:"slug"`
	Name        string          `json:"name"`
	CreatedBy   string          `json:"created_by"`
	CreatedAt   string          `json:"created_at"`
	OwnerUserID string          `json:"owner_user_id"`
	CopiedFrom  json.RawMessage `json:"copied_from,omitempty"`
}

// VaultMember mirrors one row of `GET /api/vaults/:vault/members`. Only
// the fields the CLI needs (username → user_id resolution and display)
// are captured.
type VaultMember struct {
	VaultID     string `json:"vault_id"`
	UserID      string `json:"user_id"`
	Username    string `json:"username"`
	DisplayName string `json:"display_name"`
	RoleID      string `json:"role_id"`
	RoleName    string `json:"role_name"`
}

// memberListResponse is the envelope returned by `GET /api/vaults/:vault/members`.
type memberListResponse struct {
	Members []VaultMember `json:"members"`
}

// vaultListResponse is the envelope returned by `GET /api/vaults`.
type vaultListResponse struct {
	Vaults []RemoteVault `json:"vaults"`
}

// RemoteNote mirrors the server's note DTO (id, vault_id, path, title,
// created_at, updated_at). `lumi vault clone` uses `Path` to decide the
// on-disk location of the downloaded markdown file.
type RemoteNote struct {
	ID        string `json:"id"`
	VaultID   string `json:"vault_id"`
	Path      string `json:"path"`
	Title     string `json:"title"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

// noteListResponse is the envelope returned by `GET /api/vaults/:vault/notes`.
type noteListResponse struct {
	Notes  []RemoteNote `json:"notes"`
	Limit  int          `json:"limit"`
	Offset int          `json:"offset"`
}

// NoteContent mirrors the body shape of `GET /api/vaults/:vault/notes/:id/content`.
// Slice 4 step 3 only needs `Body`; `Frontmatter` is decoded as raw JSON
// for forward-compat but ignored by clone. `Path` lets the caller
// double-check the on-disk write target.
type NoteContent struct {
	ID    string `json:"id"`
	Path  string `json:"path"`
	Body  string `json:"body"`
}

// ListVaults pulls the vault list visible to the signed-in user.
func (c *Client) ListVaults(ctx context.Context) ([]RemoteVault, error) {
	var resp vaultListResponse
	if err := c.do(ctx, "GET", "/api/vaults", nil, true, &resp); err != nil {
		return nil, err
	}
	return resp.Vaults, nil
}

// ListMembers pulls the member list of a vault. Requires a token; the
// server gates the endpoint on vault visibility.
func (c *Client) ListMembers(ctx context.Context, vaultID string) ([]VaultMember, error) {
	if vaultID == "" {
		return nil, fmt.Errorf("ListMembers: vaultID is required")
	}
	path := fmt.Sprintf("/api/vaults/%s/members", url.PathEscape(vaultID))
	var resp memberListResponse
	if err := c.do(ctx, "GET", path, nil, true, &resp); err != nil {
		return nil, err
	}
	return resp.Members, nil
}

// TransferOwnership calls `POST /api/vaults/:vault/transfer-ownership`.
// Owner-only server-side (403 otherwise); the target must already be a
// member (400 otherwise). Returns the updated vault DTO.
func (c *Client) TransferOwnership(ctx context.Context, vaultID, userID string) (*RemoteVault, error) {
	if vaultID == "" || userID == "" {
		return nil, fmt.Errorf("TransferOwnership: vaultID and userID are required")
	}
	path := fmt.Sprintf("/api/vaults/%s/transfer-ownership", url.PathEscape(vaultID))
	body := struct {
		UserID string `json:"user_id"`
	}{UserID: userID}
	var resp RemoteVault
	if err := c.do(ctx, "POST", path, body, true, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// CopyVault calls `POST /api/vaults/:vault/copies` — the v3 share-a-copy
// endpoint (capability `vault.export`). The server forks the vault's
// current FS state into a new vault owned by `recipientUsername`; no
// membership or live link is created. Returns the fork's vault DTO.
// Unknown recipients surface as *APIError with Code "recipient_not_found".
func (c *Client) CopyVault(ctx context.Context, vaultID, recipientUsername string) (*RemoteVault, error) {
	if vaultID == "" || recipientUsername == "" {
		return nil, fmt.Errorf("CopyVault: vaultID and recipientUsername are required")
	}
	path := fmt.Sprintf("/api/vaults/%s/copies", url.PathEscape(vaultID))
	body := struct {
		RecipientUsername string `json:"recipient_username"`
	}{RecipientUsername: recipientUsername}
	var resp RemoteVault
	if err := c.do(ctx, "POST", path, body, true, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// ListNotes pulls one page of notes from a vault. `limit` defaults to
// the server-side default (100) when zero; `offset` is sent verbatim.
func (c *Client) ListNotes(ctx context.Context, vaultID string, limit, offset int) ([]RemoteNote, error) {
	if vaultID == "" {
		return nil, fmt.Errorf("ListNotes: vaultID is required")
	}
	q := url.Values{}
	if limit > 0 {
		q.Set("limit", strconv.Itoa(limit))
	}
	if offset > 0 {
		q.Set("offset", strconv.Itoa(offset))
	}
	path := fmt.Sprintf("/api/vaults/%s/notes", url.PathEscape(vaultID))
	if encoded := q.Encode(); encoded != "" {
		path += "?" + encoded
	}
	var resp noteListResponse
	if err := c.do(ctx, "GET", path, nil, true, &resp); err != nil {
		return nil, err
	}
	return resp.Notes, nil
}

// ListAllNotes walks all paginated pages of `ListNotes`, returning the
// full set in stable server order. Safe for medium-sized vaults; very
// large vaults (>>10k notes) should switch to a streaming variant —
// noted for a later slice.
func (c *Client) ListAllNotes(ctx context.Context, vaultID string) ([]RemoteNote, error) {
	const pageSize = 200 // server caps higher; this is a round trip-vs-page-count compromise
	var all []RemoteNote
	offset := 0
	for {
		page, err := c.ListNotes(ctx, vaultID, pageSize, offset)
		if err != nil {
			return nil, err
		}
		all = append(all, page...)
		if len(page) < pageSize {
			return all, nil
		}
		offset += len(page)
	}
}

// GetNoteContent fetches the markdown body for a single note.
func (c *Client) GetNoteContent(ctx context.Context, vaultID, noteID string) (*NoteContent, error) {
	if vaultID == "" || noteID == "" {
		return nil, fmt.Errorf("GetNoteContent: vaultID and noteID are required")
	}
	path := fmt.Sprintf("/api/vaults/%s/notes/%s/content",
		url.PathEscape(vaultID), url.PathEscape(noteID))
	var resp NoteContent
	if err := c.do(ctx, "GET", path, nil, true, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}
