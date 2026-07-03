package account

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestListVaultsDecodesEnvelope(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/vaults" {
			t.Errorf("path = %q", r.URL.Path)
		}
		if r.Header.Get("X-Lumi-Token") != "tok" {
			t.Errorf("missing/wrong token: %q", r.Header.Get("X-Lumi-Token"))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"vaults": [
				{"id":"v1","slug":"work","name":"Work","created_by":"u1","created_at":"2026-05-25T10:00:00Z"},
				{"id":"v2","slug":"personal","name":"Personal","created_by":"u1","created_at":"2026-05-25T11:00:00Z"}
			]
		}`))
	}))
	defer srv.Close()

	c, _ := NewClient(srv.URL)
	c.SetToken("tok")
	got, err := c.ListVaults(context.Background())
	if err != nil {
		t.Fatalf("ListVaults: %v", err)
	}
	if len(got) != 2 || got[0].Slug != "work" || got[1].Slug != "personal" {
		t.Fatalf("got %+v", got)
	}
}

func TestListVaultsRequiresToken(t *testing.T) {
	c, _ := NewClient("http://example")
	_, err := c.ListVaults(context.Background())
	if err == nil {
		t.Fatal("expected ErrUnauthorized without token")
	}
	if err != ErrUnauthorized {
		t.Fatalf("err = %v, want ErrUnauthorized", err)
	}
}

func TestListNotesPagination(t *testing.T) {
	calls := 0
	var gotLimit, gotOffset string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path != "/api/vaults/v1/notes" {
			t.Errorf("path = %q", r.URL.Path)
		}
		gotLimit = r.URL.Query().Get("limit")
		gotOffset = r.URL.Query().Get("offset")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"notes":[{"id":"n1","vault_id":"v1","path":"n1.md","title":"N1","created_at":"","updated_at":""}],"limit":50,"offset":10}`))
	}))
	defer srv.Close()

	c, _ := NewClient(srv.URL)
	c.SetToken("tok")

	page, err := c.ListNotes(context.Background(), "v1", 50, 10)
	if err != nil {
		t.Fatalf("ListNotes: %v", err)
	}
	if len(page) != 1 || page[0].ID != "n1" {
		t.Fatalf("got %+v", page)
	}
	if calls != 1 {
		t.Errorf("calls = %d, want 1", calls)
	}
	if gotLimit != "50" || gotOffset != "10" {
		t.Errorf("query params = limit=%q offset=%q, want 50/10", gotLimit, gotOffset)
	}
}

// TestListNotesDefaultOffsetOmitted: zero-offset is omitted from the
// query string. The current implementation also omits zero-limit (so
// the server picks its default). Both are deliberate — keeps the URL
// terse for the common case.
func TestListNotesDefaultOffsetOmitted(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.RawQuery != "" {
			t.Errorf("expected no query string, got %q", r.URL.RawQuery)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"notes":[],"limit":100,"offset":0}`))
	}))
	defer srv.Close()
	c, _ := NewClient(srv.URL)
	c.SetToken("tok")
	if _, err := c.ListNotes(context.Background(), "v1", 0, 0); err != nil {
		t.Fatalf("ListNotes: %v", err)
	}
}

func TestListAllNotesWalksUntilEmpty(t *testing.T) {
	// First page: 200 entries (fake — actually 1 here, but small page
	// size triggers the stop condition). Use page size of 200 internal;
	// reply with len(page) < pageSize on first call to stop.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"notes":[
			{"id":"n1","vault_id":"v1","path":"n1.md","title":"N1","created_at":"","updated_at":""},
			{"id":"n2","vault_id":"v1","path":"sub/n2.md","title":"N2","created_at":"","updated_at":""}
		],"limit":200,"offset":0}`))
	}))
	defer srv.Close()

	c, _ := NewClient(srv.URL)
	c.SetToken("tok")
	all, err := c.ListAllNotes(context.Background(), "v1")
	if err != nil {
		t.Fatalf("ListAllNotes: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("got %d notes, want 2", len(all))
	}
}

func TestGetNoteContent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/vaults/v1/notes/n1/content" {
			t.Errorf("path = %q", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"n1","path":"n1.md","body":"# Hello\n"}`))
	}))
	defer srv.Close()

	c, _ := NewClient(srv.URL)
	c.SetToken("tok")
	got, err := c.GetNoteContent(context.Background(), "v1", "n1")
	if err != nil {
		t.Fatalf("GetNoteContent: %v", err)
	}
	if got.ID != "n1" || got.Path != "n1.md" || !strings.Contains(got.Body, "Hello") {
		t.Fatalf("got %+v", got)
	}
}

func TestGetNoteContentRejectsMissingIDs(t *testing.T) {
	c, _ := NewClient("http://example")
	c.SetToken("tok")
	if _, err := c.GetNoteContent(context.Background(), "", "n1"); err == nil {
		t.Errorf("expected error for empty vaultID")
	}
	if _, err := c.GetNoteContent(context.Background(), "v1", ""); err == nil {
		t.Errorf("expected error for empty noteID")
	}
}

func TestListMembersDecodesEnvelope(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/vaults/v1/members" {
			t.Errorf("path = %q", r.URL.Path)
		}
		if r.Method != http.MethodGet {
			t.Errorf("method = %q, want GET", r.Method)
		}
		if r.Header.Get("X-Lumi-Token") != "tok" {
			t.Errorf("missing/wrong token: %q", r.Header.Get("X-Lumi-Token"))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"members": [
				{"vault_id":"v1","user_id":"u1","username":"alice","display_name":"Alice","role_id":"r1","role_name":"Admin"},
				{"vault_id":"v1","user_id":"u2","username":"bob","display_name":"Bob","role_id":"r2","role_name":"Editor"}
			]
		}`))
	}))
	defer srv.Close()

	c, _ := NewClient(srv.URL)
	c.SetToken("tok")
	got, err := c.ListMembers(context.Background(), "v1")
	if err != nil {
		t.Fatalf("ListMembers: %v", err)
	}
	if len(got) != 2 || got[0].Username != "alice" || got[0].UserID != "u1" ||
		got[1].RoleName != "Editor" {
		t.Fatalf("got %+v", got)
	}
}

func TestListMembersRejectsEmptyVaultID(t *testing.T) {
	c, _ := NewClient("http://example")
	c.SetToken("tok")
	if _, err := c.ListMembers(context.Background(), ""); err == nil {
		t.Errorf("expected error for empty vaultID")
	}
}

func TestTransferOwnership(t *testing.T) {
	var gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/vaults/v1/transfer-ownership" {
			t.Errorf("path = %q", r.URL.Path)
		}
		if r.Method != http.MethodPost {
			t.Errorf("method = %q, want POST", r.Method)
		}
		if r.Header.Get("X-Lumi-Token") != "tok" {
			t.Errorf("missing/wrong token: %q", r.Header.Get("X-Lumi-Token"))
		}
		raw, _ := io.ReadAll(r.Body)
		gotBody = string(raw)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"v1","slug":"work","name":"Work","created_by":"u1","created_at":"2026-05-25T10:00:00Z","owner_user_id":"u2"}`))
	}))
	defer srv.Close()

	c, _ := NewClient(srv.URL)
	c.SetToken("tok")
	got, err := c.TransferOwnership(context.Background(), "v1", "u2")
	if err != nil {
		t.Fatalf("TransferOwnership: %v", err)
	}
	if got.OwnerUserID != "u2" || got.Slug != "work" {
		t.Fatalf("got %+v", got)
	}
	var sent map[string]string
	if err := json.Unmarshal([]byte(gotBody), &sent); err != nil {
		t.Fatalf("decode request body: %v", err)
	}
	if sent["user_id"] != "u2" {
		t.Errorf("body user_id = %q, want u2", sent["user_id"])
	}
}

func TestTransferOwnershipForbidden(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"error":"forbidden","detail":"only the owner can transfer ownership"}`))
	}))
	defer srv.Close()

	c, _ := NewClient(srv.URL)
	c.SetToken("tok")
	_, err := c.TransferOwnership(context.Background(), "v1", "u2")
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("err = %v, want *APIError", err)
	}
	if apiErr.Status != http.StatusForbidden || apiErr.Code != "forbidden" {
		t.Fatalf("got %+v", apiErr)
	}
}

func TestTransferOwnershipRejectsMissingArgs(t *testing.T) {
	c, _ := NewClient("http://example")
	c.SetToken("tok")
	if _, err := c.TransferOwnership(context.Background(), "", "u1"); err == nil {
		t.Errorf("expected error for empty vaultID")
	}
	if _, err := c.TransferOwnership(context.Background(), "v1", ""); err == nil {
		t.Errorf("expected error for empty userID")
	}
}

func TestCopyVault(t *testing.T) {
	var gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/vaults/v1/copies" {
			t.Errorf("path = %q", r.URL.Path)
		}
		if r.Method != http.MethodPost {
			t.Errorf("method = %q, want POST", r.Method)
		}
		if r.Header.Get("X-Lumi-Token") != "tok" {
			t.Errorf("missing/wrong token: %q", r.Header.Get("X-Lumi-Token"))
		}
		raw, _ := io.ReadAll(r.Body)
		gotBody = string(raw)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":"v9","slug":"work-copy","name":"Work","created_by":"u1","created_at":"2026-07-02T10:00:00Z","owner_user_id":"u2","copied_from":{"vault_id":"v1"}}`))
	}))
	defer srv.Close()

	c, _ := NewClient(srv.URL)
	c.SetToken("tok")
	got, err := c.CopyVault(context.Background(), "v1", "bob")
	if err != nil {
		t.Fatalf("CopyVault: %v", err)
	}
	if got.ID != "v9" || got.Slug != "work-copy" || got.OwnerUserID != "u2" {
		t.Fatalf("got %+v", got)
	}
	if len(got.CopiedFrom) == 0 || !strings.Contains(string(got.CopiedFrom), "v1") {
		t.Errorf("copied_from not captured: %q", string(got.CopiedFrom))
	}
	var sent map[string]string
	if err := json.Unmarshal([]byte(gotBody), &sent); err != nil {
		t.Fatalf("decode request body: %v", err)
	}
	if sent["recipient_username"] != "bob" {
		t.Errorf("body recipient_username = %q, want bob", sent["recipient_username"])
	}
}

func TestCopyVaultRecipientNotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"recipient_not_found"}`))
	}))
	defer srv.Close()

	c, _ := NewClient(srv.URL)
	c.SetToken("tok")
	_, err := c.CopyVault(context.Background(), "v1", "ghost")
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("err = %v, want *APIError", err)
	}
	if apiErr.Status != http.StatusBadRequest || apiErr.Code != "recipient_not_found" {
		t.Fatalf("got %+v", apiErr)
	}
}

func TestCopyVaultRejectsMissingArgs(t *testing.T) {
	c, _ := NewClient("http://example")
	c.SetToken("tok")
	if _, err := c.CopyVault(context.Background(), "", "bob"); err == nil {
		t.Errorf("expected error for empty vaultID")
	}
	if _, err := c.CopyVault(context.Background(), "v1", ""); err == nil {
		t.Errorf("expected error for empty recipient")
	}
}

func TestListNotesRejectsEmptyVaultID(t *testing.T) {
	c, _ := NewClient("http://example")
	c.SetToken("tok")
	if _, err := c.ListNotes(context.Background(), "", 10, 0); err == nil {
		t.Errorf("expected error for empty vaultID")
	}
}
