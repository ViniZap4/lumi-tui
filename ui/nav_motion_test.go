package ui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// --- floating explorer as the only navigation --------------------------

func TestFreshBookShowsEmptyHomeWithExplorerOpen(t *testing.T) {
	dir := notesVault(t)
	m := newTreeModel(t, dir)
	if m.legendContext() != CtxFloat || m.modeName() != "EXPLORE" {
		t.Fatalf("context %v", m.legendContext())
	}
	if v := stripAnsi(m.View()); !strings.Contains(v, "╭") || !strings.Contains(v, "≡ notes") {
		t.Fatal("home shows the ≡ notes button under the open overlay")
	}
	m = keys(t, m, "esc")
	if m.floatExp || m.viewMode != ViewHome || m.legendContext() != CtxHome {
		t.Fatal("Esc closes the explorer, leaving the empty home")
	}
	v := stripAnsi(m.View())
	for _, want := range []string{filepath.Base(dir), "Notas recentes", "One", "Two", "pasta local"} {
		if !strings.Contains(v, want) {
			t.Errorf("home missing %q", want)
		}
	}
	// every way in reopens the same overlay
	for _, seq := range [][]string{{"-"}, {`\`, "e"}, {":", "e", "x", "p", "l", "o", "r", "e", "enter"}} {
		m2 := keys(t, m, seq...)
		if !m2.floatExp || m2.bk != nil {
			t.Fatalf("%v should open the explorer", seq)
		}
	}
	// a recent note opens with ⏎ / its number
	m2 := keys(t, m, "1")
	if m2.viewMode != ViewFullNote || m2.floatExp {
		t.Fatal("1 opens the first recent note")
	}
	_, cmd := m.Update(keyMsg("q"))
	if cmd == nil {
		t.Fatal("q on the home quits")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Fatal("q on the home quits")
	}
}

func TestOverlayKeysAndSplits(t *testing.T) {
	dir := notesVault(t)
	m := newTreeModel(t, dir)
	m = moveTo(t, m, "One")
	m = keys(t, m, "enter")
	if m.viewMode != ViewFullNote || m.floatExp {
		t.Fatal("⏎ opens in the current pane and closes the explorer")
	}
	m = keys(t, m, "-")
	m = moveTo(t, m, "Two")
	m = keys(t, m, "ctrl+s")
	if len(m.ws.leaves()) != 2 || m.ws.root.dir != 'h' || m.floatExp {
		t.Fatal("⌃s opens in a horizontal split")
	}
	// the explorer keymap works inside the overlay: create a note
	m = keys(t, m, "-", "a")
	m = typeText(t, m, "fresh")
	m = keys(t, m, "enter")
	if _, err := os.Stat(filepath.Join(dir, "fresh.md")); err != nil || !m.floatExp {
		t.Fatalf("a inside the overlay: %v (open %v)", err, m.floatExp)
	}
	// q in the overlay closes it (it does not quit)
	m = keys(t, m, "q")
	if m.floatExp || m.viewMode != ViewFullNote {
		t.Fatal("q closes the overlay")
	}
}

func TestSmallTerminalUsesFullSheet(t *testing.T) {
	m := newTreeModel(t, notesVault(t))
	m.width, m.height = 99, 40
	if r := m.overlayRect(); !m.isSheet() || r != (rect{0, 0, 99, m.contentHeight()}) {
		t.Fatalf("narrow: %+v", r)
	}
	m.width, m.height = 140, 29
	if !m.isSheet() {
		t.Fatal("short terminals get the sheet too")
	}
	m.width, m.height = 140, 40
	if r := m.overlayRect(); m.isSheet() || r.w != 112 || r.x != 14 {
		t.Fatalf("centred 80%%: %+v", r)
	}
}

func TestBooksLevelInOverlay(t *testing.T) {
	m, a, _ := newBooksModel(t)
	m = settle(m, m.loadItems)
	if !m.floatExp || m.currentDir != a {
		t.Fatal("fresh book: explorer open at the root")
	}
	// h at the book root climbs to the books level
	m = keys(t, m, "h")
	if m.bk == nil || m.modeName() != "BOOKS" {
		t.Fatal("h at the root opens the books level")
	}
	// ⏎ on the active book goes back to its files
	m = keys(t, m, "enter")
	if m.bk != nil || !m.floatExp {
		t.Fatal("⏎ on the active book returns to the files")
	}
	m = keys(t, m, "g", "b", "esc")
	if m.floatExp {
		t.Fatal("Esc closes the overlay from the books level")
	}
}

// --- last opened note per book ----------------------------------------

func TestLastNoteRestoredPerBookFromConfigDir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := notesVault(t)
	before, _ := os.ReadDir(dir)

	m := drive(NewModelWithInitialNote(dir, ""))
	if m.viewMode != ViewHome || !m.floatExp {
		t.Fatal("no last note: empty home with the explorer open")
	}
	m = moveTo(t, m, "Two")
	m = keys(t, m, "enter")

	f := filepath.Join(home, ".config", "lumi", "state", "last-notes.json")
	data, err := os.ReadFile(f)
	if err != nil || !strings.Contains(string(data), `"two.md"`) {
		t.Fatalf("record not in the config dir: %v %s", err, data)
	}
	if info, _ := os.Stat(f); info.Mode().Perm() != 0o600 {
		t.Fatalf("record mode %v", info.Mode().Perm())
	}
	after, _ := os.ReadDir(dir)
	if len(after) != len(before) {
		t.Fatal("nothing may be written into the book folder")
	}

	re := drive(NewModelWithInitialNote(dir, ""))
	if re.viewMode != ViewFullNote || re.floatExp || re.ws.focus.buf.note.Title != "Two" {
		t.Fatalf("reopen: view %v explorer %v", re.viewMode, re.floatExp)
	}
	// another book keeps its own record
	other := notesVault(t)
	if o := drive(NewModelWithInitialNote(other, "")); o.viewMode != ViewHome {
		t.Fatal("other book has no last note")
	}
	// a deleted last note falls back to the home
	os.Remove(filepath.Join(dir, "two.md"))
	gone := drive(NewModelWithInitialNote(dir, ""))
	if gone.viewMode != ViewHome || !gone.floatExp {
		t.Fatal("missing last note: empty home")
	}
}

// --- motion -----------------------------------------------------------

func motionModel(t *testing.T) Model {
	t.Helper()
	t.Setenv("REDUCE_MOTION", "")
	t.Setenv("NO_MOTION", "")
	m := newTreeModel(t, notesVault(t))
	m.cfg.ReduceMotion = false
	m.mo.setReduced(false)
	m.mo.settle()
	m.mo.ticking = false
	return m
}

// runFrames feeds frame messages while a frame is scheduled.
func runFrames(t *testing.T, m Model, cmd tea.Cmd) (Model, int) {
	t.Helper()
	n := 0
	for m.mo.ticking {
		if n > 600 {
			t.Fatal("animation never settles")
		}
		next, _ := m.Update(frameMsg{})
		m = next.(Model)
		n++
	}
	return m, n
}

func TestMotionTicksOnlyWhileAnimating(t *testing.T) {
	m := motionModel(t)
	if m.mo.reduced {
		t.Fatal("motion should be on")
	}
	// idle: a key that moves nothing schedules no frame
	next, _ := m.Update(keyMsg("j"))
	m = next.(Model)
	if m.mo.ticking || m.mo.busy() {
		t.Fatal("idle keys must not tick")
	}
	// closing the overlay animates; keys still apply at once
	frames := m.mo.frames
	next, cmd := m.Update(keyMsg("esc"))
	m = next.(Model)
	if m.floatExp || !m.mo.ticking || cmd == nil {
		t.Fatal("close: state changes now, a frame is scheduled")
	}
	if !strings.Contains(stripAnsi(m.View()), "╭") {
		t.Fatal("the overlay is still shrinking on screen")
	}
	m, n := runFrames(t, m, cmd)
	if n == 0 || n > 60 || m.mo.busy() {
		t.Fatalf("settled after %d frames, busy %v", n, m.mo.busy())
	}
	if strings.Contains(stripAnsi(m.View()), "╭") {
		t.Fatal("closed overlay gone once settled")
	}
	if m.mo.frames-frames != n {
		t.Fatalf("one tick per frame: %d ticks for %d frames", m.mo.frames-frames, n)
	}
	// settled: the last frame scheduled nothing
	if m.mo.frame() != nil {
		t.Fatal("no tick when idle")
	}
	// opening: grows, staggers the rows, mode pill morphs
	next, _ = m.Update(keyMsg("-"))
	m = next.(Model)
	if a, _ := m.mo.get(animOverlay); !a.active || a.pos != 0 {
		t.Fatalf("overlay starts at 0: %+v", a)
	}
	if !m.mo.a[animPill].active || m.mo.pillFrom != "HOME" {
		t.Fatalf("pill morph from %q", m.mo.pillFrom)
	}
	next, _ = m.Update(frameMsg{})
	m = next.(Model)
	if a, _ := m.mo.get(animOverlay); a.pos <= 0 || a.pos >= 1 {
		t.Fatalf("mid-flight overlay %v", a.pos)
	}
	_ = m.View() // renders mid-animation without panicking
	m, _ = runFrames(t, m, nil)
	if a, _ := m.mo.get(animOverlay); a.pos != 1 || m.mo.ticking {
		t.Fatal("overlay settles open, ticker stops")
	}
	// toasts slide in, splits slide open
	m.showToast("hi", ToastInfo, 0)
	if !m.mo.a[animToast].active {
		t.Fatal("toast slides")
	}
	m, _ = runFrames(t, m, m.mo.kick())
	m = moveTo(t, m, "One")
	m = keys(t, m, "enter")
	m.mo.settle()
	m.mo.ticking = false
	next, _ = m.Update(keyMsg("ctrl+w"))
	next, _ = next.(Model).Update(keyMsg("v"))
	m = next.(Model)
	if m.mo.splitPane == nil || !m.mo.a[animSplit].active {
		t.Fatal("the new pane slides open")
	}
	if r := m.layoutRects()[m.mo.splitPane]; r.w >= m.width/2-1 {
		t.Fatalf("the new pane starts narrow: %d", r.w)
	}
	m, _ = runFrames(t, m, nil)
	if m.mo.splitPane != nil {
		t.Fatal("slide done")
	}
}

func TestBreathingTicksOnlyWhileSyncing(t *testing.T) {
	m := motionModel(t)
	m.mo.syncing = true
	if m.mo.kick() == nil {
		t.Fatal("syncing breathes")
	}
	m.mo.frame()
	if !m.mo.ticking || m.mo.breatheGlyph() == "" {
		t.Fatal("keeps breathing while syncing")
	}
	m.mo.syncing = false
	if m.mo.frame() != nil || m.mo.ticking {
		t.Fatal("stops when the sync ends")
	}
}

func TestReducedMotionNeverTicks(t *testing.T) {
	for _, env := range []string{"REDUCE_MOTION", "NO_MOTION"} {
		t.Run(env, func(t *testing.T) {
			t.Setenv("REDUCE_MOTION", "")
			t.Setenv("NO_MOTION", "")
			t.Setenv(env, "1")
			m := newTreeModel(t, notesVault(t))
			if !m.mo.reduced {
				t.Fatal("env reduces motion")
			}
			for _, k := range []string{"esc", "-", "j", "esc"} {
				next, _ := m.Update(keyMsg(k))
				m = next.(Model)
				if m.mo.ticking || m.mo.frames != 0 {
					t.Fatalf("%s scheduled a frame", k)
				}
			}
			m.showToast("x", ToastInfo, 0)
			m.mo.syncing = true
			if m.mo.kick() != nil {
				t.Fatal("reduced: no ticks, even while syncing")
			}
		})
	}
	// the Settings switch alone does it too
	t.Setenv("REDUCE_MOTION", "")
	t.Setenv("NO_MOTION", "")
	m := newTreeModel(t, notesVault(t))
	m.mo.setReduced(true)
	m.mo.ticking = false
	next, _ := m.Update(keyMsg("esc"))
	if m = next.(Model); m.mo.ticking {
		t.Fatal("setting: no ticks")
	}
}
