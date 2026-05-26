package account

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestGetNoteSnapshotDecodesEnvelope(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/vaults/v1/notes/hello/snapshot" {
			t.Errorf("path = %q", r.URL.Path)
		}
		if r.Header.Get("X-Lumi-Token") != "tok" {
			t.Errorf("missing token: %q", r.Header.Get("X-Lumi-Token"))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"hello","path":"hello.md","text":"body text","vector_clock":"YWJjZA=="}`))
	}))
	defer srv.Close()

	c, _ := NewClient(srv.URL)
	c.SetToken("tok")
	snap, err := c.GetNoteSnapshot(context.Background(), "v1", "hello")
	if err != nil {
		t.Fatalf("GetNoteSnapshot: %v", err)
	}
	if snap.Text != "body text" || snap.VectorClock != "YWJjZA==" || snap.Path != "hello.md" {
		t.Fatalf("snapshot = %+v", snap)
	}
}

func TestGetNoteSnapshotRequiresIDs(t *testing.T) {
	c, _ := NewClient("http://example")
	c.SetToken("tok")
	if _, err := c.GetNoteSnapshot(context.Background(), "", "x"); err == nil {
		t.Errorf("empty vaultID should error")
	}
	if _, err := c.GetNoteSnapshot(context.Background(), "v", ""); err == nil {
		t.Errorf("empty noteID should error")
	}
}

func TestApplyNoteDiffSendsBaseClockTextOrigin(t *testing.T) {
	var received applyDiffRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/vaults/v1/notes/hello/diff" {
			t.Errorf("path = %q", r.URL.Path)
		}
		if r.Method != http.MethodPost {
			t.Errorf("method = %q", r.Method)
		}
		raw, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(raw, &received); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"hello","path":"hello.md","text":"merged","vector_clock":"eHl6"}`))
	}))
	defer srv.Close()

	c, _ := NewClient(srv.URL)
	c.SetToken("tok")
	snap, err := c.ApplyNoteDiff(context.Background(), "v1", "hello", "YWJjZA==", "new body", "")
	if err != nil {
		t.Fatalf("ApplyNoteDiff: %v", err)
	}
	if snap.Text != "merged" || snap.VectorClock != "eHl6" {
		t.Fatalf("snapshot = %+v", snap)
	}
	if received.BaseClock != "YWJjZA==" {
		t.Errorf("base_clock = %q, want YWJjZA==", received.BaseClock)
	}
	if received.Text != "new body" {
		t.Errorf("text = %q", received.Text)
	}
	if received.Origin != "tui-diff" {
		t.Errorf("origin = %q, want default tui-diff", received.Origin)
	}
}

// Server-side `omitempty` semantics: empty base_clock must NOT appear in
// the wire payload (mirrors the Apple client). Lock it here so a future
// refactor that switches to a manually-encoded body keeps the same
// shape.
func TestApplyNoteDiffOmitsEmptyBaseClock(t *testing.T) {
	var rawBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		rawBody = string(raw)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"x","path":"x.md","text":"","vector_clock":"AAAA"}`))
	}))
	defer srv.Close()

	c, _ := NewClient(srv.URL)
	c.SetToken("tok")
	if _, err := c.ApplyNoteDiff(context.Background(), "v1", "x", "", "body", "tui-diff"); err != nil {
		t.Fatalf("ApplyNoteDiff: %v", err)
	}
	if rawBody == "" {
		t.Fatal("empty body — handler didn't read it")
	}
	if strings.Contains(rawBody, "base_clock") {
		t.Errorf("base_clock should be omitted when empty, got %q", rawBody)
	}
}

func TestApplyNoteDiffConflictSurfacesAPIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusConflict)
		_, _ = w.Write([]byte(`{"error":"conflict","detail":"base_clock is stale"}`))
	}))
	defer srv.Close()

	c, _ := NewClient(srv.URL)
	c.SetToken("tok")
	_, err := c.ApplyNoteDiff(context.Background(), "v1", "x", "stale", "z", "")
	if err == nil {
		t.Fatal("expected error")
	}
	apiErr, ok := err.(*APIError)
	if !ok {
		t.Fatalf("err = %T, want *APIError", err)
	}
	if apiErr.Status != http.StatusConflict || apiErr.Code != "conflict" {
		t.Errorf("got %+v", apiErr)
	}
}
