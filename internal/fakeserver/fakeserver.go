// Package fakeserver is an in-memory stand-in for the lumi server's note
// sync endpoints, used by tests (httptest). It implements the semantics
// the TUI's safe push depends on (CRDT-CLIENT-PROTOCOL §7.2, §10 S1/S9):
//
//   - GET  /api/vaults/:v/notes/:id/snapshot → {id, path, text, vector_clock, state}
//   - POST /api/vaults/:v/notes/:id/diff {text, base_state, origin}
//     → 3-way merge base→text into the current text, {id, path, text, vector_clock, state}
//     (DiffOmitState drops `state`, like older servers);
//     no base_state → 409 base_required; unknown base_state → 409 base_mismatch;
//     expired base_state (ExpireHistory) → 409 base_too_old
//   - GET  /api/vaults/:v/notes/:id/state-vector → {id, crdt_epoch, state_vector}
//     (NoStateVector answers a plain-text 404, like servers before 0008)
//   - every note DTO carries `crdt_epoch` (NoEpochs leaves it out);
//     Recreate(id, text) starts a new CRDT history under the same id
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
	"sort"
	"strconv"
	"strings"
	"sync"
)

// Note is one server note.
type Note struct {
	ID, Path, Title string
	Text            string
	Seq             int64
	Epoch           string
	version         int
	client          uint64 // the history's Yjs client id
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
	// expired holds tokens the server can no longer reconstruct.
	expired   map[string]bool
	snapshots int
	head      int64
	diffs     []DiffRequest
	// OnDiff runs (unlocked) before a /diff is processed.
	OnDiff func(DiffRequest)
	// Fail409 forces the next /diff calls to answer 409 with these codes.
	Fail409 []string
	// OmitState makes GET /snapshot leave out `state` (pre-S9 server).
	OmitState bool
	// DiffOmitState makes POST /diff leave out `state` (servers before
	// the /diff reply carried the merged doc).
	DiffOmitState bool
	// Events, when set, serves GET /api/events (the SSE feed).
	Events http.HandlerFunc
	// NoEpochs omits `crdt_epoch` everywhere (pre-0008 server).
	NoEpochs bool
	// NoStateVector makes /state-vector an unknown route.
	NoStateVector bool

	stateVectors int
	epochSeq     int
}

// Head returns the vault's current change seq.
func (s *Server) Head() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.head
}

// New starts a fake server for one vault.
func New(vaultID, slug string) *Server {
	s := &Server{VaultID: vaultID, Slug: slug, notes: map[string]*Note{}, states: map[string]string{}, expired: map[string]bool{}}
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

// clock is the note's lib0-v1 state vector: one client (the history's)
// whose clock grows with every version.
func clock(n *Note) string {
	return EncodeSV(map[uint64]uint64{n.client: uint64(n.version + 1)})
}

// EncodeSV encodes a lib0-v1 state vector (base64), clients ascending.
func EncodeSV(sv map[uint64]uint64) string {
	keys := make([]uint64, 0, len(sv))
	for k := range sv {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
	buf := putUvarint(nil, uint64(len(keys)))
	for _, k := range keys {
		buf = putUvarint(buf, k)
		buf = putUvarint(buf, sv[k])
	}
	return base64.StdEncoding.EncodeToString(buf)
}

func putUvarint(b []byte, v uint64) []byte {
	for v >= 0x80 {
		b = append(b, byte(v)|0x80)
		v >>= 7
	}
	return append(b, byte(v))
}

func (s *Server) newEpochLocked() (string, uint64) {
	s.epochSeq++
	return fmt.Sprintf("epoch-%02d", s.epochSeq), uint64(100 + s.epochSeq)
}

// Epoch returns a note's current crdt_epoch.
func (s *Server) Epoch(id string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.notes[id].Epoch
}

// StateVectors returns how many GET /state-vector requests were served.
func (s *Server) StateVectors() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.stateVectors
}

// Recreate deletes and recreates a note under the same id (protocol
// §12.1): a new epoch and a new history; state tokens of the old
// history are no longer known.
func (s *Server) Recreate(id, text string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := s.notes[id]
	for t := range s.states {
		if strings.HasPrefix(mustDecode(t), n.ID+"@") {
			delete(s.states, t)
		}
	}
	n.Epoch, n.client = s.newEpochLocked()
	n.Text = text
	n.version += 5000
	s.head++
	n.Seq = s.head
	s.stateLocked(n)
}

func mustDecode(t string) string {
	b, _ := base64.StdEncoding.DecodeString(t)
	return string(b)
}

func (s *Server) epochField(m map[string]any, n *Note) map[string]any {
	if !s.NoEpochs {
		m["crdt_epoch"] = n.Epoch
	}
	return m
}

// AddNote creates a note.
func (s *Server) AddNote(id, path, text string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.head++
	n := &Note{ID: id, Path: path, Title: strings.ToUpper(id[:1]) + id[1:], Text: text, Seq: s.head}
	n.Epoch, n.client = s.newEpochLocked()
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

// ExpireHistory makes every state token issued so far unreconstructable
// (history compacted): pushes on old bases get base_too_old. Current
// texts get fresh tokens.
func (s *Server) ExpireHistory() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for t := range s.states {
		s.expired[t] = true
	}
	s.states = map[string]string{}
	for _, n := range s.notes {
		n.version += 1000
		s.stateLocked(n)
	}
}

// Snapshots returns how many GET /snapshot requests were served.
func (s *Server) Snapshots() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.snapshots
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
	return s.epochField(map[string]any{"id": n.ID, "vault_id": s.VaultID, "path": n.Path, "title": n.Title,
		"created_at": "2026-11-01T10:00:00Z", "updated_at": "2026-11-01T10:00:00Z", "change_seq": n.Seq}, n)
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
		s.snapshots++
		body := s.epochField(map[string]any{"id": n.ID, "path": n.Path, "text": n.Text, "vector_clock": clock(n)}, n)
		if !s.OmitState {
			body["state"] = s.stateLocked(n)
		}
		s.mu.Unlock()
		writeJSON(w, 200, body)
	case op == "state-vector" && r.Method == http.MethodGet:
		if s.NoStateVector {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte("Cannot GET " + r.URL.Path))
			return
		}
		s.mu.Lock()
		s.stateVectors++
		body := s.epochField(map[string]any{"id": n.ID, "state_vector": clock(n)}, n)
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
	if !ok && s.expired[req.BaseState] {
		writeJSON(w, 409, map[string]string{"error": "base_too_old"})
		return
	}
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
	out := s.epochField(map[string]any{"id": n.ID, "path": n.Path, "text": n.Text, "vector_clock": clock(n)}, n)
	if !s.DiffOmitState {
		out["state"] = s.stateLocked(n)
	}
	writeJSON(w, 200, out)
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
