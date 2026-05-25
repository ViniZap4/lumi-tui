package account

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
)

// RemoteVault mirrors the server's vault DTO (see
// server/internal/vaults/handlers.go's vaultDTO). Only the fields used
// by `lumi vault clone` and the future TUI multi-vault surface are
// captured here; the role/member arrays come from separate endpoints.
type RemoteVault struct {
	ID        string `json:"id"`
	Slug      string `json:"slug"`
	Name      string `json:"name"`
	CreatedBy string `json:"created_by"`
	CreatedAt string `json:"created_at"`
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
