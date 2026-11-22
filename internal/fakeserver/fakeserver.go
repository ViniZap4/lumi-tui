// Package fakeserver is an in-memory stand-in for the lumi server's note
// sync endpoints, used by tests (httptest). It implements the semantics
// the TUI's safe push depends on (CRDT-CLIENT-PROTOCOL §7.2, §10 S1/S9):
//
//   - GET  /api/vaults/:v/notes/:id/snapshot → {id, path, text, vector_clock, state}
//   - POST /api/vaults/:v/notes/:id/diff {text, base_state, origin}
//     → 3-way merge base→text into the current text, {id, path, text, vector_clock}
//     (no `state`, like the real server);
//     no base_state → 409 base_required; unknown base_state → 409 base_mismatch
//   - GET  /api/vaults/:v/changes?after=N, GET /api/vaults/:v/notes,
//     GET /api/vaults, GET /api/vaults/:v/notes/:id/content
//
// The "CRDT" is a version history: every text the server ever held gets
// an opaque state token. The merge applies the client's base→edited hunk
// and the server's base→current hunk (prefix/suffix each); overlapping
// hunks are not supported (tests avoid them) and answer 500.
package fakeserver

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
)

// Note is one server note.
type Note struct {
	ID, Path, Title string
	Text            string
	Seq             int64
	version         int
}

// DiffRequest records one POST /diff body.
type DiffRequest struct {
	NoteID    string
	Text      string
	BaseState string
	BaseClock string
	Origin    string
}

// Server is the fake. Lock-protected; safe for -race.
type Server struct {
	*httptest.Server
	VaultID, Slug string

	mu     sync.Mutex
	notes  map[string]*Note
	order  []string
	states map[string]string // state token -> text
	head   int64
	diffs  []DiffRequest
	// OnDiff runs (unlocked) before a /diff is processed.
	OnDiff func(DiffRequest)
	// Fail409 forces the next /diff calls to answer 409 with these codes.
	Fail409 []string
	// OmitState makes GET /snapshot leave out `state` (pre-S9 server).
	OmitState bool
	// Events, when set, serves GET /api/events (the SSE feed).
	Events http.HandlerFunc
}

// Head returns the vault's current change seq.
func (s *Server) Head() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.head
}

// New starts a fake server for one vault.
func New(vaultID, slug string) *Server {
	s := &Server{VaultID: vaultID, Slug: slug, notes: map[string]*Note{}, states: map[string]string{}}
	s.Server = httptest.NewServer(http.HandlerFunc(s.serve))
	return s
}

func token(id string, version int) string {
	return base64.StdEncoding.EncodeToString([]byte(fmt.Sprintf("%s@%d", id, version)))
}

func (s *Server) stateLocked(n *Note) string {
	t := token(n.ID, n.version)
	s.states[t] = n.Text
	return t
}

func clock(n *Note) string {
	return base64.StdEncoding.EncodeToString([]byte("sv" + strconv.Itoa(n.version)))
}

// AddNote creates a note.
func (s *Server) AddNote(id, path, text string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.head++
	n := &Note{ID: id, Path: path, Title: strings.ToUpper(id[:1]) + id[1:], Text: text, Seq: s.head}
	s.notes[id] = n
	s.order = append(s.order, id)
	s.stateLocked(n)
}

// RemoteEdit simulates another client (web) editing a note.
func (s *Server) RemoteEdit(id, text string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := s.notes[id]
	n.Text = text
	n.version++
	s.head++
	n.Seq = s.head
	s.stateLocked(n)
}

// ForgetHistory makes every state token issued so far unknown (a server
// reset or a switched replica): pushes on old bases get base_mismatch.
func (s *Server) ForgetHistory() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.states = map[string]string{}
	for _, n := range s.notes {
		n.version += 1000
		s.stateLocked(n)
	}
}

// Text returns a note's current server text.
func (s *Server) Text(id string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.notes[id].Text
}

// Diffs returns the /diff requests received so far.
func (s *Server) Diffs() []DiffRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]DiffRequest(nil), s.diffs...)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func (s *Server) serve(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/api/events" && s.Events != nil {
		s.Events(w, r)
		return
	}
	if r.Header.Get("X-Lumi-Token") == "" {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	switch {
	case len(parts) == 2 && parts[0] == "api" && parts[1] == "vaults":
		writeJSON(w, 200, map[string]any{"vaults": []map[string]string{{"id": s.VaultID, "slug": s.Slug, "name": s.Slug}}})
		return
	case len(parts) < 4 || parts[0] != "api" || parts[1] != "vaults" || parts[2] != s.VaultID:
		writeJSON(w, 404, map[string]string{"error": "not_found"})
		return
	}
	switch {
	case len(parts) == 4 && parts[3] == "changes":
		s.changes(w, r)
	case len(parts) == 4 && parts[3] == "notes":
		s.list(w, r)
	case len(parts) == 6 && parts[3] == "notes":
		s.note(w, r, parts[4], parts[5])
	default:
		writeJSON(w, 404, map[string]string{"error": "not_found"})
	}
}

func (s *Server) row(n *Note) map[string]any {
	return map[string]any{"id": n.ID, "vault_id": s.VaultID, "path": n.Path, "title": n.Title,
		"created_at": "2026-11-01T10:00:00Z", "updated_at": "2026-11-01T10:00:00Z", "change_seq": n.Seq}
}

func (s *Server) changes(w http.ResponseWriter, r *http.Request) {
	after, _ := strconv.ParseInt(r.URL.Query().Get("after"), 10, 64)
	s.mu.Lock()
	defer s.mu.Unlock()
	notes := []map[string]any{}
	for _, id := range s.order {
		if n := s.notes[id]; n.Seq > after {
			notes = append(notes, s.row(n))
		}
	}
	writeJSON(w, 200, map[string]any{"notes": notes, "deleted": []any{}, "head": s.head, "more": false})
}

func (s *Server) list(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []map[string]any{}
	if r.URL.Query().Get("offset") == "" || r.URL.Query().Get("offset") == "0" {
		for _, id := range s.order {
			out = append(out, s.row(s.notes[id]))
		}
	}
	writeJSON(w, 200, map[string]any{"notes": out})
}

func (s *Server) note(w http.ResponseWriter, r *http.Request, id, op string) {
	s.mu.Lock()
	n, ok := s.notes[id]
	s.mu.Unlock()
	if !ok {
		writeJSON(w, 404, map[string]string{"error": "not_found"})
		return
	}
	switch {
	case op == "snapshot" && r.Method == http.MethodGet:
		s.mu.Lock()
		body := map[string]any{"id": n.ID, "path": n.Path, "text": n.Text, "vector_clock": clock(n)}
		if !s.OmitState {
			body["state"] = s.stateLocked(n)
		}
		s.mu.Unlock()
		writeJSON(w, 200, body)
	case op == "content" && r.Method == http.MethodGet:
		s.mu.Lock()
		body := fmt.Sprintf("---\nid: %s\ntitle: %s\n---\n\n%s", n.ID, n.Title, n.Text)
		s.mu.Unlock()
		writeJSON(w, 200, map[string]any{"id": n.ID, "path": n.Path, "body": body})
	case op == "diff" && r.Method == http.MethodPost:
		s.diff(w, r, n)
	default:
		writeJSON(w, 405, map[string]string{"error": "method_not_allowed"})
	}
}

func (s *Server) diff(w http.ResponseWriter, r *http.Request, n *Note) {
	var req struct {
		Text      string `json:"text"`
		BaseState string `json:"base_state"`
		BaseClock string `json:"base_clock"`
		Origin    string `json:"origin"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, 400, map[string]string{"error": "invalid_body"})
		return
	}
	dr := DiffRequest{NoteID: n.ID, Text: req.Text, BaseState: req.BaseState, BaseClock: req.BaseClock, Origin: req.Origin}
	s.mu.Lock()
	s.diffs = append(s.diffs, dr)
	hook := s.OnDiff
	s.mu.Unlock()
	if hook != nil {
		hook(dr)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.Fail409) > 0 {
		code := s.Fail409[0]
		s.Fail409 = s.Fail409[1:]
		writeJSON(w, 409, map[string]string{"error": code})
		return
	}
	switch req.Origin {
	case "", "tui", "tui-diff", "web", "web-diff", "apple", "apple-diff":
	default:
		writeJSON(w, 400, map[string]string{"error": "invalid_origin"})
		return
	}
	if req.BaseState == "" {
		writeJSON(w, 409, map[string]string{"error": "base_required"})
		return
	}
	baseText, ok := s.states[req.BaseState]
	if !ok {
		writeJSON(w, 409, map[string]string{"error": "base_mismatch"})
		return
	}
	merged, err := Merge3(baseText, req.Text, n.Text)
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": "internal", "detail": err.Error()})
		return
	}
	if merged != n.Text {
		n.Text = merged
		n.version++
		s.head++
		n.Seq = s.head
		s.stateLocked(n)
	}
	writeJSON(w, 200, map[string]any{"id": n.ID, "path": n.Path, "text": n.Text, "vector_clock": clock(n)})
}

type hunk struct {
	start, end int // in base
	repl       string
}

func diffHunk(base, other string) (hunk, bool) {
	if base == other {
		return hunk{}, false
	}
	p := 0
	for p < len(base) && p < len(other) && base[p] == other[p] {
		p++
	}
	sfx := 0
	for sfx < len(base)-p && sfx < len(other)-p && base[len(base)-1-sfx] == other[len(other)-1-sfx] {
		sfx++
	}
	return hunk{start: p, end: len(base) - sfx, repl: other[p : len(other)-sfx]}, true
}

// Merge3 merges base→local into base→remote (one prefix/suffix hunk per
// side). Concurrent edits in different places both survive.
func Merge3(base, local, remote string) (string, error) {
	lh, lok := diffHunk(base, local)
	rh, rok := diffHunk(base, remote)
	switch {
	case !lok:
		return remote, nil
	case !rok:
		return local, nil
	}
	first, second := lh, rh
	if rh.start < lh.start || (rh.start == lh.start && rh.end <= lh.start) {
		first, second = rh, lh
	}
	if first.end > second.start {
		return "", fmt.Errorf("overlapping hunks")
	}
	return base[:first.start] + first.repl + base[first.end:second.start] + second.repl + base[second.end:], nil
}
