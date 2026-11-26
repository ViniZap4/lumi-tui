package ui

// Benchmarks for the TUI render paths (docs/PERF-BUDGETS.md, umbrella:
// TUI reader note open <= 50 ms, 0 wakeups/s idle). CI compares them
// with main through scripts/bench-gate.sh (benchstat, > 10 % blocks).
//
//   go test ./ui -run '^$' -bench . -benchmem

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/vinizap/lumi/tui-client/internal/perfcorpus"
)

var benchNoteSizes = []int{20 * perfcorpus.KB, 200 * perfcorpus.KB}

var benchScreens = [][2]int{{80, 24}, {240, 70}}

func benchPress(m Model, ks ...string) Model {
	for _, k := range ks {
		next, cmd := m.Update(keyMsg(k))
		m = settle(next.(Model), cmd)
	}
	return m
}

// benchPressSync is benchPress for setup: it runs the key's commands to
// completion (listing 10k files takes longer than drainFast waits).
func benchPressSync(m Model, k string) Model {
	next, cmd := m.Update(keyMsg(k))
	m = next.(Model)
	for _, msg := range drainCmd(cmd) {
		next, more := m.Update(msg)
		m = settle(next.(Model), more)
	}
	return m
}

func benchResize(m Model, w, h int) Model {
	next, cmd := m.Update(tea.WindowSizeMsg{Width: w, Height: h})
	return settle(next.(Model), cmd)
}

// benchNoteModel opens a corpus note of size bytes in the reader.
func benchNoteModel(b *testing.B, size, w, h int) Model {
	b.Helper()
	dir := b.TempDir()
	path := filepath.Join(dir, "big.md")
	if err := os.WriteFile(path, []byte(perfcorpus.Prose(size)), 0o600); err != nil {
		b.Fatal(err)
	}
	m := benchResize(drive(NewModelWithInitialNote(dir, path)), w, h)
	if m.viewMode != ViewFullNote || m.fullNote == nil {
		b.Fatalf("setup: note not open (view %v)", m.viewMode)
	}
	_ = m.View() // first frame renders and caches the prose
	return m
}

// BenchmarkProseRender: glamour + callouts + footnotes for a whole note,
// uncached (what opening a note or resizing the terminal costs).
func BenchmarkProseRender(b *testing.B) {
	for _, size := range benchNoteSizes {
		b.Run(fmt.Sprintf("%dKB/w80", size/perfcorpus.KB), func(b *testing.B) {
			md := perfcorpus.Prose(size)
			c := newProseCache()
			_ = c.render("# warm the renderer", 80)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				_ = renderProse(c, md, 80)
			}
		})
	}
}

// BenchmarkNoteOpen: model creation to the first reader frame of a big
// note (lumi <note>): disk read, prose render, layout.
func BenchmarkNoteOpen(b *testing.B) {
	for _, size := range benchNoteSizes {
		b.Run(fmt.Sprintf("%dKB", size/perfcorpus.KB), func(b *testing.B) {
			dir := b.TempDir()
			path := filepath.Join(dir, "big.md")
			if err := os.WriteFile(path, []byte(perfcorpus.Prose(size)), 0o600); err != nil {
				b.Fatal(err)
			}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				m := drive(NewModelWithInitialNote(dir, path))
				_ = m.View()
			}
		})
	}
}

// BenchmarkReaderView: one steady-state frame of the reader (prose
// cached), the cost every repaint pays.
func BenchmarkReaderView(b *testing.B) {
	for _, size := range benchNoteSizes {
		for _, sc := range benchScreens {
			b.Run(fmt.Sprintf("%dKB/%dx%d", size/perfcorpus.KB, sc[0], sc[1]), func(b *testing.B) {
				m := benchNoteModel(b, size, sc[0], sc[1])
				b.ReportAllocs()
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					_ = m.View()
				}
			})
		}
	}
}

// BenchmarkReaderScroll: one `j` keystroke in the reader, Update + View.
func BenchmarkReaderScroll(b *testing.B) {
	for _, size := range benchNoteSizes {
		b.Run(fmt.Sprintf("%dKB/80x24", size/perfcorpus.KB), func(b *testing.B) {
			m := benchNoteModel(b, size, 80, 24)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				k := "j"
				if i%400 >= 200 { // stay inside the note
					k = "k"
				}
				m = benchPress(m, k)
				_ = m.View()
			}
		})
	}
}

var (
	benchBookOnce sync.Once
	benchBookDir  string
	benchBookErr  error
)

// benchBook writes a book with n notes in one folder (the widest Miller
// column a big book produces) once per test binary, under the temp HOME
// that TestMain removes at exit; b.Run calls setup again for every b.N.
func benchBook(b *testing.B, n int) string {
	b.Helper()
	benchBookOnce.Do(func() {
		benchBookDir = filepath.Join(os.Getenv("HOME"), "bench-book")
		sub := filepath.Join(benchBookDir, "inbox")
		if benchBookErr = os.MkdirAll(sub, 0o700); benchBookErr != nil {
			return
		}
		for i := 0; i < n; i++ {
			body := fmt.Sprintf("---\ntitle: Nota %05d\n---\n\n# Nota %05d\n", i, i)
			if benchBookErr = os.WriteFile(filepath.Join(sub, fmt.Sprintf("nota-%05d.md", i)), []byte(body), 0o600); benchBookErr != nil {
				return
			}
		}
	})
	if benchBookErr != nil {
		b.Fatal(benchBookErr)
	}
	return benchBookDir
}

const benchBookNotes = 10_000

// benchExplorerModel opens the floating explorer inside the 10k folder.
func benchExplorerModel(b *testing.B, w, h int) Model {
	b.Helper()
	dir := benchBook(b, benchBookNotes)
	forgetLastNoteB(dir)
	m := benchResize(drive(NewModelWithInitialNote(dir, "")), w, h)
	if !m.floatExp {
		b.Fatal("setup: explorer not open")
	}
	m = benchPressSync(m, "l") // into inbox/
	if len(m.items) < benchBookNotes {
		b.Fatalf("setup: %d items, want %d", len(m.items), benchBookNotes)
	}
	_ = m.View()
	return m
}

// forgetLastNoteB drops the book's last-note record so every setup
// starts on the explorer, not on a note a previous run left open.
func forgetLastNoteB(dir string) {
	ln := readLastNotes()
	delete(ln.Books, filepath.Clean(dir))
	if data, err := json.Marshal(ln); err == nil {
		if f := lastNotesFile(); f != "" {
			_ = os.MkdirAll(filepath.Dir(f), 0o700)
			_ = os.WriteFile(f, data, 0o600)
		}
	}
}

// BenchmarkExplorerView: one frame of the floating explorer over a
// 10k-entry folder.
func BenchmarkExplorerView(b *testing.B) {
	for _, sc := range benchScreens {
		b.Run(fmt.Sprintf("10k/%dx%d", sc[0], sc[1]), func(b *testing.B) {
			m := benchExplorerModel(b, sc[0], sc[1])
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				_ = m.View()
			}
		})
	}
}

// BenchmarkExplorerCursor: one `j`/`k` in a 10k-entry folder,
// Update + View (cursor travel; preview of the selected note included).
func BenchmarkExplorerCursor(b *testing.B) {
	m := benchExplorerModel(b, 120, 40)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		k := "j"
		if i%2000 >= 1000 {
			k = "k"
		}
		m = benchPress(m, k)
		_ = m.View()
	}
}
