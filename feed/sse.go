// Package feed is the TUI side of the per-account event stream of
// SPEC-LIVROS §3: `GET /api/events` read with net/http (never a URL
// token), an incremental SSE parser, reconnect with backoff, and the
// catch-up loop that reconciles books through `GET .../changes`.
package feed

import (
	"bytes"
	"strings"
)

// Event is one dispatched SSE event.
type Event struct {
	Name string // "message" when the stream omitted `event:`
	Data string // `data:` lines joined with "\n"
}

// Parser is an incremental text/event-stream decoder (WHATWG HTML §9.2.6).
// It accepts arbitrary chunk boundaries — mid-line, mid-CRLF, mid-UTF-8 —
// and LF, CRLF or bare CR line endings. Comments (`: ping`) are counted
// but not dispatched. `id:` and `retry:` are ignored: the server sends no
// ids and there is no replay (§3.2).
type Parser struct {
	buf      []byte
	skipLF   bool // previous chunk ended in CR; drop a leading LF
	started  bool // BOM check done
	event    string
	data     strings.Builder
	hasData  bool
	Comments int // total comment lines seen
}

// Feed consumes a chunk and returns the events it completed.
func (p *Parser) Feed(chunk []byte) []Event {
	var out []Event
	if p.skipLF && len(chunk) > 0 {
		if chunk[0] == '\n' {
			chunk = chunk[1:]
		}
		p.skipLF = false
	}
	p.buf = append(p.buf, chunk...)
	if !p.started && len(p.buf) >= 3 {
		p.started = true
		p.buf = bytes.TrimPrefix(p.buf, []byte("\xef\xbb\xbf"))
	}
	for {
		i := bytes.IndexAny(p.buf, "\r\n")
		if i < 0 {
			break
		}
		line := string(p.buf[:i])
		next := i + 1
		if p.buf[i] == '\r' {
			if next < len(p.buf) {
				if p.buf[next] == '\n' {
					next++
				}
			} else {
				p.skipLF = true
			}
		}
		p.buf = p.buf[next:]
		if ev, ok := p.line(line); ok {
			out = append(out, ev)
		}
	}
	return out
}

func (p *Parser) line(line string) (Event, bool) {
	if line == "" {
		if !p.hasData {
			p.event = ""
			return Event{}, false
		}
		name := p.event
		if name == "" {
			name = "message"
		}
		ev := Event{Name: name, Data: p.data.String()}
		p.event, p.hasData = "", false
		p.data.Reset()
		return ev, true
	}
	if line[0] == ':' {
		p.Comments++
		return Event{}, false
	}
	field, value := line, ""
	if i := strings.IndexByte(line, ':'); i >= 0 {
		field, value = line[:i], line[i+1:]
		value = strings.TrimPrefix(value, " ")
	}
	switch field {
	case "event":
		p.event = value
	case "data":
		if p.hasData {
			p.data.WriteByte('\n')
		}
		p.data.WriteString(value)
		p.hasData = true
	}
	return Event{}, false
}
