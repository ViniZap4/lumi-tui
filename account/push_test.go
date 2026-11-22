package account

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestPushEditSendsTextBaseStateOrigin(t *testing.T) {
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/vaults/v1/notes/n1/diff" {
			t.Errorf("%s %s", r.Method, r.URL.Path)
		}
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &got)
		_, _ = w.Write([]byte(`{"id":"n1","path":"n1.md","text":"merged","vector_clock":"c2","state":"TUVSR0VE"}`))
	}))
	defer srv.Close()
	c, _ := NewClient(srv.URL)
	c.SetToken("tok")
	snap, err := c.PushEdit(context.Background(), "v1", "n1", EditBase{Text: "b", VectorClock: "c1", State: "U1RBVEU="}, "edited")
	if err != nil {
		t.Fatal(err)
	}
	if snap.Text != "merged" || snap.VectorClock != "c2" || snap.State != "TUVSR0VE" {
		t.Fatalf("snap = %+v", snap)
	}
	if got["text"] != "edited" || got["base_state"] != "U1RBVEU=" || got["origin"] != "tui" {
		t.Fatalf("body = %v", got)
	}
	if _, ok := got["base_clock"]; ok {
		t.Fatalf("base_clock must not be sent: %v", got)
	}
}

func TestPushEditWithoutStateNeverHitsTheWire(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected request %s", r.URL.Path)
	}))
	defer srv.Close()
	c, _ := NewClient(srv.URL)
	c.SetToken("tok")
	_, err := c.PushEdit(context.Background(), "v1", "n1", EditBase{Text: "b", VectorClock: "c"}, "x")
	if !errors.Is(err, ErrBaseRequired) || !IsBaseConflict(err) {
		t.Fatalf("err = %v", err)
	}
}

func TestPushEditMaps409s(t *testing.T) {
	for _, tc := range []struct {
		code string
		want error
	}{{"base_required", ErrBaseRequired}, {"base_mismatch", ErrBaseMismatch}, {"base_too_old", ErrBaseTooOld}} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusConflict)
			_, _ = w.Write([]byte(`{"error":"` + tc.code + `","detail":"d"}`))
		}))
		c, _ := NewClient(srv.URL)
		c.SetToken("tok")
		_, err := c.PushEdit(context.Background(), "v1", "n1", EditBase{State: "AA=="}, "x")
		srv.Close()
		if !errors.Is(err, tc.want) || !IsBaseConflict(err) {
			t.Fatalf("%s: err = %v", tc.code, err)
		}
		var apiErr *APIError
		if !errors.As(err, &apiErr) || apiErr.Status != http.StatusConflict {
			t.Fatalf("%s: APIError not reachable: %v", tc.code, err)
		}
	}
	// Any other 409 stays a plain APIError.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusConflict)
		_, _ = w.Write([]byte(`{"error":"conflict"}`))
	}))
	defer srv.Close()
	c, _ := NewClient(srv.URL)
	c.SetToken("tok")
	_, err := c.PushEdit(context.Background(), "v1", "n1", EditBase{State: "AA=="}, "x")
	if IsBaseConflict(err) || err == nil {
		t.Fatalf("err = %v", err)
	}
}

func TestGetNoteSnapshotDecodesState(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"id":"n","path":"n.md","text":"t","vector_clock":"c","state":"U1Q="}`))
	}))
	defer srv.Close()
	c, _ := NewClient(srv.URL)
	c.SetToken("tok")
	snap, err := c.GetNoteSnapshot(context.Background(), "v", "n")
	if err != nil || snap.State != "U1Q=" {
		t.Fatalf("snap = %+v err = %v", snap, err)
	}
}
