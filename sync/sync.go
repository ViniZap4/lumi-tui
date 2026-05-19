package sync

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
	"github.com/vinizap/lumi/tui-client/config"
)

// Timing constants. pongWait is the budget for the next message from the
// server (any message resets the deadline, but a pong arrives at least
// every pingPeriod). pingPeriod must be strictly less than pongWait so
// our pings can race a hung peer to the deadline.
const (
	pongWait         = 60 * time.Second
	pingPeriod       = (pongWait * 9) / 10 // 54s
	writeWait        = 10 * time.Second
	handshakeTimeout = 15 * time.Second
	initialBackoff   = 1 * time.Second
	maxBackoff       = 60 * time.Second
	maxReadBytes     = 1 << 20 // 1 MiB — sync events should never exceed
)

// Event is a sync event received from the server.
type Event struct {
	Type string          `json:"type"`
	Note json.RawMessage `json:"note,omitempty"`
}

// StatusKind enumerates the three states the UI cares about.
type StatusKind int

const (
	StatusConnected StatusKind = iota
	StatusDisconnected
	StatusDropped // Connected, but the event buffer overflowed.
)

// StatusMsg reports connection state changes (and event drops) to the UI.
type StatusMsg struct {
	Kind       StatusKind
	Err        error
	LostEvents uint64 // cumulative since client start
}

// Client manages a WebSocket connection to the lumi server for real-time
// sync, with auth, ping/pong keepalive, exponential reconnect backoff,
// and visibility into dropped events when the consumer falls behind.
type Client struct {
	cfg      *config.FolderConfig
	eventCh  chan Event
	statusCh chan StatusMsg
	done     chan struct{}

	stopOnce sync.Once
	lost     atomic.Uint64

	// connMu guards conn for the Stop() path that needs to abort an
	// in-flight ReadMessage. gorilla/websocket's Close is documented as
	// safe to call concurrently with any other method.
	connMu sync.Mutex
	conn   *websocket.Conn
}

// NewClient creates a sync client from a folder config. Returns nil if
// the folder has no server configured — the caller treats nil as "no
// sync" rather than failing.
func NewClient(cfg *config.FolderConfig) *Client {
	if cfg == nil || cfg.ServerURL == "" {
		return nil
	}
	return &Client{
		cfg:      cfg,
		eventCh:  make(chan Event, 32),
		statusCh: make(chan StatusMsg, 8),
		done:     make(chan struct{}),
	}
}

// Start launches the connect loop in a background goroutine.
func (c *Client) Start() {
	go c.connectLoop()
}

// Stop terminates the client. Idempotent; safe to call from any
// goroutine. Closes the active connection (if any) so a blocking
// ReadMessage returns promptly.
func (c *Client) Stop() {
	c.stopOnce.Do(func() {
		close(c.done)
		c.connMu.Lock()
		if c.conn != nil {
			_ = c.conn.Close()
		}
		c.connMu.Unlock()
	})
}

// Events returns the channel that receives sync events.
func (c *Client) Events() <-chan Event { return c.eventCh }

// Status returns the channel that receives connection-state messages.
func (c *Client) Status() <-chan StatusMsg { return c.statusCh }

// LostEvents returns the cumulative number of sync events dropped
// because the consumer fell behind.
func (c *Client) LostEvents() uint64 { return c.lost.Load() }

func (c *Client) connectLoop() {
	attempt := 0
	for {
		select {
		case <-c.done:
			return
		default:
		}

		c.connect()

		wait := backoffFor(attempt)
		attempt++

		select {
		case <-c.done:
			return
		case <-time.After(wait):
		}
	}
}

// backoffFor returns an exponential-backoff duration with ±20% jitter,
// capped at maxBackoff. Pure function so tests can pin the curve.
func backoffFor(attempt int) time.Duration {
	if attempt < 0 {
		attempt = 0
	}
	if attempt > 6 {
		attempt = 6 // cap before bit-shift overflow concern; capped again below
	}
	base := time.Duration(1<<uint(attempt)) * initialBackoff
	if base > maxBackoff {
		base = maxBackoff
	}
	jitter := time.Duration(rand.Int63n(int64(base/5) + 1))
	if rand.Intn(2) == 0 {
		jitter = -jitter
	}
	out := base + jitter
	if out < initialBackoff {
		out = initialBackoff
	}
	return out
}

func (c *Client) connect() {
	wsURL, err := c.wsURL()
	if err != nil {
		c.sendStatus(StatusMsg{Kind: StatusDisconnected, Err: fmt.Errorf("ws url: %w", err)})
		return
	}
	if wsURL == "" {
		return
	}

	header := http.Header{}
	if tok := strings.TrimSpace(c.cfg.ServerToken); tok != "" {
		// Defence in depth: token also goes in the URL query (per the
		// project's documented WS scheme), but a bearer header is the
		// preferred form when the server accepts it.
		header.Set("Authorization", "Bearer "+tok)
	}

	dialer := *websocket.DefaultDialer // copy so we don't mutate the package singleton
	dialer.HandshakeTimeout = handshakeTimeout

	conn, _, err := dialer.Dial(wsURL, header)
	if err != nil {
		c.sendStatus(StatusMsg{Kind: StatusDisconnected, Err: err})
		return
	}
	defer conn.Close()

	c.connMu.Lock()
	c.conn = conn
	c.connMu.Unlock()
	defer func() {
		c.connMu.Lock()
		c.conn = nil
		c.connMu.Unlock()
	}()

	// Subscribe handshake. If this write fails we must NOT report
	// Connected — the previous code did, leaving the UI lying about a
	// dead session until the next read errored.
	if err := conn.WriteJSON(map[string]string{"type": "subscribe"}); err != nil {
		c.sendStatus(StatusMsg{Kind: StatusDisconnected, Err: fmt.Errorf("subscribe: %w", err)})
		return
	}

	conn.SetReadLimit(maxReadBytes)
	_ = conn.SetReadDeadline(time.Now().Add(pongWait))
	conn.SetPongHandler(func(string) error {
		return conn.SetReadDeadline(time.Now().Add(pongWait))
	})

	c.sendStatus(StatusMsg{Kind: StatusConnected})

	// Ping ticker — WriteControl is the one write method documented as
	// concurrent-safe with the reader, so we can run it alongside the
	// reader without a write mutex.
	readDone := make(chan struct{})
	go c.pingLoop(conn, readDone)
	defer close(readDone)

	for {
		select {
		case <-c.done:
			return
		default:
		}

		_, raw, err := conn.ReadMessage()
		if err != nil {
			c.sendStatus(StatusMsg{Kind: StatusDisconnected, Err: err})
			return
		}

		var evt Event
		if err := json.Unmarshal(raw, &evt); err != nil {
			// Malformed payloads aren't fatal; skip.
			continue
		}

		select {
		case c.eventCh <- evt:
		default:
			lost := c.lost.Add(1)
			c.sendStatus(StatusMsg{Kind: StatusDropped, LostEvents: lost})
		}
	}
}

func (c *Client) pingLoop(conn *websocket.Conn, readDone <-chan struct{}) {
	ticker := time.NewTicker(pingPeriod)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			deadline := time.Now().Add(writeWait)
			if err := conn.WriteControl(websocket.PingMessage, nil, deadline); err != nil {
				// WriteControl failed — the reader will see the next
				// ReadMessage error and tear down. Exit the ping loop
				// to avoid spamming a dead socket.
				return
			}
		case <-c.done:
			return
		case <-readDone:
			return
		}
	}
}

// sendStatus is non-blocking and stamps the current LostEvents count
// onto outbound messages so consumers always see the latest total.
func (c *Client) sendStatus(s StatusMsg) {
	if s.LostEvents == 0 {
		s.LostEvents = c.lost.Load()
	}
	select {
	case c.statusCh <- s:
	default:
	}
}

// wsURL builds the WebSocket URL the client dials. Token (if any) goes
// on the query string per the project's WS auth convention. Returns
// ("", nil) when no server is configured — connect() treats this as a
// no-op rather than an error.
func (c *Client) wsURL() (string, error) {
	serverURL := strings.TrimRight(c.cfg.ServerURL, "/")
	if serverURL == "" {
		return "", nil
	}

	switch {
	case strings.HasPrefix(serverURL, "https://"):
		serverURL = "wss://" + strings.TrimPrefix(serverURL, "https://")
	case strings.HasPrefix(serverURL, "http://"):
		serverURL = "ws://" + strings.TrimPrefix(serverURL, "http://")
	}

	u, err := url.Parse(serverURL + "/ws")
	if err != nil {
		return "", err
	}

	if tok := strings.TrimSpace(c.cfg.ServerToken); tok != "" {
		q := u.Query()
		q.Set("token", tok)
		u.RawQuery = q.Encode()
	}

	return u.String(), nil
}
