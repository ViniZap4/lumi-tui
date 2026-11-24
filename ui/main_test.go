package ui

import (
	"errors"
	"flag"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
	"github.com/vinizap/lumi/tui-client/theme"
	"github.com/vinizap/lumi/tui-client/vim"
)

// openedURLs records what would have been opened in a browser.
var openedURLs []string

// openedInApp records the notes `lumi open` would have opened (gx on a
// mermaid diagram); openInAppResult is what the fake returns.
var (
	openedInApp     []string
	openInAppResult = func(string) (string, error) { return "Opened lumi://open?note=test", nil }
)

var update = flag.Bool("update", false, "rewrite golden files")

// TestMain isolates every UI test from the owner's real config: HOME
// and XDG_CONFIG_HOME point at a temp dir (config.Load/Save, books,
// accounts), and $EDITOR is a no-op. Rendering is plain ASCII colour
// profile so golden files are stable.
func TestMain(m *testing.M) {
	tmp, err := os.MkdirTemp("", "lumi-ui-test-")
	if err != nil {
		panic(err)
	}
	os.Setenv("HOME", tmp)
	os.Setenv("XDG_CONFIG_HOME", filepath.Join(tmp, ".config"))
	os.Setenv("XDG_CACHE_HOME", filepath.Join(tmp, ".cache"))
	os.Setenv("EDITOR", "true")
	// Motion off by default so renders are the settled frames; the
	// motion tests turn it back on with t.Setenv.
	os.Setenv("REDUCE_MOTION", "1")
	lipgloss.SetColorProfile(termenv.Ascii)
	// Never touch the real clipboard, terminal or browser from tests.
	systemClipboard = &vim.MemClipboard{}
	termOut = io.Discard
	openURLFn = func(u string) { openedURLs = append(openedURLs, u) }
	openInAppFn = func(p string) (string, error) {
		openedInApp = append(openedInApp, p)
		return openInAppResult(p)
	}
	// Server checks (GET /api/info) only ever reach httptest servers.
	infoClient = &http.Client{Timeout: 2 * time.Second, Transport: loopbackOnly{}}
	theme.Resolve("dark", "tokyo-night", "catppuccin-latte")
	ApplyTheme()
	code := m.Run()
	os.RemoveAll(tmp)
	os.Exit(code)
}

// golden compares got with testdata/golden/<name>.txt (-update writes).
func golden(t *testing.T, name, got string) {
	t.Helper()
	got = stripAnsi(got)
	var lines []string
	for _, l := range strings.Split(got, "\n") {
		lines = append(lines, strings.TrimRight(l, " "))
	}
	got = strings.Join(lines, "\n") + "\n"
	path := filepath.Join("testdata", "golden", name+".txt")
	if *update {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("missing golden %s (run go test ./ui -run %s -update): %v", path, t.Name(), err)
	}
	if string(want) != got {
		t.Errorf("golden %s mismatch\n--- want\n%s\n--- got\n%s", name, want, got)
	}
}

// loopbackOnly refuses any request that is not to a loopback address.
type loopbackOnly struct{}

func (loopbackOnly) RoundTrip(r *http.Request) (*http.Response, error) {
	if ip := net.ParseIP(r.URL.Hostname()); ip == nil || !ip.IsLoopback() {
		return nil, &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("tests never leave loopback")}
	}
	return http.DefaultTransport.RoundTrip(r)
}
