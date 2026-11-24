package ui

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/vinizap/lumi/tui-client/account"
	"github.com/vinizap/lumi/tui-client/vim"
)

// typeText presses each rune as a key.
func typeText(t *testing.T, m Model, s string) Model {
	t.Helper()
	for _, r := range s {
		m = keys(t, m, string(r))
	}
	return m
}

func notesVault(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "one.md"), "---\nid: one\ntitle: One\nkanban-plugin: none\ncustom: kept\n---\n\n# one\n\nSee [[two]] and [the site](https://example.com).\n")
	mustWrite(t, filepath.Join(dir, "two.md"), "---\nid: two\ntitle: Two\nupdated_at: 2026-11-20T10:00:00Z\n---\n\n# two\n\nsecond note body\n")
	mustWrite(t, filepath.Join(dir, "board.md"), "---\ntitle: Board\nkanban-plugin: board\n---\n\n## Todo\n\n- [ ] alpha\n- [ ] beta\n\n## Done\n\n- [x] gamma\n\n%% kanban:settings\n```\n{}\n```\n%%\n")
	mustWrite(t, filepath.Join(dir, "map.canvas"), `{"nodes":[{"id":"g","type":"group","x":0,"y":0,"width":500,"height":500,"label":"Plano"},{"id":"f","type":"file","x":10,"y":10,"width":100,"height":50,"file":"two.md"},{"id":"t","type":"text","x":10,"y":100,"width":100,"height":50,"text":"ideia"}],"edges":[{"id":"e","fromNode":"t","toNode":"f","label":"usa"}]}`)
	return dir
}

func openNamed(t *testing.T, m Model, name string) Model {
	t.Helper()
	m = moveTo(t, m, name)
	return keys(t, m, "enter")
}

func TestBuiltInEditorSavesThroughWriteBody(t *testing.T) {
	dir := notesVault(t)
	m := newTreeModel(t, dir)
	m = openNamed(t, m, "One")
	if m.viewMode != ViewFullNote || m.ws.focus.view != viewReader {
		t.Fatalf("opened in %v / %v", m.viewMode, m.ws.focus.view)
	}
	m = keys(t, m, "i", "G", "o")
	m = typeText(t, m, "ação nova")
	m = keys(t, m, "esc")
	if m.ws.focus.buf.eng.Mode() != vim.ModeNormal || !m.ws.focus.buf.dirty() {
		t.Fatal("edit not applied")
	}
	m = keys(t, m, ":", "w", "enter")
	data, _ := os.ReadFile(filepath.Join(dir, "one.md"))
	got := string(data)
	if !strings.HasPrefix(got, "---\nid: one\ntitle: One\nkanban-plugin: none\ncustom: kept\n---\n\n") || !strings.HasSuffix(got, "ação nova\n") {
		t.Fatalf("file after :w:\n%s", got)
	}
	if m.ws.focus.buf.dirty() {
		t.Fatal("still dirty after :w")
	}
	// autosave: an edit schedules a save; the matching tick writes it
	m = keys(t, m, "d", "d")
	b := m.ws.focus.buf
	next, _ := m.Update(autosaveMsg{path: b.path, seq: b.editSeq - 1})
	m = next.(Model)
	if !b.dirty() {
		t.Fatal("a stale autosave tick must not save")
	}
	next, _ = m.Update(autosaveMsg{path: b.path, seq: b.editSeq})
	m = next.(Model)
	data, _ = os.ReadFile(filepath.Join(dir, "one.md"))
	if b.dirty() || strings.Contains(string(data), "ação nova") {
		t.Fatalf("autosave did not write:\n%s", data)
	}
	// :q with one pane goes back to the reader
	m = keys(t, m, ":", "q", "enter")
	if m.ws.focus.view != viewReader {
		t.Fatal(":q → reader")
	}
}

func TestDefaultEditorSettingAndOtherEditorKey(t *testing.T) {
	dir := notesVault(t)
	m := newTreeModel(t, dir)
	m.cfg.DefaultEditor = "external"
	m = openNamed(t, m, "One")
	next, cmd := m.Update(keyMsg("i"))
	m = next.(Model)
	if m.ws.focus.view != viewReader || cmd == nil {
		t.Fatal("i with an external default must hand off to $EDITOR, not switch view")
	}
	m = keys(t, m, "E")
	if m.ws.focus.view != viewSource {
		t.Fatal("E opens the built-in editor when the default is external")
	}
	m.cfg.DefaultEditor = "internal"
	m = keys(t, m, ":", "q", "enter")
	next, cmd = m.Update(keyMsg("E"))
	m = next.(Model)
	if m.ws.focus.view != viewReader || cmd == nil {
		t.Fatal("E opens $EDITOR when the default is internal")
	}
	// :edit! from the built-in editor also hands off
	m = keys(t, m, "i")
	_, cmd = m.Update(keyMsg(":"))
	m2 := m
	for _, k := range []string{":", "e", "d", "i", "t", "!"} {
		next, _ = m2.Update(keyMsg(k))
		m2 = next.(Model)
	}
	_, cmd = m2.Update(keyMsg("enter"))
	if cmd == nil {
		t.Fatal(":edit! must hand off to $EDITOR")
	}
}

func TestSplitsAndPaneNavigation(t *testing.T) {
	dir := notesVault(t)
	m := newTreeModel(t, dir)
	m.width, m.height = 120, 30
	m = openNamed(t, m, "One")
	m = keys(t, m, "ctrl+w", "v")
	if n := len(m.ws.leaves()); n != 2 || m.ws.root.dir != 'v' {
		t.Fatalf("⌃w v: %d panes", n)
	}
	ls := m.ws.leaves()
	if ls[0].buf != ls[1].buf {
		t.Fatal("two panes on one note share one buffer")
	}
	m = keys(t, m, "ctrl+w", "s")
	if n := len(m.ws.leaves()); n != 3 {
		t.Fatalf("⌃w s: %d panes", n)
	}
	right := m.ws.focus
	m = keys(t, m, "ctrl+h")
	if m.ws.focus != ls[0] {
		t.Fatal("⌃h → left pane")
	}
	m = keys(t, m, "ctrl+l")
	if m.ws.focus == ls[0] {
		t.Fatal("⌃l → right column")
	}
	m = keys(t, m, "ctrl+w", "p")
	m = keys(t, m, "ctrl+w", "q")
	if n := len(m.ws.leaves()); n != 2 {
		t.Fatalf("⌃w q: %d panes", n)
	}
	_ = right
	m = keys(t, m, "ctrl+w", "o")
	if n := len(m.ws.leaves()); n != 1 {
		t.Fatalf("⌃w o: %d panes", n)
	}
	// :vsplit <note> from the editor
	m = keys(t, m, "i")
	m = typeText(t, m, ":vs two")
	m = keys(t, m, "enter")
	if n := len(m.ws.leaves()); n != 2 || m.ws.focus.buf.note.Title != "Two" {
		t.Fatalf(":vs two → %d panes, focus %q", n, m.ws.focus.buf.note.Title)
	}
	// the last pane closes the note view
	m = keys(t, m, "ctrl+w", "q", "ctrl+w", "q")
	if m.viewMode != ViewHome || !m.floatExp {
		t.Fatal("closing the last pane leaves the home with the explorer open")
	}
}

func TestSeparateCursorsPerPane(t *testing.T) {
	dir := notesVault(t)
	m := newTreeModel(t, dir)
	m = openNamed(t, m, "One")
	m = keys(t, m, "i", "G")
	m = keys(t, m, "ctrl+w", "v", "g", "g")
	if m.ws.focus.buf.eng.Cursor().Line != 0 {
		t.Fatal("gg in the new pane")
	}
	m = keys(t, m, "ctrl+h")
	if m.ws.focus.buf.eng.Cursor().Line == 0 {
		t.Fatal("the first pane kept its own cursor")
	}
}

func TestReaderLinksHoverAndSplitFollow(t *testing.T) {
	dir := notesVault(t)
	m := newTreeModel(t, dir)
	m.width, m.height = 120, 30
	m = openNamed(t, m, "One")
	m = keys(t, m, "tab")
	if m.ws.focus.link != 0 {
		t.Fatal("Tab focuses the first link")
	}
	m = keys(t, m, "K")
	if m.hover == nil || m.hover.note == nil || m.hover.note.Title != "Two" {
		t.Fatalf("K card %+v", m.hover)
	}
	if v := stripAnsi(m.View()); !strings.Contains(v, "second note body") {
		t.Fatalf("hover card body missing:\n%s", v)
	}
	m = keys(t, m, "ctrl+w", "f")
	if m.hover != nil || len(m.ws.leaves()) != 2 || m.ws.focus.buf.note.Title != "Two" {
		t.Fatal("⌃w f in the card opens a split")
	}
	m = keys(t, m, "ctrl+w", "h", "tab")
	openedURLs = nil
	m = keys(t, m, "enter")
	if len(openedURLs) != 1 || openedURLs[0] != "https://example.com" {
		t.Fatalf("external link → %v", openedURLs)
	}
	m = keys(t, m, "shift+tab", "enter")
	if m.ws.focus.buf.note.Title != "Two" {
		t.Fatal("⏎ follows the note link in the pane")
	}
}

func TestEditorGfAndEnterOnLink(t *testing.T) {
	dir := notesVault(t)
	m := newTreeModel(t, dir)
	m = openNamed(t, m, "One")
	m = keys(t, m, "i", "G")
	m = keys(t, m, "f", "[")
	m = keys(t, m, "g", "f")
	if m.ws.focus.buf.note.Title != "Two" {
		t.Fatalf("gf opened %q", m.ws.focus.buf.note.Title)
	}
}

func TestFloatingExplorer(t *testing.T) {
	dir := notesVault(t)
	m := newTreeModel(t, dir)
	m.width, m.height = 120, 30
	m = openNamed(t, m, "One")
	m = keys(t, m, "-")
	if !m.floatExp || m.legendContext() != CtxFloat {
		t.Fatal("- opens the floating explorer")
	}
	if v := stripAnsi(m.View()); !strings.Contains(v, "╭") {
		t.Fatal("floating explorer is a bordered overlay")
	}
	m = moveTo(t, m, "Two")
	m = keys(t, m, "ctrl+v")
	if m.floatExp || len(m.ws.leaves()) != 2 || m.ws.focus.buf.note.Title != "Two" {
		t.Fatal("⌃v opens the note in a vertical split")
	}
	m = keys(t, m, "-", "esc")
	if m.floatExp || m.viewMode != ViewFullNote {
		t.Fatal("Esc closes the overlay")
	}
	m = keys(t, m, `\`, "e")
	if !m.floatExp {
		t.Fatal("<leader>e opens it too")
	}
	m = keys(t, m, "g", "g")
	m = moveTo(t, m, "One")
	m = keys(t, m, "enter")
	if m.floatExp || m.ws.focus.buf.note.Title != "One" || len(m.ws.leaves()) != 2 {
		t.Fatal("⏎ opens in the current pane")
	}
}

func TestKanbanBoardKeys(t *testing.T) {
	dir := notesVault(t)
	m := newTreeModel(t, dir)
	m.width, m.height = 120, 30
	m = openNamed(t, m, "Board")
	if m.ws.focus.view != viewBoard || m.legendContext() != CtxBoardRead {
		t.Fatalf("kanban opens as a board in Reading: %v", m.ws.focus.view)
	}
	read := func() string {
		data, _ := os.ReadFile(filepath.Join(dir, "board.md"))
		return string(data)
	}
	m = keys(t, m, "i")
	if m.legendContext() != CtxBoard || m.modeName() != "EDIT" {
		t.Fatal("i → Edit")
	}
	m = keys(t, m, "x")
	if !strings.Contains(read(), "- [x] alpha") {
		t.Fatal("x toggles")
	}
	m = keys(t, m, "L")
	got := read()
	if !strings.Contains(got, "## Done\n\n- [x] alpha\n- [x] gamma") || m.ws.focus.bcol != 1 {
		t.Fatalf("L moves to the next column:\n%s", got)
	}
	m = keys(t, m, "a")
	m = typeText(t, m, "nova tarefa")
	m = keys(t, m, "enter")
	if !strings.Contains(read(), "- [ ] nova tarefa") {
		t.Fatal("a adds a card")
	}
	m = keys(t, m, "d", "d", "y")
	if strings.Contains(read(), "nova tarefa") {
		t.Fatal("dd y deletes")
	}
	if !strings.HasSuffix(read(), "%% kanban:settings\n```\n{}\n```\n%%\n") || !strings.HasPrefix(read(), "---\ntitle: Board\nkanban-plugin: board\n---\n") {
		t.Fatalf("settings/frontmatter changed:\n%s", read())
	}
	if v := stripAnsi(m.View()); !strings.Contains(v, "Todo") || !strings.Contains(v, "beta") {
		t.Fatalf("board render:\n%s", v)
	}
	m = keys(t, m, "z", "v")
	if m.ws.focus.view != viewSource {
		t.Fatal("zv → source")
	}
}

func TestKanbanReadingNeverWrites(t *testing.T) {
	dir := notesVault(t)
	path := filepath.Join(dir, "board.md")
	mustWrite(t, path, "---\ntitle: Board\nkanban-plugin: board\n---\n\n## Todo\n\n- [ ] alpha [[two]]\n- [ ] beta\n\n## Done\n\n- [x] gamma\n")
	before, _ := os.ReadFile(path)
	m := newTreeModel(t, dir)
	m.width, m.height = 120, 30
	m = openNamed(t, m, "Board")
	for _, k := range []string{"x", " ", "H", "L", "J", "a", "o", "A", "d", "d", "y", "u", "D", "j", "l", "h", "k", "tab", "shift+tab"} {
		m = keys(t, m, k)
	}
	if after, _ := os.ReadFile(path); string(after) != string(before) || m.ws.focus.buf.dirty() {
		t.Fatalf("Reading changed the board:\n%s", after)
	}
	if m.ws.focus.bedit || m.ws.focus.binput != "" {
		t.Fatal("still Reading, no prompt")
	}
	// links on the focused card: K card, ⏎ follows
	m.ws.focus.bcol, m.ws.focus.bcard = 0, 0
	m = keys(t, m, "K")
	if m.hover == nil || m.hover.note == nil || m.hover.note.Title != "Two" {
		t.Fatal("K on a card link")
	}
	m = keys(t, m, "esc", "ctrl+w", "f")
	if len(m.ws.leaves()) != 2 || m.ws.focus.buf.note.Title != "Two" {
		t.Fatal("⌃w f on a card link")
	}
	m = keys(t, m, "ctrl+w", "q")
	// Edit and back: Esc / q return to Reading
	m = keys(t, m, "e")
	if !m.ws.focus.bedit || !strings.Contains(stripAnsi(m.View()), "quadro · edição") {
		t.Fatal("e → Edit")
	}
	m = keys(t, m, "esc")
	if m.ws.focus.bedit {
		t.Fatal("Esc → Reading")
	}
	m = keys(t, m, "i", "q")
	if m.ws.focus.bedit || len(m.ws.leaves()) != 1 {
		t.Fatal("q in Edit → Reading (not close)")
	}
	// zv: Reading → Edit → Source → Reading
	m = keys(t, m, "z", "v")
	if !m.ws.focus.bedit {
		t.Fatal("zv → Edit")
	}
	m = keys(t, m, "z", "v")
	if m.ws.focus.view != viewSource {
		t.Fatal("zv → Source")
	}
	m = keys(t, m, ":")
	m = typeText(t, m, "reader")
	m = keys(t, m, "enter")
	if m.ws.focus.view != viewBoard || m.ws.focus.bedit {
		t.Fatalf(":reader → board Reading (%v)", m.ws.focus.view)
	}
	if after, _ := os.ReadFile(path); string(after) != string(before) {
		t.Fatalf("modes changed the file:\n%s", after)
	}
}

func TestCanvasEditHint(t *testing.T) {
	dir := notesVault(t)
	m := newTreeModel(t, dir)
	m.width, m.height = 120, 30
	m = openNamed(t, m, "map")
	m = keys(t, m, "i")
	if m.ws.focus.view != viewCanvas || !strings.Contains(m.statusMsg, "app ou na web") {
		t.Fatalf("i in a canvas: %v %q", m.ws.focus.view, m.statusMsg)
	}
}

func TestCanvasOutlineOpensFileNode(t *testing.T) {
	dir := notesVault(t)
	m := newTreeModel(t, dir)
	m.width, m.height = 120, 30
	m = openNamed(t, m, "map")
	if m.ws.focus.view != viewCanvas {
		t.Fatalf("canvas view: %v", m.ws.focus.view)
	}
	v := stripAnsi(m.View())
	for _, want := range []string{"▣ Plano", "◇ two.md", "¶ ideia", "ideia → two.md  (usa)"} {
		if !strings.Contains(v, want) {
			t.Fatalf("outline missing %q:\n%s", want, v)
		}
	}
	m = keys(t, m, "j", "ctrl+w", "f")
	if len(m.ws.leaves()) != 2 || m.ws.focus.buf.note.Title != "Two" {
		t.Fatal("⌃w f on a file node opens it in a split")
	}
	// invalid JSON mid-edit keeps the last good outline
	m = keys(t, m, "ctrl+w", "h")
	p := m.ws.focus
	p.buf.eng.SetText(`{"nodes":[`)
	rows, _, warn := m.canvasRows(p)
	if len(rows) == 0 || warn == "" {
		t.Fatal("last good parse kept")
	}
}

func TestMerge3(t *testing.T) {
	cases := []struct {
		base, ours, theirs, want string
		ok                       bool
	}{
		{"a\nb\nc", "A\nb\nc", "a\nb\nC", "A\nb\nC", true},
		{"a\nb\nc", "a\nb\nc", "x", "x", true},
		{"a\nb\nc", "aX\nb\nc", "aY\nb\nc", "", false},
		{"hello world", "hello brave world", "hello world!", "hello brave world!", true},
	}
	for _, c := range cases {
		got, ok := merge3(c.base, c.ours, c.theirs)
		if ok != c.ok || (ok && got != c.want) {
			t.Errorf("merge3(%q,%q,%q) = %q,%v", c.base, c.ours, c.theirs, got, ok)
		}
	}
}

func TestSaveMergesExternalChange(t *testing.T) {
	dir := notesVault(t)
	m := newTreeModel(t, dir)
	m = openNamed(t, m, "Two")
	m = keys(t, m, "i", "g", "g", "A")
	m = typeText(t, m, " (local)")
	m = keys(t, m, "esc")
	// a pull rewrote the file meanwhile (different region)
	path := filepath.Join(dir, "two.md")
	mustWrite(t, path, "---\nid: two\ntitle: Two\n---\n\n# two\n\nsecond note body, from the server\n")
	m = keys(t, m, ":", "w", "enter")
	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), "# two (local)") || !strings.Contains(string(data), "from the server") {
		t.Fatalf("not merged:\n%s", data)
	}
	// overlapping edits: ours goes to a conflict copy, nothing is lost
	m = keys(t, m, "G", "0", "c", "w")
	m = typeText(t, m, "SECOND")
	m = keys(t, m, "esc")
	mustWrite(t, path, "---\nid: two\ntitle: Two\n---\n\n# two (local)\n\n2nd note body, from the server\n")
	m = keys(t, m, ":", "w", "enter")
	cp, err := os.ReadFile(filepath.Join(dir, "two (conflito).md"))
	if err != nil || !strings.Contains(string(cp), "SECOND note body") {
		t.Fatalf("conflict copy: %v %s", err, cp)
	}
	if !strings.Contains(m.ws.focus.buf.eng.Text(), "2nd note") {
		t.Fatal("buffer reloads the disk version")
	}
}

func TestLayoutPersistsPerBook(t *testing.T) {
	dir := notesVault(t)
	m := newTreeModel(t, dir)
	m = openNamed(t, m, "One")
	m = keys(t, m, "ctrl+w", "v")
	m = keys(t, m, "-")
	m = moveTo(t, m, "Two")
	m = keys(t, m, "enter")
	// reopening the book lands on its last note with the saved layout
	fresh := drive(NewModelWithInitialNote(dir, ""))
	if fresh.viewMode != ViewFullNote || fresh.floatExp {
		t.Fatalf("reopen: view %v explorer %v", fresh.viewMode, fresh.floatExp)
	}
	if n := len(fresh.ws.leaves()); n != 2 || fresh.ws.focus.buf.note.Title != "Two" {
		t.Fatalf("layout restored with %d panes", n)
	}
}

func TestAccountsSignInSignOutNeverShowsSecrets(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	var logouts atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/auth/login":
			var body struct{ Username, Password string }
			json.NewDecoder(r.Body).Decode(&body)
			if body.Password != "s3cret-pw" {
				w.WriteHeader(401)
				w.Write([]byte(`{"error":"invalid_credentials"}`))
				return
			}
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"token":"tok-SECRET-123","expires_at":"2099-01-01T00:00:00Z","user":{"id":"u1","username":"ana","display_name":"Ana"}}`))
		case "/api/auth/logout":
			logouts.Add(1)
			w.WriteHeader(204)
		default:
			w.WriteHeader(404)
		}
	}))
	defer srv.Close()
	m := newTreeModel(t, notesVault(t))
	m = keys(t, m, "g", "a")
	if m.acct == nil || m.legendContext() != CtxAccounts {
		t.Fatal("ga opens accounts")
	}
	m = keys(t, m, "a")
	m = typeText(t, m, srv.URL)
	m = keys(t, m, "tab")
	m = typeText(t, m, "ana")
	m = keys(t, m, "tab")
	m = typeText(t, m, "wrong")
	if v := m.View(); strings.Contains(v, "wrong") || !strings.Contains(v, "•••••") {
		t.Fatal("password must be masked")
	}
	m = keys(t, m, "enter")
	// the card stays open with the error inline, the password cleared
	if m.acct.form == nil || !strings.Contains(m.acct.form.err, "inválidos") || m.acct.form.pass != "" || m.acct.form.field != 2 {
		t.Fatalf("bad password: %+v", m.acct.form)
	}
	m = keys(t, m, "ctrl+t")
	m = typeText(t, m, "s3cret-pw")
	if !strings.Contains(m.View(), "s3cret-pw") {
		t.Fatal("⌃t shows the password")
	}
	m = keys(t, m, "ctrl+t")
	if strings.Contains(m.View(), "s3cret-pw") {
		t.Fatal("⌃t hides it again")
	}
	m = keys(t, m, "enter")
	f, err := account.Load()
	if err != nil || len(f.Accounts) != 1 || f.Accounts[0].Token != "tok-SECRET-123" || f.Accounts[0].Username != "ana" {
		t.Fatalf("accounts.yaml: %+v %v (screen err %q)", f, err, m.acct.err)
	}
	v := m.View()
	if strings.Contains(v, "tok-SECRET") || strings.Contains(v, "s3cret") {
		t.Fatal("a secret reached the screen")
	}
	if !strings.Contains(stripAnsi(v), "conectada") {
		t.Fatalf("account row:\n%s", stripAnsi(v))
	}
	m = keys(t, m, "s", "y")
	f, _ = account.Load()
	if len(f.Accounts) != 1 || f.Accounts[0].Token != "" || logouts.Load() != 1 {
		t.Fatalf("sign out: %+v logouts %d", f.Accounts, logouts.Load())
	}
	m = keys(t, m, "d", "y")
	f, _ = account.Load()
	if len(f.Accounts) != 0 {
		t.Fatal("remove")
	}
	m = keys(t, m, "esc")
	if m.acct != nil {
		t.Fatal("esc closes")
	}
}

func TestCursorShapeSequences(t *testing.T) {
	dir := notesVault(t)
	m := newTreeModel(t, dir)
	m = openNamed(t, m, "One")
	m = keys(t, m, "i")
	if m.lastShape != "" && m.lastShape != vim.ShapeBlock.DECSCUSR() {
		t.Fatalf("normal mode shape %q", m.lastShape)
	}
	m = keys(t, m, "i")
	if m.lastShape != vim.ShapeBar.DECSCUSR() {
		t.Fatalf("insert → bar, got %q", m.lastShape)
	}
	m = keys(t, m, "esc")
	if m.lastShape != vim.ShapeBlock.DECSCUSR() {
		t.Fatalf("normal → block, got %q", m.lastShape)
	}
}

var _ = tea.Quit

func TestEscapeSequenceSettingAndFlushOnExit(t *testing.T) {
	dir := notesVault(t)
	m := newTreeModel(t, dir)
	m.cfg.EscapeSequences = []string{"jj"}
	m.cfg.Autosave = false
	m = openNamed(t, m, "Two")
	m = keys(t, m, "i", "g", "g", "I", "x", "j", "j")
	if m.ws.focus.buf.eng.Mode() != vim.ModeNormal {
		t.Fatal("jj leaves insert mode")
	}
	if !strings.HasPrefix(m.ws.focus.buf.eng.Text(), "x# two") {
		t.Fatalf("text %q", m.ws.focus.buf.eng.Text())
	}
	m.FlushEdits()
	data, _ := os.ReadFile(filepath.Join(dir, "two.md"))
	if !strings.Contains(string(data), "\nx# two\n") {
		t.Fatalf("FlushEdits did not save:\n%s", data)
	}
}

func TestExpandImagesReplacesPlaceholders(t *testing.T) {
	imageRenders.Store("/x/cat.png@40", []string{"IMG-ROW-1", "IMG-ROW-2"})
	out := expandImages([]string{"before", "  \x1b[1mLUMIIMG0X\x1b[0m", "after"}, []string{"/x/cat.png"}, 40)
	if strings.Join(out, "|") != "before|IMG-ROW-1|IMG-ROW-2|after" {
		t.Fatalf("%q", out)
	}
}
