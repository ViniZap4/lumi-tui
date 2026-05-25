package account

import (
	"context"
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

func TestListNotesRejectsEmptyVaultID(t *testing.T) {
	c, _ := NewClient("http://example")
	c.SetToken("tok")
	if _, err := c.ListNotes(context.Background(), "", 10, 0); err == nil {
		t.Errorf("expected error for empty vaultID")
	}
}
