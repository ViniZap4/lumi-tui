package account

import (
	"context"
	"errors"
	"time"
)

// ErrInvalidLink is the server's single answer for an unknown, used,
// expired or mismatched device-link code.
var ErrInvalidLink = errors.New("device link is invalid or expired")

// DeviceLink is a freshly minted code (POST /api/auth/device-links).
type DeviceLink struct {
	Code      string    `json:"code"`
	ExpiresAt time.Time `json:"expires_at"`
}

// IssueDeviceLink mints a single-use code bound to challenge for another
// client of the same user (SPEC-LIVROS §8.4). Requires a token.
func (c *Client) IssueDeviceLink(ctx context.Context, challenge, label string) (*DeviceLink, error) {
	var out DeviceLink
	body := map[string]string{"challenge": challenge, "label": label}
	if err := c.do(ctx, "POST", "/api/auth/device-links", body, true, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// RedeemDeviceLink trades a code + PKCE verifier for a new session of
// this client. No token needed.
func (c *Client) RedeemDeviceLink(ctx context.Context, code, verifier, label string) (*SessionResponse, error) {
	var out SessionResponse
	body := map[string]string{"code": code, "verifier": verifier, "label": label}
	err := c.do(ctx, "POST", "/api/auth/device-links/redeem", body, false, &out)
	if errors.Is(err, ErrUnauthorized) {
		return nil, ErrInvalidLink
	}
	if err != nil {
		return nil, err
	}
	return &out, nil
}

// LogoutDevice ends this session and every session linked to it by a
// device link ("sign out on this Mac", POST /api/auth/logout
// {"scope":"device"}).
func (c *Client) LogoutDevice(ctx context.Context) error {
	return c.do(ctx, "POST", "/api/auth/logout", map[string]string{"scope": "device"}, true, nil)
}
