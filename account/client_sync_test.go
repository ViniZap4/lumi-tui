package account

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
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

func TestGetNoteSnapshotDecodesEpoch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"n","path":"n.md","crdt_epoch":"e1","text":"t","vector_clock":"AA==","state":"AAA="}`))
	}))
	defer srv.Close()
	c, _ := NewClient(srv.URL)
	c.SetToken("tok")
	snap, err := c.GetNoteSnapshot(context.Background(), "v1", "n")
	if err != nil || snap.CRDTEpoch != "e1" || snap.State != "AAA=" {
		t.Fatalf("snap = %+v, err = %v", snap, err)
	}
}

// §12.3: a 409 conflict on a read means the lineage changed mid-read;
// the client retries.
func TestGetNoteSnapshotRetriesEpochUnstable(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		if calls == 1 {
			w.WriteHeader(http.StatusConflict)
			_, _ = w.Write([]byte(`{"error":"conflict","detail":"crdt_epoch changed while reading; retry"}`))
			return
		}
		_, _ = w.Write([]byte(`{"id":"n","crdt_epoch":"e2","text":"t","vector_clock":"AA=="}`))
	}))
	defer srv.Close()
	c, _ := NewClient(srv.URL)
	c.SetToken("tok")
	snap, err := c.GetNoteSnapshot(context.Background(), "v1", "n")
	if err != nil || snap.CRDTEpoch != "e2" || calls != 2 {
		t.Fatalf("snap = %+v, err = %v, calls = %d", snap, err, calls)
	}
}

func TestGetStateVector(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/vaults/v1/notes/n/state-vector" || r.Method != http.MethodGet {
			t.Errorf("%s %s", r.Method, r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"n","crdt_epoch":"e1","state_vector":"AQEC"}`))
	}))
	defer srv.Close()
	c, _ := NewClient(srv.URL)
	c.SetToken("tok")
	sv, err := c.GetStateVector(context.Background(), "v1", "n")
	if err != nil || sv.CRDTEpoch != "e1" || sv.StateVector != "AQEC" {
		t.Fatalf("sv = %+v, err = %v", sv, err)
	}
}

// Older servers: Fiber's plain-text 404 for an unknown route means
// "unsupported"; a JSON not_found means the note is gone.
func TestGetStateVectorUnsupportedVsNotFound(t *testing.T) {
	plain := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte("Cannot GET /api/vaults/v1/notes/n/state-vector"))
	}))
	defer plain.Close()
	c, _ := NewClient(plain.URL)
	c.SetToken("tok")
	if _, err := c.GetStateVector(context.Background(), "v1", "n"); !errors.Is(err, ErrStateVectorUnsupported) {
		t.Fatalf("plain 404: err = %v, want ErrStateVectorUnsupported", err)
	}
	gone := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":"not_found"}`))
	}))
	defer gone.Close()
	c2, _ := NewClient(gone.URL)
	c2.SetToken("tok")
	_, err := c2.GetStateVector(context.Background(), "v1", "n")
	var apiErr *APIError
	if errors.Is(err, ErrStateVectorUnsupported) || !errors.As(err, &apiErr) || apiErr.Code != "not_found" {
		t.Fatalf("json 404: err = %v", err)
	}
}
