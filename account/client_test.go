package account

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestNewClientRejectsBlankURL(t *testing.T) {
	if _, err := NewClient(""); err == nil {
		t.Errorf("expected error for empty URL")
	}
}

func TestNewClientRejectsNonHTTPScheme(t *testing.T) {
	if _, err := NewClient("ftp://example.com"); err == nil {
		t.Errorf("expected error for ftp scheme")
	}
}

func TestNewClientRequiresHost(t *testing.T) {
	if _, err := NewClient("http://"); err == nil {
		t.Errorf("expected error for missing host")
	}
}

func TestLoginSuccessDecodesEnvelope(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/auth/login" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if r.Method != "POST" {
			t.Errorf("unexpected method: %s", r.Method)
		}
		body, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(body), `"username":"alice"`) {
			t.Errorf("body missing username: %s", body)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{
			"token": "tok-abc",
			"expires_at": "2026-12-31T23:59:59Z",
			"user": {"id": "u1", "username": "alice", "display_name": "Alice"}
		}`))
	}))
	defer srv.Close()

	c, err := NewClient(srv.URL)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	resp, err := c.Login(context.Background(), "alice", "hunter2")
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	if resp.Token != "tok-abc" {
		t.Errorf("token: got %q", resp.Token)
	}
	if resp.User.Username != "alice" {
		t.Errorf("username: got %q", resp.User.Username)
	}
	if resp.User.DisplayName != "Alice" {
		t.Errorf("display_name: got %q", resp.User.DisplayName)
	}
	if resp.ExpiresAt.IsZero() {
		t.Errorf("expires_at should be parsed")
	}
}

func TestLoginUnauthorizedSurfacesSentinel(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(401)
		_, _ = w.Write([]byte(`{"error":"invalid_credentials","detail":"bad username or password"}`))
	}))
	defer srv.Close()

	c, _ := NewClient(srv.URL)
	_, err := c.Login(context.Background(), "alice", "wrong")
	if !errors.Is(err, ErrUnauthorized) {
		t.Errorf("expected ErrUnauthorized, got %v", err)
	}
}

func TestLoginValidationFailureSurfacesAPIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(400)
		_, _ = w.Write([]byte(`{"error":"validation_failed","detail":"username required"}`))
	}))
	defer srv.Close()

	c, _ := NewClient(srv.URL)
	_, err := c.Login(context.Background(), "", "x")
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected *APIError, got %T (%v)", err, err)
	}
	if apiErr.Status != 400 || apiErr.Code != "validation_failed" {
		t.Errorf("unexpected APIError: %+v", apiErr)
	}
}

func TestCurrentUserRequiresToken(t *testing.T) {
	c, _ := NewClient("https://lumi.test")
	_, err := c.CurrentUser(context.Background())
	if !errors.Is(err, ErrUnauthorized) {
		t.Errorf("expected ErrUnauthorized when no token set, got %v", err)
	}
}

func TestCurrentUserSendsTokenHeader(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Lumi-Token") != "tok-123" {
			t.Errorf("expected X-Lumi-Token header, got %q", r.Header.Get("X-Lumi-Token"))
		}
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{"id":"u1","username":"alice","display_name":"Alice"}`))
	}))
	defer srv.Close()

	c, _ := NewClient(srv.URL)
	c.SetToken("tok-123")
	u, err := c.CurrentUser(context.Background())
	if err != nil {
		t.Fatalf("CurrentUser: %v", err)
	}
	if u.Username != "alice" {
		t.Errorf("username: got %q", u.Username)
	}
}
