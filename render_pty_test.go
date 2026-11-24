package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/creack/pty"
	"github.com/vinizap/lumi/tui-client/ui"
)

// The renderer test drives the real program (alt screen, mouse, the
// standard renderer) inside a pseudo-terminal — never a visible
// window — and measures what reaches the terminal per scroll step.
// It guards the "o tui fica piscando quando scrolla" regression: a
// scroll step must never erase a line before repainting it (the
// Bubbletea < v0.26 renderer cleared every changed line with ESC[2K
// in one pass and repainted it in another, so the terminal showed
// blank rows between the two), never clear the whole screen, and stay
// within a bounded number of bytes.

type ptyStep struct {
	name       string
	bytes      int
	clearLine  int // ESC[2K: a line blanked before it is repainted
	clearAll   int // ESC[2J
	eraseRight int // ESC[K after the new content (no blank gap)
	syncFrames int // DEC 2026 synchronized updates
}

var (
	reClearLine  = regexp.MustCompile(`\x1b\[2K`)
	reClearAll   = regexp.MustCompile(`\x1b\[2J`)
	reEraseRight = regexp.MustCompile(`\x1b\[0?K`)
	reSyncBegin  = regexp.MustCompile(`\x1b\[\?2026h`)
)

var dumpHook func(string, []byte)

type ptyCapture struct {
	mu   sync.Mutex
	buf  bytes.Buffer
	last time.Time
}

func (c *ptyCapture) take() []byte {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := append([]byte(nil), c.buf.Bytes()...)
	c.buf.Reset()
	c.last = time.Now()
	return out
}

// quiet waits until nothing was written for d (or max elapsed).
func (c *ptyCapture) quiet(d, maxWait time.Duration) {
	deadline := time.Now().Add(maxWait)
	for time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
		c.mu.Lock()
		idle := time.Since(c.last) >= d
		c.mu.Unlock()
		if idle {
			return
		}
	}
}

const ptyNote = `---
title: Flicker
---

# Rolagem sem piscar

Texto com acentuação, emoji 🙂 e largura dupla 漢字 para medir a largura.
Um [link interno](other.md) e um [externo](https://example.com).

- [ ] tarefa aberta
- [x] tarefa feita

| coluna | outra | 🙂 |
|--------|-------|----|
| a      | b     | c  |

` + "```go\nfunc main() { println(\"oi\") }\n```\n"

func writePTYBook(t *testing.T) (root, note string) {
	t.Helper()
	root = t.TempDir()
	var b strings.Builder
	b.WriteString(ptyNote)
	for i := 0; i < 120; i++ {
		fmt.Fprintf(&b, "\nParágrafo %d — lorem ipsum dolor sit amet, consectetur adipiscing elit, sed do eiusmod 🙂 tempor.\n", i)
		if i%10 == 0 {
			fmt.Fprintf(&b, "\n## Seção %d\n\n- item com [[other]] e **negrito**\n", i)
		}
	}
	note = filepath.Join(root, "flicker.md")
	if err := os.WriteFile(note, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "other.md"), []byte("# Other\n\nhello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return root, note
}

// runInPTY starts the program on note at cols×rows and feeds each input,
// returning the bytes the terminal received for each one.
func runInPTY(t *testing.T, root, note string, cols, rows int, inputs []struct{ name, seq string }) []ptyStep {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(home, ".cache"))
	t.Setenv("TERM", "xterm-256color")
	t.Setenv("COLORTERM", "truecolor")

	master, slave, err := pty.Open()
	if err != nil {
		t.Skipf("no pty: %v", err)
	}
	defer master.Close()
	defer slave.Close()
	if err := pty.Setsize(master, &pty.Winsize{Rows: uint16(rows), Cols: uint16(cols)}); err != nil {
		t.Fatal(err)
	}
	capt := &ptyCapture{last: time.Now()}
	go func() {
		b := make([]byte, 64*1024)
		for {
			n, err := master.Read(b)
			if n > 0 {
				capt.mu.Lock()
				capt.buf.Write(b[:n])
				capt.last = time.Now()
				capt.mu.Unlock()
			}
			if err != nil {
				return
			}
		}
	}()

	model := ui.NewModelWithInitialNote(root, note)
	p := tea.NewProgram(model, ui.ProgramOptions(slave, slave)...)
	done := make(chan struct{})
	go func() {
		_, _ = p.Run()
		close(done)
	}()
	capt.quiet(250*time.Millisecond, 5*time.Second)
	capt.take()

	var out []ptyStep
	for _, in := range inputs {
		if _, err := master.Write([]byte(in.seq)); err != nil {
			t.Fatal(err)
		}
		capt.quiet(120*time.Millisecond, 3*time.Second)
		b := capt.take()
		if dumpHook != nil {
			dumpHook(fmt.Sprintf("%02d-%s", len(out), in.name), b)
		}
		out = append(out, ptyStep{
			name:       in.name,
			bytes:      len(b),
			clearLine:  len(reClearLine.FindAll(b, -1)),
			clearAll:   len(reClearAll.FindAll(b, -1)),
			eraseRight: len(reEraseRight.FindAll(b, -1)),
			syncFrames: len(reSyncBegin.FindAll(b, -1)),
		})
	}
	p.Quit()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		p.Kill()
	}
	return out
}

func scrollInputs() []struct{ name, seq string } {
	var in []struct{ name, seq string }
	for i := 0; i < 6; i++ {
		in = append(in, struct{ name, seq string }{"j", "j"})
	}
	for i := 0; i < 3; i++ {
		in = append(in, struct{ name, seq string }{"ctrl+d", "\x04"})
	}
	in = append(in, struct{ name, seq string }{"ctrl+u", "\x15"})
	for i := 0; i < 4; i++ {
		// SGR wheel down at column 20, row 10
		in = append(in, struct{ name, seq string }{"wheel", "\x1b[<65;20;10M"})
	}
	return in
}

func TestRenderScrollNoFlicker(t *testing.T) {
	if testing.Short() {
		t.Skip("pty renderer test")
	}
	for _, reduced := range []string{"", "1"} {
		t.Run("reduce_motion="+reduced, func(t *testing.T) {
			t.Setenv("REDUCE_MOTION", reduced)
			checkScroll(t)
		})
	}
}

func checkScroll(t *testing.T) {
	root, note := writePTYBook(t)
	const cols, rows = 120, 40
	steps := runInPTY(t, root, note, cols, rows, scrollInputs())
	total := 0
	for _, s := range steps {
		t.Logf("%-7s bytes=%6d clearLine=%3d clearAll=%d eraseRight=%3d sync=%d", s.name, s.bytes, s.clearLine, s.clearAll, s.eraseRight, s.syncFrames)
		total += s.bytes
		if s.clearAll > 0 {
			t.Errorf("%s: full-screen clear while scrolling", s.name)
		}
		if s.clearLine > 0 {
			t.Errorf("%s: %d lines blanked (ESC[2K) before being repainted", s.name, s.clearLine)
		}
		if s.bytes == 0 {
			t.Errorf("%s: nothing repainted (did the scroll reach the program?)", s.name)
		}
		// ~4 bytes per cell covers a full repaint of every row with one
		// truecolor SGR per style run (was ~20 per cell before compactSGR)
		if limit := cols * rows * 4; s.bytes > limit {
			t.Errorf("%s: %d bytes for one scroll step (limit %d)", s.name, s.bytes, limit)
		}
		if s.bytes > 0 && s.syncFrames == 0 {
			t.Errorf("%s: frame not wrapped in a synchronized update (DEC 2026)", s.name)
		}
	}
	t.Logf("total %d bytes over %d steps (avg %d)", total, len(steps), total/max(1, len(steps)))
}
