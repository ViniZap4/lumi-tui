package account

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// LoginRequest matches the server's `POST /api/auth/login` body.
type loginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// SessionResponse matches the server's session envelope returned by
// `/api/auth/login` and `/api/auth/register`.
type SessionResponse struct {
	Token     string    `json:"token"`
	ExpiresAt time.Time `json:"expires_at,omitempty"`
	User      User      `json:"user"`
}

// User mirrors the server's user DTO.
type User struct {
	ID          string `json:"id"`
	Username    string `json:"username"`
	DisplayName string `json:"display_name"`
}

// errorEnvelope matches the server's `{ "error": code, "detail": msg }`.
type errorEnvelope struct {
	Error  string `json:"error"`
	Detail string `json:"detail,omitempty"`
}

// APIError carries a server-side failure code. Caller-friendly errors
// surface via `Code` (e.g. "invalid_credentials", "validation_failed") and
// optional `Detail`.
type APIError struct {
	Status int
	Code   string
	Detail string
}

func (e *APIError) Error() string {
	if e.Detail != "" {
		return fmt.Sprintf("%s: %s (HTTP %d)", e.Code, e.Detail, e.Status)
	}
	return fmt.Sprintf("%s (HTTP %d)", e.Code, e.Status)
}

// ErrUnauthorized is the canonical "credentials wrong / token expired"
// signal. Returned alongside an APIError so callers can either type-assert
// for the typed error or check via errors.Is.
var ErrUnauthorized = errors.New("unauthorized")

// Client is a small `net/http` wrapper for the server's auth endpoints.
// Keeps a base URL + optional token; safe for single-thread use.
type Client struct {
	baseURL *url.URL
	token   string
	http    *http.Client
}

// NewClient parses `serverURL` and returns a ready-to-use Client.
// Returns an error when `serverURL` is empty or malformed.
func NewClient(serverURL string) (*Client, error) {
	if strings.TrimSpace(serverURL) == "" {
		return nil, errors.New("server URL is required")
	}
	u, err := url.Parse(serverURL)
	if err != nil {
		return nil, fmt.Errorf("parse server URL: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, fmt.Errorf("server URL must use http or https, got %q", u.Scheme)
	}
	if u.Host == "" {
		return nil, errors.New("server URL must include a host")
	}
	return &Client{
		baseURL: u,
		http:    &http.Client{Timeout: 30 * time.Second},
	}, nil
}

// SetToken attaches an `X-Lumi-Token` header to authenticated requests.
func (c *Client) SetToken(token string) {
	c.token = token
}

// SetHTTPClient swaps the underlying http.Client. Useful for tests.
func (c *Client) SetHTTPClient(h *http.Client) {
	if h != nil {
		c.http = h
	}
}

// BaseURL returns the configured server URL.
func (c *Client) BaseURL() *url.URL {
	return c.baseURL
}

// Login posts to `/api/auth/login` and returns the session envelope.
// HTTP 401 surfaces as ErrUnauthorized; other 4xx/5xx as *APIError.
func (c *Client) Login(ctx context.Context, username, password string) (*SessionResponse, error) {
	var resp SessionResponse
	if err := c.do(ctx, "POST", "/api/auth/login", loginRequest{Username: username, Password: password}, false, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// CurrentUser validates the current token by calling `/api/users/me`.
// Returns the User on success, ErrUnauthorized when the token is dead.
func (c *Client) CurrentUser(ctx context.Context) (*User, error) {
	var u User
	if err := c.do(ctx, "GET", "/api/users/me", nil, true, &u); err != nil {
		return nil, err
	}
	return &u, nil
}

// Logout invalidates the current token server-side. Caller is still
// responsible for removing the local account entry.
func (c *Client) Logout(ctx context.Context) error {
	return c.do(ctx, "POST", "/api/auth/logout", nil, true, nil)
}

func (c *Client) do(ctx context.Context, method, path string, body any, requireToken bool, out any) error {
	u := *c.baseURL
	u.Path = singleJoinPath(u.Path, path)

	var reqBody io.Reader
	if body != nil {
		buf, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("encode request body: %w", err)
		}
		reqBody = bytes.NewReader(buf)
	}

	req, err := http.NewRequestWithContext(ctx, method, u.String(), reqBody)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	if reqBody != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if requireToken {
		if c.token == "" {
			return ErrUnauthorized
		}
		req.Header.Set("X-Lumi-Token", c.token)
	} else if c.token != "" {
		req.Header.Set("X-Lumi-Token", c.token)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("network: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized {
		// Drain the body so the connection can be reused, then return the
		// canonical sentinel.
		_, _ = io.Copy(io.Discard, resp.Body)
		return ErrUnauthorized
	}

	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var env errorEnvelope
		_ = json.Unmarshal(respBytes, &env)
		return &APIError{Status: resp.StatusCode, Code: env.Error, Detail: env.Detail}
	}

	if out == nil || len(respBytes) == 0 {
		return nil
	}
	if err := json.Unmarshal(respBytes, out); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	return nil
}

// singleJoinPath joins two URL paths with exactly one separator.
func singleJoinPath(base, suffix string) string {
	if base == "" {
		return suffix
	}
	if strings.HasSuffix(base, "/") && strings.HasPrefix(suffix, "/") {
		return base + suffix[1:]
	}
	if !strings.HasSuffix(base, "/") && !strings.HasPrefix(suffix, "/") {
		return base + "/" + suffix
	}
	return base + suffix
}
