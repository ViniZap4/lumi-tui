package feed

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/vinizap/lumi/tui-client/account"
	"github.com/vinizap/lumi/tui-client/books"
	"github.com/vinizap/lumi/tui-client/filesystem"
	"github.com/vinizap/lumi/tui-client/internal/fakeserver"
)

const vid = "8c5a1d9f-2b3c-4d5e-8f60-718293a4b5c6"

// fakeServer is an httptest stand-in for the wave-2 endpoints.
func fakeServer(t *testing.T, head *int64) *httptest.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/events", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok" {
			w.WriteHeader(401)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		fmt.Fprintf(w, "event: hello\ndata: {\"protocol\":1,\"vaults\":[{\"vault_id\":%q,\"seq\":%d,\"role\":\"Editor\",\"can_edit\":true}]}\n\n", vid, atomic.LoadInt64(head))
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	})
	mux.HandleFunc("/api/vaults/"+vid+"/changes", func(w http.ResponseWriter, r *http.Request) {
		after := r.URL.Query().Get("after")
		h := atomic.LoadInt64(head)
		page := account.ChangesPage{Head: h}
		if after == "0" {
			page.Notes = []account.ChangedNote{{RemoteNote: account.RemoteNote{ID: "n1", Path: "inbox/n1.md", Title: "N1"}, ChangeSeq: h}}
		}
		json.NewEncoder(w).Encode(page)
	})
	mux.HandleFunc("/api/vaults/"+vid+"/notes/n1/snapshot", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(account.NoteSnapshot{ID: "n1", Text: "hello from web"})
	})
	return httptest.NewServer(mux)
}

func waitFor(t *testing.T, m *Manager, cond func() bool) {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for !cond() {
		select {
		case <-m.Notify():
		case <-time.After(50 * time.Millisecond):
		case <-deadline:
			t.Fatal("condition not reached")
		}
	}
}

func TestManagerHelloCatchUpMirrorsAndBecomesUpToDate(t *testing.T) {
	var head int64 = 2
	srv := fakeServer(t, &head)
	defer srv.Close()
	root := t.TempDir()
	reg := &books.Registry{}
	reg.Add(books.Book{ID: "b1", Name: "Work", Kind: books.KindBound, ServerURL: srv.URL, Account: "alice", VaultID: vid, Path: root})
	reg.Add(books.Book{ID: "b2", Name: "Local", Kind: books.KindLocal, Path: t.TempDir()})
	st := &books.Store{Path: filepath.Join(t.TempDir(), "books.yaml"), Registry: reg}
	m := NewManager(st, []account.Account{{Server: srv.URL, Username: "alice", Token: "tok"}})
	m.SetCacheDir(t.TempDir())
	m.ListAll = func(context.Context, []account.Account) ([]books.Listing, []error) { return nil, nil }
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m.Start(ctx)

	waitFor(t, m, func() bool {
		v, _ := m.View("b1")
		return v.Status.State == books.StateUpToDate
	})
	v, _ := m.View("b1")
	if v.Cursor != 2 || v.Head != 2 || v.MirrorGen == 0 || v.LastSynced.IsZero() {
		t.Fatalf("view %+v", v)
	}
	n, err := filesystem.ReadNote(filepath.Join(root, "inbox", "n1.md"))
	if err != nil || n.Content != "hello from web" {
		t.Fatalf("mirror: %+v %v", n, err)
	}
	if lv, _ := m.View("b2"); lv.Status.State != books.StateLocal {
		t.Errorf("local book state %v", lv.Status.State)
	}
	// Cache persisted outside the notes folder.
	if _, err := os.Stat(filepath.Join(root, ".lumi")); err == nil {
		t.Error("sync cache must not be written into the notes folder")
	}
}

func TestManagerRevokedWhenAbsentFromHelloAndSignInOn401(t *testing.T) {
	var head int64 = 1
	srv := fakeServer(t, &head)
	defer srv.Close()
	reg := &books.Registry{}
	reg.Add(books.Book{ID: "gone", Name: "Gone", Kind: books.KindBound, ServerURL: srv.URL, Account: "alice", VaultID: "other-vault"})
	reg.Add(books.Book{ID: "bob", Name: "Bob's", Kind: books.KindBound, ServerURL: srv.URL, Account: "bob", VaultID: "x"})
	reg.Add(books.Book{ID: "noacct", Name: "No account", Kind: books.KindBound, ServerURL: "https://elsewhere", Account: "z", VaultID: "y"})
	st := &books.Store{Path: filepath.Join(t.TempDir(), "books.yaml"), Registry: reg}
	m := NewManager(st, []account.Account{
		{Server: srv.URL, Username: "alice", Token: "tok"},
		{Server: srv.URL, Username: "bob", Token: "bad"},
	})
	m.SetCacheDir(t.TempDir())
	m.ListAll = func(context.Context, []account.Account) ([]books.Listing, []error) { return nil, nil }
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m.Start(ctx)
	waitFor(t, m, func() bool {
		g, _ := m.View("gone")
		b, _ := m.View("bob")
		return g.Status.State == books.StateError && b.Status.State == books.StateSignIn
	})
	g, _ := m.View("gone")
	if !strings.Contains(g.Status.Reason, "removed") {
		t.Errorf("reason %q", g.Status.Reason)
	}
	if v, _ := m.View("noacct"); v.Status.State != books.StateSignIn {
		t.Errorf("book without account: %v", v.Status.State)
	}
}

func TestManagerIgnoresEventsFromAlternateAccount(t *testing.T) {
	reg := &books.Registry{}
	reg.Add(books.Book{ID: "b", Name: "B", Kind: books.KindBound, ServerURL: "https://home", Account: "u", VaultID: vid})
	m := NewManager(&books.Store{Registry: reg}, nil)
	m.feeds[acctKey("https://follower", "u")] = &feedInfo{}
	m.feeds[acctKey("https://home", "u")] = &feedInfo{}
	m.handle(account.Account{Server: "https://follower", Username: "u"}, Update{State: books.FeedOpen, Payload: &VaultRevoked{VaultID: vid, Reason: "removed"}})
	if m.bstate["b"] != nil && m.bstate["b"].revoked {
		t.Fatal("an alternate's event must not affect the bound book")
	}
}

// The feed-driven safe push: a local edit is merged 3-way with a
// concurrent remote edit, the pending set drains, and offline the pending
// count is shown instead of anything being queued.
func TestManagerLocalEditPushesThreeWayAndShowsPendingOffline(t *testing.T) {
	srv := fakeserver.New(vid, "work")
	defer srv.Close()
	srv.Events = func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		fmt.Fprintf(w, "event: hello\ndata: {\"protocol\":1,\"vaults\":[{\"vault_id\":%q,\"seq\":%d,\"role\":\"Editor\",\"can_edit\":true}]}\n\n", vid, srv.Head())
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}
	srv.AddNote("n1", "n1.md", "hello world")
	srv.AddNote("n2", "n2.md", "second")
	root := t.TempDir()
	reg := &books.Registry{}
	reg.Add(books.Book{ID: "b1", Name: "Work", Kind: books.KindBound, ServerURL: srv.URL, Account: "alice", VaultID: vid, Path: root})
	st := &books.Store{Path: filepath.Join(t.TempDir(), "books.yaml"), Registry: reg}
	m := NewManager(st, []account.Account{{Server: srv.URL, Username: "alice", Token: "tok"}})
	cacheDir := t.TempDir()
	m.SetCacheDir(cacheDir)
	m.ListAll = func(context.Context, []account.Account) ([]books.Listing, []error) { return nil, nil }
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m.Start(ctx)
	waitFor(t, m, func() bool {
		v, _ := m.View("b1")
		return v.Status.State == books.StateUpToDate && v.Cursor == srv.Head()
	})
	if _, ok := BasesFor(cacheDir, "b1").Load("n1"); !ok {
		t.Fatal("mirror did not record a base")
	}

	edit := func(rel, body string) {
		n, err := filesystem.ReadNote(filepath.Join(root, rel))
		if err != nil {
			t.Fatal(err)
		}
		n.Content = body
		if err := filesystem.WriteNote(n); err != nil {
			t.Fatal(err)
		}
	}
	srv.RemoteEdit("n1", "# hello world")
	edit("n1.md", "hello brave world")
	m.LocalEdit("b1")
	waitFor(t, m, func() bool {
		v, _ := m.View("b1")
		return srv.Text("n1") == "# hello brave world" && len(v.Pending) == 0 && v.Status.State == books.StateUpToDate
	})
	if n, _ := filesystem.ReadNote(filepath.Join(root, "n1.md")); n.Content != "# hello brave world" {
		t.Fatalf("local n1 = %q", n.Content)
	}

	// Offline: nothing is queued, the pending count shows.
	cancel()
	srv.Close()
	m2 := NewManager(st, []account.Account{{Server: srv.URL, Username: "alice", Token: "tok"}})
	m2.SetCacheDir(cacheDir)
	edit("n2.md", "second, edited offline")
	m2.LocalEdit("b1")
	v, _ := m2.View("b1")
	if len(v.Pending) != 1 || v.Pending[0] != "n2.md" {
		t.Fatalf("pending = %v", v.Pending)
	}
	if v.Status.State != books.StateOffline {
		t.Fatalf("state = %v", v.Status.State)
	}
}
