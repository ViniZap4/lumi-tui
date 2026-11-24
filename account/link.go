package account

// Local device-link channel between the TUI and Lumi.app on one machine
// (SPEC-LIVROS §8.4). Files in `~/.config/lumi/link/` (0700, files 0600,
// atomic writes):
//
//	<client>.alive   {"v":1,"pid":…,"started_at":…}  while the client runs
//	req-<id>.json    a request for a session, with a PKCE challenge
//	resp-<id>.json   the holder's answer: a code, or an error
//
// The verifier never touches the disk, so a code read from resp-*.json by
// any other process is useless. The TUI answers Lumi.app's requests on
// its own (its own token sits in a 0600 file anyway); Lumi.app always
// asks the user before answering the TUI's.

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Client names on the channel.
const (
	LinkClientTUI   = "tui"
	LinkClientApple = "apple"
)

// LinkRequestTTL: an unanswered request older than this is stale unless
// its requester is still running.
const LinkRequestTTL = 5 * time.Minute

// Link errors.
var (
	ErrLinkDenied    = errors.New("Lumi.app did not allow the sign-in")
	ErrLinkNoSession = errors.New("Lumi.app has no session for this account")
	ErrLinkFailed    = errors.New("Lumi.app could not create a link")
	ErrLinkNoPeer    = errors.New("Lumi.app is not running")
)

// LinkRequest is req-<id>.json.
type LinkRequest struct {
	V         int    `json:"v"`
	ID        string `json:"id"`
	From      string `json:"from"`
	To        string `json:"to"`
	Server    string `json:"server"`
	Username  string `json:"username"`
	Challenge string `json:"challenge"`
	Label     string `json:"label"`
	PID       int    `json:"pid"`
	CreatedAt string `json:"created_at"`
}

// LinkResponse is resp-<id>.json: Code + ExpiresAt, or Error.
type LinkResponse struct {
	V         int    `json:"v"`
	ID        string `json:"id"`
	Code      string `json:"code,omitempty"`
	ExpiresAt string `json:"expires_at,omitempty"`
	Error     string `json:"error,omitempty"`
}

type presence struct {
	V         int    `json:"v"`
	PID       int    `json:"pid"`
	StartedAt string `json:"started_at"`
}

// LinkChannel is one client's view of the link directory.
type LinkChannel struct {
	Dir      string
	Self     string
	Now      func() time.Time
	PIDAlive func(int) bool
	PID      int
}

// NewLinkChannel returns the TUI's channel at ~/.config/lumi/link.
func NewLinkChannel() (*LinkChannel, error) {
	dir, err := Dir()
	if err != nil {
		return nil, err
	}
	return &LinkChannel{Dir: filepath.Join(dir, "link"), Self: LinkClientTUI}, nil
}

func (ch *LinkChannel) now() time.Time {
	if ch.Now != nil {
		return ch.Now().UTC()
	}
	return time.Now().UTC()
}

func (ch *LinkChannel) pid() int {
	if ch.PID != 0 {
		return ch.PID
	}
	return os.Getpid()
}

func (ch *LinkChannel) alive(pid int) bool {
	if ch.PIDAlive != nil {
		return ch.PIDAlive(pid)
	}
	return pidAlive(pid)
}

func (ch *LinkChannel) ensureDir() error {
	if err := os.MkdirAll(ch.Dir, 0700); err != nil {
		return err
	}
	return os.Chmod(ch.Dir, 0700)
}

func (ch *LinkChannel) writeJSON(name string, v any) error {
	if err := ch.ensureDir(); err != nil {
		return err
	}
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return writeFileAtomic(filepath.Join(ch.Dir, name), append(data, '\n'))
}

func (ch *LinkChannel) readJSON(name string, v any) error {
	data, err := os.ReadFile(filepath.Join(ch.Dir, name))
	if err != nil {
		return err
	}
	return json.Unmarshal(data, v)
}

// WritePresence announces this client as running.
func (ch *LinkChannel) WritePresence() error {
	return ch.writeJSON(ch.Self+".alive", presence{V: 1, PID: ch.pid(), StartedAt: stamp(ch.now())})
}

// RemovePresence withdraws the announcement (only if it is ours).
func (ch *LinkChannel) RemovePresence() {
	var p presence
	if ch.readJSON(ch.Self+".alive", &p) == nil && p.PID == ch.pid() {
		_ = os.Remove(filepath.Join(ch.Dir, ch.Self+".alive"))
	}
}

// PeerAlive reports whether the named client announced itself and its
// process still exists.
func (ch *LinkChannel) PeerAlive(peer string) bool {
	var p presence
	if err := ch.readJSON(peer+".alive", &p); err != nil {
		return false
	}
	return ch.alive(p.PID)
}

// NewVerifier returns a PKCE verifier (32 random bytes, base64url) and its
// S256 challenge.
func NewVerifier() (verifier, challenge string, err error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", "", err
	}
	verifier = base64.RawURLEncoding.EncodeToString(buf)
	return verifier, PKCEChallenge(verifier), nil
}

// PKCEChallenge is base64url-nopad(SHA-256(verifier)).
func PKCEChallenge(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

func newID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	h := hex.EncodeToString(b)
	return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:]
}

// WriteRequest asks `to` for a session for (server, username). The
// verifier is returned to the caller and must stay in memory.
func (ch *LinkChannel) WriteRequest(to, server, username, label string) (LinkRequest, string, error) {
	verifier, challenge, err := NewVerifier()
	if err != nil {
		return LinkRequest{}, "", err
	}
	req := LinkRequest{V: 1, ID: newID(), From: ch.Self, To: to, Server: normalizeOrSelf(server),
		Username: lowerTrim(username), Challenge: challenge, Label: label, PID: ch.pid(), CreatedAt: stamp(ch.now())}
	if err := ch.writeJSON("req-"+req.ID+".json", req); err != nil {
		return LinkRequest{}, "", err
	}
	return req, verifier, nil
}

// ReadResponse returns the answer to request id, or nil while none.
func (ch *LinkChannel) ReadResponse(id string) (*LinkResponse, error) {
	var r LinkResponse
	err := ch.readJSON("resp-"+id+".json", &r)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if r.ID != id {
		return nil, fmt.Errorf("link response id mismatch")
	}
	return &r, nil
}

// Finish deletes a request and its response.
func (ch *LinkChannel) Finish(id string) {
	_ = os.Remove(filepath.Join(ch.Dir, "req-"+id+".json"))
	_ = os.Remove(filepath.Join(ch.Dir, "resp-"+id+".json"))
}

func validChallenge(s string) bool {
	if len(s) != 43 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !(c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-' || c == '_') {
			return false
		}
	}
	return true
}

func (ch *LinkChannel) stale(r LinkRequest) bool {
	created := parseStamp(r.CreatedAt)
	if !created.IsZero() && ch.now().Sub(created) <= LinkRequestTTL {
		return false
	}
	return !ch.alive(r.PID)
}

// Pending lists valid, unanswered requests addressed to this client and
// garbage-collects stale ones (any direction).
func (ch *LinkChannel) Pending() []LinkRequest {
	entries, err := os.ReadDir(ch.Dir)
	if err != nil {
		return nil
	}
	var out []LinkRequest
	for _, e := range entries {
		name := e.Name()
		if !strings.HasPrefix(name, "req-") || !strings.HasSuffix(name, ".json") {
			continue
		}
		var r LinkRequest
		if err := ch.readJSON(name, &r); err != nil || r.V != 1 || "req-"+r.ID+".json" != name {
			continue
		}
		if ch.stale(r) {
			ch.Finish(r.ID)
			continue
		}
		if r.To != ch.Self || r.From == ch.Self || (r.From != LinkClientApple && r.From != LinkClientTUI) {
			continue
		}
		if !validChallenge(r.Challenge) || r.Server == "" || r.Username == "" {
			continue
		}
		if _, err := os.Stat(filepath.Join(ch.Dir, "resp-"+r.ID+".json")); err == nil {
			continue // already answered
		}
		out = append(out, r)
	}
	return out
}

// Respond writes the answer to a request.
func (ch *LinkChannel) Respond(r LinkResponse) error {
	r.V = 1
	return ch.writeJSON("resp-"+r.ID+".json", r)
}

// ServeLinkRequests answers Lumi.app's pending requests with this TUI's
// sessions, without asking (SPEC-LIVROS §8.2). newClient lets tests
// inject an HTTP client. Returns how many requests got a code.
func ServeLinkRequests(ctx context.Context, ch *LinkChannel, f *File, newClient func(string) (*Client, error)) int {
	if newClient == nil {
		newClient = NewClient
	}
	served := 0
	for _, r := range ch.Pending() {
		resp := LinkResponse{ID: r.ID}
		a := f.Find(r.Server, r.Username)
		switch {
		case a == nil || a.Token == "":
			resp.Error = "no_session"
		default:
			c, err := newClient(a.Server)
			if err != nil {
				resp.Error = "failed"
				break
			}
			c.SetToken(a.Token)
			cctx, cancel := context.WithTimeout(ctx, 15*time.Second)
			label := r.Label
			if label == "" {
				label = "Lumi.app"
			}
			link, err := c.IssueDeviceLink(cctx, r.Challenge, label)
			cancel()
			switch {
			case errors.Is(err, ErrUnauthorized):
				resp.Error = "no_session"
			case err != nil:
				resp.Error = "failed"
			default:
				resp.Code = link.Code
				resp.ExpiresAt = stamp(link.ExpiresAt)
				served++
			}
		}
		_ = ch.Respond(resp)
	}
	return served
}

// RequestLink asks Lumi.app for a session for (server, username) and,
// once it answers, redeems the code. It blocks until an answer, ctx is
// done, or the app stops running. The caller stores the session (Upsert +
// Save).
func RequestLink(ctx context.Context, ch *LinkChannel, server, username string, poll time.Duration,
	newClient func(string) (*Client, error)) (*SessionResponse, error) {
	if newClient == nil {
		newClient = NewClient
	}
	if poll <= 0 {
		poll = 300 * time.Millisecond
	}
	if !ch.PeerAlive(LinkClientApple) {
		return nil, ErrLinkNoPeer
	}
	req, verifier, err := ch.WriteRequest(LinkClientApple, server, username, "lumi TUI")
	if err != nil {
		return nil, err
	}
	defer ch.Finish(req.ID)
	t := time.NewTicker(poll)
	defer t.Stop()
	checks := 0
	for {
		resp, err := ch.ReadResponse(req.ID)
		if err != nil {
			return nil, err
		}
		if resp != nil {
			switch resp.Error {
			case "":
			case "denied":
				return nil, ErrLinkDenied
			case "no_session":
				return nil, ErrLinkNoSession
			default:
				return nil, ErrLinkFailed
			}
			c, err := newClient(req.Server)
			if err != nil {
				return nil, err
			}
			return c.RedeemDeviceLink(ctx, resp.Code, verifier, "lumi TUI")
		}
		checks++
		if checks%10 == 0 && !ch.PeerAlive(LinkClientApple) {
			return nil, ErrLinkNoPeer
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-t.C:
		}
	}
}

// LinkAndSave runs RequestLink and stores the new session in the
// registry + tokens.yaml, clearing the account's link opt-out. A denial
// sets the opt-out so the TUI doesn't ask again on its own.
func LinkAndSave(ctx context.Context, ch *LinkChannel, server, username string, poll time.Duration,
	newClient func(string) (*Client, error)) (*Account, error) {
	resp, err := RequestLink(ctx, ch, server, username, poll, newClient)
	f, lerr := LoadFrom(filepath.Join(filepath.Dir(ch.Dir), "accounts.yaml"))
	if lerr != nil {
		return nil, lerr
	}
	if err != nil {
		if errors.Is(err, ErrLinkDenied) {
			f.SetLinkOptOut(server, username, true)
			_ = f.Save()
		}
		return nil, err
	}
	a := Account{Server: normalizeOrSelf(server), Username: resp.User.Username, UserID: resp.User.ID,
		DisplayName: resp.User.DisplayName, Token: resp.Token, ExpiresAt: resp.ExpiresAt,
		SignedInAt: time.Now().UTC()}
	if a.Username == "" {
		a.Username = lowerTrim(username)
	}
	f.Upsert(a)
	f.SetLinkOptOut(a.Server, a.Username, false)
	if err := f.Save(); err != nil {
		return nil, err
	}
	return f.Find(a.Server, a.Username), nil
}

// NeedsLink lists registry accounts this TUI has no session for and that
// the user didn't opt out of linking.
func (f *File) NeedsLink() []Account {
	var out []Account
	for _, a := range f.Accounts {
		if a.Token == "" && !f.LinkOptedOut(a.Server, a.Username) {
			out = append(out, a)
		}
	}
	return out
}
