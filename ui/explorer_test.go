package ui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/vinizap/lumi/tui-client/filesystem"
)

// keyMsg builds the KeyMsg Bubbletea would deliver for a key name.
func keyMsg(k string) tea.KeyMsg {
	special := map[string]tea.KeyType{
		"enter": tea.KeyEnter, "esc": tea.KeyEsc, "backspace": tea.KeyBackspace,
		"ctrl+h": tea.KeyCtrlH, "ctrl+l": tea.KeyCtrlL, "ctrl+j": tea.KeyCtrlJ, "ctrl+k": tea.KeyCtrlK,
		"ctrl+d": tea.KeyCtrlD, "ctrl+u": tea.KeyCtrlU, "ctrl+c": tea.KeyCtrlC, "up": tea.KeyUp, "down": tea.KeyDown,
		"tab": tea.KeyTab, " ": tea.KeySpace,
	}
	if t, ok := special[k]; ok {
		return tea.KeyMsg{Type: t}
	}
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(k)}
}

// keys presses each key and settles the resulting commands, skipping
// timers (toasts) and terminal control messages so tests stay fast.
func keys(t *testing.T, m Model, ks ...string) Model {
	t.Helper()
	for _, k := range ks {
		next, cmd := m.Update(keyMsg(k))
		m = settle(next.(Model), cmd)
	}
	return m
}

func settle(m Model, cmd tea.Cmd) Model {
	for _, msg := range drainFast(cmd) {
		next, more := m.Update(msg)
		m = settle(next.(Model), more)
	}
	return m
}

// drainFast runs cmds that complete immediately (loadItems and friends);
// tea.Tick-based ones (toasts, anim) would block, so they run with a
// short deadline and are dropped when they do not answer.
func drainFast(cmd tea.Cmd) []tea.Msg {
	if cmd == nil {
		return nil
	}
	ch := make(chan tea.Msg, 1)
	go func() { ch <- cmd() }()
	var msg tea.Msg
	select {
	case msg = <-ch:
	case <-time.After(30 * time.Millisecond):
		return nil
	}
	switch v := msg.(type) {
	case nil:
		return nil
	case tea.BatchMsg:
		var out []tea.Msg
		for _, sub := range v {
			out = append(out, drainFast(sub)...)
		}
		return out
	case toastDismissMsg, animTickMsg, tea.QuitMsg:
		return nil
	}
	if strings.HasPrefix(typeName(msg), "tea.") {
		return nil // enable/disable mouse, exec, …
	}
	return []tea.Msg{msg}
}

func typeName(v any) string { return strings.TrimPrefix(fmt.Sprintf("%T", v), "*") }

func explorer(t *testing.T) (Model, string) {
	t.Helper()
	root := newYaziVault(t)
	m := newTreeModel(t, root)
	return m, root
}

func names(m Model) []string {
	var out []string
	for _, it := range m.items {
		out = append(out, it.Name)
	}
	return out
}

func TestCreateNoteFolderAndNestedPath(t *testing.T) {
	m, root := explorer(t)
	m = keys(t, m, "a", "M", "i", "n", "h", "a", " ", "n", "o", "t", "a", "enter")
	if _, err := os.Stat(filepath.Join(root, "minha-nota.md")); err != nil {
		t.Fatalf("note not created: %v (items %v)", err, names(m))
	}
	if it, _ := m.currentItem(); it.Name != "Minha nota" {
		t.Fatalf("cursor on %q, want the new note", it.Name)
	}

	m = keys(t, m, "a")
	for _, r := range "deep/er/Ação" {
		m = keys(t, m, string(r))
	}
	m = keys(t, m, "enter")
	if m.currentDir != filepath.Join(root, "deep", "er") {
		t.Fatalf("currentDir = %s, want the new folder", m.currentDir)
	}
	if it, _ := m.currentItem(); it.Note == nil || it.Note.Title != "Ação" {
		t.Fatalf("selected %+v, want the note with an accented title", it)
	}

	m = keys(t, m, "~", "a", "b", "o", "x", "/", "enter")
	if fi, err := os.Stat(filepath.Join(root, "box")); err != nil || !fi.IsDir() {
		t.Fatalf("folder not created: %v", err)
	}
}

func TestCreateRejectsDotAndEscapes(t *testing.T) {
	m, root := explorer(t)
	m = keys(t, m, "a", ".", "l", "u", "m", "i", "/", "x", "enter")
	if _, err := os.Stat(filepath.Join(root, ".lumi", "x.md")); err == nil {
		t.Fatal("created a note under .lumi")
	}
	// `..` is clamped at the root: never outside the book.
	m = keys(t, m, "a", ".", ".", "/", ".", ".", "/", "o", "u", "t", "enter")
	if _, err := os.Stat(filepath.Join(filepath.Dir(root), "out.md")); err == nil {
		t.Fatal("created a note outside the book")
	}
	if _, err := os.Stat(filepath.Join(root, "out.md")); err != nil {
		t.Fatalf("clamped create missing: %v", err)
	}
}

func TestRenameRetitleAndMove(t *testing.T) {
	m, root := explorer(t)
	m = moveTo(t, m, "One")
	m = keys(t, m, "r", "ctrl+u")
	for _, r := range "beta/Uno" {
		m = keys(t, m, string(r))
	}
	m = keys(t, m, "enter")
	// A plain-markdown note's title is its file name, so the retitle
	// renames the file too (frontmatter notes keep it).
	n, err := filesystem.ReadNote(filepath.Join(root, "beta", "uno.md"))
	if err != nil || n.Title != "Uno" {
		t.Fatalf("moved note = %+v, err %v", n, err)
	}
	if m.currentDir != filepath.Join(root, "beta") {
		t.Fatalf("explorer did not follow the move: %s", m.currentDir)
	}
}

func TestRenameFolder(t *testing.T) {
	m, root := explorer(t)
	m = moveTo(t, m, "beta")
	m = keys(t, m, "r", "ctrl+u", "g", "a", "m", "m", "a", "enter")
	if _, err := os.Stat(filepath.Join(root, "gamma", "b1.md")); err != nil {
		t.Fatalf("folder not renamed: %v", err)
	}
}

func TestDeleteInlineConfirm(t *testing.T) {
	m, root := explorer(t)
	m = moveTo(t, m, "Two")
	m = keys(t, m, "d")
	if !m.showConfirm || m.legendContext() != CtxConfirm {
		t.Fatal("d did not ask")
	}
	if row := stripAnsi(m.renderLegendRow()); !strings.Contains(row, "delete “Two”?") {
		t.Fatalf("legend row = %q", row)
	}
	m = keys(t, m, "n")
	if _, err := os.Stat(filepath.Join(root, "two.md")); err != nil {
		t.Fatal("n deleted the note")
	}
	m = keys(t, m, "d", "y")
	if _, err := os.Stat(filepath.Join(root, "two.md")); err == nil {
		t.Fatal("y did not delete")
	}
}

func TestYankPasteMakesCopyWithNewID(t *testing.T) {
	m, root := explorer(t)
	m = moveTo(t, m, "One")
	m = keys(t, m, "y", "g", "g")
	m = moveTo(t, m, "beta")
	m = keys(t, m, "l", "p")
	cp, err := filesystem.ReadNote(filepath.Join(root, "beta", "one-copy.md"))
	if err != nil || cp.Title != "One copy" || cp.ID == "one" || !strings.Contains(cp.Content, "body.") {
		t.Fatalf("copy = %+v, err %v", cp, err)
	}
	m = keys(t, m, "p")
	if _, err := os.Stat(filepath.Join(root, "beta", "one-copy-2.md")); err != nil {
		t.Fatalf("second paste: %v (items %v)", err, names(m))
	}
}

func TestHiddenToggle(t *testing.T) {
	m, root := explorer(t)
	mustMkdir(t, filepath.Join(root, ".lumi"))
	m = keys(t, m, "R")
	for _, n := range names(m) {
		if n == ".lumi" {
			t.Fatal(".lumi listed without `.`")
		}
	}
	m = keys(t, m, ".")
	if names(m)[0] != ".lumi" {
		t.Fatalf("items = %v, want .lumi first", names(m))
	}
}

func TestFilterInPlaceAndNextMatch(t *testing.T) {
	m, _ := explorer(t)
	m = keys(t, m, "/", "o")
	if !m.filterTyping || m.legendContext() != CtxFilter {
		t.Fatal("filter prompt not open")
	}
	got := strings.Join(names(m), ",")
	if got != "One,Two" { // fuzzy "o": One, twO (alpha/beta have no o)
		t.Fatalf("filtered = %s", got)
	}
	m = keys(t, m, "enter")
	if m.filterTyping || m.filter != "o" {
		t.Fatal("enter should keep the filter")
	}
	start := m.cursor
	m = keys(t, m, "n")
	if m.cursor == start {
		t.Fatal("n did not move to the next match")
	}
	m = keys(t, m, "N")
	if m.cursor != start {
		t.Fatal("N did not come back")
	}
	m = keys(t, m, "esc")
	if m.filter != "" || len(m.items) != 4 {
		t.Fatalf("esc did not clear: %v", names(m))
	}
}

func TestCountsAndHalfPage(t *testing.T) {
	m, _ := explorer(t)
	m = keys(t, m, "3", "j")
	if m.cursor != 3 {
		t.Fatalf("3j → %d", m.cursor)
	}
	m = keys(t, m, "2", "k")
	if m.cursor != 1 {
		t.Fatalf("2k → %d", m.cursor)
	}
	m = keys(t, m, "2")
	if m.legendContext() != CtxExplorer || legendFor(CtxExplorer, m.pendingInput())[0].Keys != "2j" {
		t.Fatalf("legend for a pending count = %+v", legendFor(CtxExplorer, m.pendingInput()))
	}
	m = keys(t, m, "esc")
	m = keys(t, m, "ctrl+d")
	if m.cursor != 3 {
		t.Fatalf("ctrl+d → %d (4 items, clamps)", m.cursor)
	}
	m = keys(t, m, "g", "g")
	if m.cursor != 0 {
		t.Fatal("gg")
	}
}

func TestPaneFocusAndPreviewScroll(t *testing.T) {
	m, root := explorer(t)
	m = keys(t, m, "ctrl+l")
	if m.pane != PanePreview || m.legendContext() != CtxPreview {
		t.Fatalf("pane = %v", m.pane)
	}
	m = keys(t, m, "j", "j")
	if m.previewScroll != 2 {
		t.Fatalf("preview scroll = %d", m.previewScroll)
	}
	m = keys(t, m, "ctrl+h")
	if m.pane != PaneList {
		t.Fatalf("ctrl+h → %v", m.pane)
	}
	m = keys(t, m, "ctrl+h") // at the root: no parent pane
	if m.pane != PaneList {
		t.Fatalf("parent focus at root: %v", m.pane)
	}
	m = keys(t, m, "l") // into alpha/
	m = keys(t, m, "ctrl+h")
	if m.pane != PaneParent {
		t.Fatalf("pane = %v, want parent", m.pane)
	}
	if items := m.parentItems(); items[m.parentCursor].Path != filepath.Join(root, "alpha") {
		t.Fatal("parent cursor not on the current folder")
	}
	m = keys(t, m, "j", "l") // beta/
	if m.currentDir != filepath.Join(root, "beta") || m.pane != PaneList {
		t.Fatalf("parent l → %s pane %v", m.currentDir, m.pane)
	}
	m.paneNav = false
	m = keys(t, m, "ctrl+l")
	if m.pane != PaneList {
		t.Fatal("pane nav disabled but focus moved")
	}
}

func TestHistoryRootAndParent(t *testing.T) {
	m, root := explorer(t)
	m = keys(t, m, "l", "l") // alpha/nested
	if m.currentDir != filepath.Join(root, "alpha", "nested") {
		t.Fatalf("dir = %s", m.currentDir)
	}
	m = keys(t, m, "h")
	if it, _ := m.currentItem(); it.Name != "nested" {
		t.Fatalf("h should land on the folder we left, got %q", it.Name)
	}
	m = keys(t, m, "~")
	if m.currentDir != root {
		t.Fatal("~")
	}
	m = keys(t, m, "H")
	if m.currentDir != filepath.Join(root, "alpha") {
		t.Fatalf("H → %s", m.currentDir)
	}
	m = keys(t, m, "L")
	if m.currentDir != root {
		t.Fatalf("L → %s", m.currentDir)
	}
}

func TestPaletteRunsCommandsAndOpensNotes(t *testing.T) {
	m, root := explorer(t)
	m = keys(t, m, ":")
	if !m.showPalette || m.paletteQuery != ":" {
		t.Fatal(": should open the command palette")
	}
	for _, e := range m.paletteResults() {
		if e.kind != palCommand {
			t.Fatalf(": lists %v", e)
		}
	}
	m = keys(t, m, "h", "i", "d", "d", "e", "n", "enter")
	if !m.showHidden {
		t.Fatal("palette `hidden` did not run")
	}
	m = keys(t, m, " ", "d", "e", "e", "p")
	res := m.paletteResults()
	if len(res) == 0 || res[0].kind != palNote || res[0].label != "Deep" {
		t.Fatalf("results = %+v", res)
	}
	m = keys(t, m, "enter")
	if m.viewMode != ViewFullNote || m.fullNote.Path != filepath.Join(root, "alpha", "nested", "deep.md") {
		t.Fatalf("view %v note %+v", m.viewMode, m.fullNote)
	}
	m = keys(t, m, "q")
	if m.viewMode != ViewTree || m.currentDir != filepath.Join(root, "alpha", "nested") {
		t.Fatalf("q from reader → %v %s", m.viewMode, m.currentDir)
	}
}

func TestHelpAndSettingsOpenFromAnywhere(t *testing.T) {
	m, _ := explorer(t)
	m = keys(t, m, "?")
	if !m.showHelp || m.legendContext() != CtxHelp {
		t.Fatal("? help")
	}
	m = keys(t, m, "esc", ",")
	if m.viewMode != ViewConfig {
		t.Fatal(", settings")
	}
	m = keys(t, m, ",")
	if m.viewMode != ViewTree {
		t.Fatal(", again should go back")
	}
}

func TestSettingsTogglesPersistToTempHome(t *testing.T) {
	t.Setenv("HOME", t.TempDir()) // own config: other tests keep the defaults
	m, _ := explorer(t)
	m = keys(t, m, ",")
	idx := -1
	for i, it := range m.configItems {
		if it.Key == "show_legend" {
			idx = i
		}
	}
	m.configCursor = idx
	m = keys(t, m, "l")
	if m.legendOn {
		t.Fatal("legend still on")
	}
	home, _ := os.UserHomeDir()
	data, err := os.ReadFile(filepath.Join(home, ".config", "lumi", "config.yaml"))
	if err != nil || !strings.Contains(string(data), "show_legend: false") {
		t.Fatalf("config not saved under the temp HOME: %v\n%s", err, data)
	}
	m = keys(t, m, "esc")
	if m.legendVisible() {
		t.Fatal("legend row still visible")
	}
	m = keys(t, m, "a")
	if !m.legendVisible() {
		t.Fatal("the prompt needs the legend row even when the legend is off")
	}
}

func TestReaderKeys(t *testing.T) {
	m, _ := explorer(t)
	m = moveTo(t, m, "One")
	m = keys(t, m, "enter")
	if m.legendContext() != CtxReader {
		t.Fatal("reader context")
	}
	m = keys(t, m, "j", "g", "g")
	if m.lineCursor != 0 {
		t.Fatal("gg in reader")
	}
	m = keys(t, m, "V")
	if m.legendContext() != CtxVisual {
		t.Fatal("visual context")
	}
	m = keys(t, m, "esc", " ")
	if !m.showPalette {
		t.Fatal("space opens the palette in the reader")
	}
	m = keys(t, m, "esc", "esc")
	if m.viewMode != ViewTree {
		t.Fatal("esc back to explorer")
	}
}

func TestBooksBracketsSwitch(t *testing.T) {
	m, _, b := newBooksModel(t)
	m, _ = pressKeys(m, "]")
	if m.SwitchTarget() != b {
		t.Fatalf("] switched to %q, want %q", m.SwitchTarget(), b)
	}
}

func TestStatusLineShowsBookSegment(t *testing.T) {
	m, _, _ := newBooksModel(t)
	m.viewMode = ViewTree
	line := stripAnsi(m.renderStatusLine())
	if !strings.Contains(line, "EXPLORE") || !strings.Contains(line, "Alpha") {
		t.Fatalf("status = %q", line)
	}
}

func TestMouseClickDoubleClickWheel(t *testing.T) {
	m, root := explorer(t)
	clock := time.Date(2026, 11, 23, 10, 0, 0, 0, time.UTC)
	nowFn = func() time.Time { return clock }
	defer func() { nowFn = time.Now }()
	left, _, _, sep := treeColumnWidths(m.width, false)
	x := left + sep + 3
	click := func(m Model, y int) Model {
		next, cmd := m.Update(tea.MouseMsg{X: x, Y: y, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
		return settle(next.(Model), cmd)
	}
	m = click(m, treeTop+1) // beta/
	if it, _ := m.currentItem(); it.Name != "beta" {
		t.Fatalf("click selected %q", it.Name)
	}
	clock = clock.Add(100 * time.Millisecond)
	m = click(m, treeTop+1)
	if m.currentDir != filepath.Join(root, "beta") {
		t.Fatalf("double-click did not open: %s", m.currentDir)
	}
	m = keys(t, m, "h")
	next, _ := m.Update(tea.MouseMsg{X: x, Y: treeTop, Button: tea.MouseButtonWheelDown, Action: tea.MouseActionPress})
	m = next.(Model)
	if m.cursor != 2 {
		t.Fatalf("wheel → cursor %d", m.cursor)
	}
	m.mouseOn = false
	next, _ = m.Update(tea.MouseMsg{X: x, Y: treeTop, Button: tea.MouseButtonWheelDown, Action: tea.MouseActionPress})
	if next.(Model).cursor != 2 {
		t.Fatal("mouse off but wheel moved")
	}
}

func TestEditLineIsRuneSafe(t *testing.T) {
	v, _ := editLine("", tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("ação")})
	v, _ = editLine(v, tea.KeyMsg{Type: tea.KeyBackspace})
	if v != "açã" {
		t.Fatalf("v = %q", v)
	}
	v, _ = editLine("a/b c", tea.KeyMsg{Type: tea.KeyCtrlW})
	if v != "a/b " {
		t.Fatalf("ctrl+w = %q", v)
	}
	if _, ok := editLine("x", tea.KeyMsg{Type: tea.KeyEnter}); ok {
		t.Fatal("enter is not an edit")
	}
}
