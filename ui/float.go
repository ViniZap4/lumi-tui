package ui

import (
	"fmt"
	"math"
	"path/filepath"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/vinizap/lumi/tui-client/account"
	"github.com/vinizap/lumi/tui-client/books"
	"github.com/vinizap/lumi/tui-client/theme"
)

// Floating explorer (SPEC-PARTE2 §2): the main and only navigation.
// A centred bordered overlay (~80% × 80%) with yazi Miller columns and
// a preview, or a full-height sheet on small terminals. It opens over
// the notes or over the empty home with `-`, `<leader>e`, `:explore`,
// the palette and the "≡ notes" button in the top line. The same
// overlay has a books level (accounts → books) reached with `gb` or `h`
// at the book root.
//
// Inside it the whole explorer keymap works (j/k/h/l, /, a r d y p m .
// R H L ~ …); ⏎ opens the note in the current pane, ⌃v / ⌃s in a
// vertical / horizontal split, Esc / q close it.

// sheetCols / sheetRows: below either the overlay becomes a sheet.
const (
	sheetCols = 100
	sheetRows = 30
)

// isSheet: small terminal, the overlay takes the whole body.
func (m Model) isSheet() bool { return m.width < sheetCols || m.height < sheetRows }

// overlayRect is the settled overlay box (border included).
func (m Model) overlayRect() rect {
	h := m.contentHeight()
	if m.isSheet() {
		return rect{0, 0, m.width, h}
	}
	bw := max(30, m.width*80/100)
	bh := max(8, h*80/100)
	return rect{(m.width - bw) / 2, (h - bh) / 2, bw, bh}
}

// explorerFrame is a copy of m sized to the overlay's inner area, so
// the explorer renders and hit-tests exactly as inside the box.
func (m Model) explorerFrame() (Model, rect) {
	r := m.overlayRect()
	inner := m
	inner.width = max(1, r.w-2)
	inner.height = max(1, r.h-2) + m.bottomRows()
	return inner, rect{r.x + 1, r.y + 1, inner.width, max(1, r.h-2)}
}

// openOverlay shows the overlay (files level) and starts its springs.
func (m *Model) openOverlay() {
	wasOpen := m.floatExp
	m.floatExp = true
	m.bk = nil
	if !wasOpen {
		from := math.NaN()
		if a, ok := m.mo.get(animOverlay); ok && a.pos < 0.05 {
			from = 0
		}
		m.mo.to(animOverlay, from, 1)
	}
	m.startReveal()
}

// closeOverlay hides it; it keeps shrinking away on screen while keys
// already go to the view underneath.
func (m *Model) closeOverlay() {
	m.floatExp = false
	m.bk = nil
	m.filterTyping = false
	m.treePrefix, m.count = "", ""
	m.mo.to(animOverlay, math.NaN(), 0)
}

// startReveal staggers the explorer rows in again.
func (m *Model) startReveal() {
	if m.mo == nil {
		return
	}
	m.mo.revealDir = m.currentDir
	m.mo.to(animReveal, 0, 1)
}

// openFloatExplorer opens the overlay at the focused note's folder
// (or where the explorer last was, on the empty home).
func (m Model) openFloatExplorer() (Model, tea.Cmd) {
	m.syncFocusOut()
	dir := m.currentDir
	if m.ws != nil && m.ws.focus != nil {
		dir = filepath.Dir(m.ws.focus.buf.path)
		m.pendingSelect = m.ws.focus.buf.path
	}
	m.clearFilter()
	m.pane = PaneList
	m.openOverlay()
	cmd := m.navigateTo(dir, 0)
	if cmd == nil {
		cmd = m.loadItems
	}
	return m, cmd
}

// focusedPane is the focused pane pointer (nil without a workspace).
func (m Model) focusedPane() *notePane {
	if m.ws == nil {
		return nil
	}
	return m.ws.focus
}

func (m Model) updateFloatExplorer(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.bk != nil {
		return m.updateBooksLevel(msg)
	}
	key := msg.String()
	busy := m.filterTyping || m.showInput || m.showConfirm || m.showNav || m.treePrefix != "" || m.count != ""
	if !busy {
		switch key {
		case "esc":
			if m.filter != "" {
				m.clearFilter()
				return m, m.loadItems
			}
			m.closeOverlay()
			return m, m.shapeCmd()
		case "q":
			m.closeOverlay()
			return m, m.shapeCmd()
		case "enter", "l", "right", "ctrl+v", "ctrl+s":
			if m.pane == PaneParent {
				break
			}
			it, ok := m.currentItem()
			if ok && !it.IsFolder && it.Note != nil {
				how := map[string]string{"ctrl+v": "vsplit", "ctrl+s": "hsplit"}[key]
				if how == "" || m.ws == nil || m.ws.root == nil {
					how = "replace"
				}
				m.closeOverlay()
				cmd := m.openNoteIn(it.Note, how)
				return m, tea.Batch(cmd, m.shapeCmd())
			}
			if key == "ctrl+v" || key == "ctrl+s" {
				return m, nil
			}
		case "h", "left", "backspace":
			if m.pane == PaneList && m.currentDir == m.rootDir && m.bookMgr != nil {
				m.openBooksLevel()
				return m, nil
			}
		case "e", "i", "E":
			it, ok := m.currentItem()
			if ok && it.Note != nil && m.pane != PaneParent {
				external := m.noteCfg().DefaultEditor == "external"
				if key == "E" {
					external = !external
				}
				if external {
					return m, m.editCmd(&noteRef{note: it.Note, path: it.Path}, false)
				}
				m.closeOverlay()
				cmd := m.openNoteIn(it.Note, "replace")
				var c2 tea.Cmd
				m, c2 = m.editInternal(m.ws.focus)
				return m, tea.Batch(cmd, c2)
			}
		}
	}
	before := m.focusedPane()
	mm, cmd := m.updateTree(msg)
	nm := mm.(Model)
	switch {
	case nm.viewMode == ViewConfig:
		nm.closeOverlay()
	case nm.focusedPane() != before && nm.focusedPane() != nil:
		// a note was opened from inside (parent column, palette jump)
		nm.closeOverlay()
	}
	return nm, cmd
}

// renderFloatExplorer draws the overlay over base. While the overlay
// spring runs, the box grows from (or shrinks to) its centre: the
// content is clipped around the centre and re-bordered.
func (m Model) renderFloatExplorer(base string) string {
	s := 1.0
	if m.mo != nil {
		a, _ := m.mo.get(animOverlay)
		s = a.value(0, 1)
	}
	if s <= 0.02 {
		return base
	}
	r := m.overlayRect()
	iw, ih := max(1, r.w-2), max(1, r.h-2)
	var content string
	if m.bk != nil {
		content = m.renderBooksLevel(iw, ih)
	} else {
		inner, _ := m.explorerFrame()
		inner.floatExp = false
		content = inner.renderTree()
	}
	lines := strings.Split(fitHeight(content, ih), "\n")
	cw, ch := iw, ih
	if s < 1 {
		cw = max(1, lerp(min(10, iw), iw, s))
		ch = max(1, lerp(min(2, ih), ih, s))
		top, left := (ih-ch)/2, (iw-cw)/2
		clip := make([]string, ch)
		for i := range clip {
			clip[i] = cutVisual(padVisual(lines[top+i], iw), left, left+cw)
		}
		lines = clip
	}
	for i := range lines {
		lines[i] = padVisual(lines[i], cw)
	}
	border := theme.Current.Accent
	box := lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(border).
		Background(theme.Current.Background).Render(strings.Join(lines, "\n"))
	x := r.x + (iw-cw)/2
	y := r.y + (ih-ch)/2
	return overlayBox(base, box, x, y, m.width)
}

// --- books level (accounts → books) ------------------------------------

// booksLevel is the overlay's books level: an accounts column, the
// books of the selected account and a preview of the selected book.
type booksLevel struct {
	accounts []string // "local", "user@host"
	acct     int      // selected account
	cursor   int      // selected book (within the account)
	pane     int      // 0 accounts, 1 books
	prefix   string   // pending g chord
}

// bookGroup is the account a book belongs to ("local" when unbound).
func bookGroup(b books.Book) string {
	if !b.IsBound() {
		return "local"
	}
	return b.Account + "@" + books.Host(b.ServerURL)
}

// openBooksLevel switches the overlay to the books level with the
// active book selected.
func (m *Model) openBooksLevel() {
	if !m.floatExp {
		m.openOverlay()
	}
	bk := &booksLevel{pane: 1}
	seen := map[string]bool{}
	add := func(g string) {
		if !seen[g] {
			seen[g] = true
			bk.accounts = append(bk.accounts, g)
		}
	}
	var list []books.Book
	if m.bookMgr != nil {
		list = m.bookMgr.Registry()
	}
	for _, b := range list {
		add(bookGroup(b))
	}
	if f, err := account.Load(); err == nil {
		for _, a := range f.Accounts {
			add(a.Username + "@" + books.Host(a.Server))
		}
	}
	if len(bk.accounts) == 0 {
		bk.accounts = []string{"local"}
	}
	for _, b := range list {
		if b.ID == m.activeBookID {
			for i, g := range bk.accounts {
				if g == bookGroup(b) {
					bk.acct = i
				}
			}
		}
	}
	m.bk = bk
	for i, ix := range m.bkBooks() {
		if list[ix].ID == m.activeBookID {
			bk.cursor = i
		}
	}
	m.filterTyping = false
	m.startReveal()
}

// bkBooks lists the registry indexes of the selected account's books.
func (m Model) bkBooks() []int {
	if m.bk == nil || m.bookMgr == nil || m.bk.acct >= len(m.bk.accounts) {
		return nil
	}
	g := m.bk.accounts[m.bk.acct]
	var out []int
	for i, b := range m.bookMgr.Registry() {
		if bookGroup(b) == g {
			out = append(out, i)
		}
	}
	return out
}

func (m Model) updateBooksLevel(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	bk := m.bk
	key := msg.String()
	idx := m.bkBooks()
	if bk.prefix != "" {
		pre := bk.prefix
		bk.prefix = ""
		switch pre + key {
		case "gg":
			if bk.pane == 0 {
				bk.acct, bk.cursor = 0, 0
			} else {
				bk.cursor = 0
			}
			return m, nil
		case "gb":
			return m, nil
		case "gs":
			m.showBookDetails = m.bookMgr != nil
			return m, nil
		case "ga":
			m.openAccounts()
			return m, nil
		}
		if len(key) == 1 && key[0] >= '1' && key[0] <= '9' {
			return m.switchToBook(int(key[0] - '1'))
		}
		if key == "esc" {
			return m, nil
		}
	}
	switch key {
	case "esc", "q":
		m.closeOverlay()
		return m, m.shapeCmd()
	case "g":
		bk.prefix = "g"
	case "j", "down", "k", "up":
		d := 1
		if key == "k" || key == "up" {
			d = -1
		}
		if bk.pane == 0 {
			bk.acct = clamp(bk.acct+d, 0, len(bk.accounts)-1)
			bk.cursor = 0
		} else {
			bk.cursor = clamp(bk.cursor+d, 0, len(idx)-1)
		}
	case "G":
		if bk.pane == 0 {
			bk.acct, bk.cursor = max(0, len(bk.accounts)-1), 0
		} else {
			bk.cursor = max(0, len(idx)-1)
		}
	case "h", "left", "backspace":
		bk.pane = 0
	case "l", "right", "enter":
		if bk.pane == 0 {
			bk.pane = 1
			bk.cursor = clamp(bk.cursor, 0, max(0, len(idx)-1))
			return m, nil
		}
		if bk.cursor >= len(idx) {
			return m, nil
		}
		reg := m.bookMgr.Registry()
		if reg[idx[bk.cursor]].ID == m.activeBookID {
			// this book: back to its files
			m.bk = nil
			m.startReveal()
			return m, m.loadItems
		}
		return m.switchToBook(idx[bk.cursor])
	case "J", "K":
		if bk.pane == 1 && bk.cursor < len(idx) {
			reg := m.bookMgr.Registry()
			d := 1
			if key == "K" {
				d = -1
			}
			if err := m.bookMgr.Move(reg[idx[bk.cursor]].ID, d); err != nil {
				return m, m.showToast("Livro: "+err.Error(), ToastError, 4*time.Second)
			}
			bk.cursor = clamp(bk.cursor+d, 0, len(idx)-1)
		}
	case "s":
		if bk.cursor < len(idx) && m.bookMgr.Registry()[idx[bk.cursor]].ID == m.activeBookID {
			m.showBookDetails = true
		}
	case "a":
		m.openAccounts()
	default:
		if len(key) == 1 && key[0] >= '1' && key[0] <= '9' {
			return m.switchToBook(int(key[0] - '1'))
		}
		if mm, cmd, ok := m.globalKey(key); ok {
			return mm, cmd
		}
	}
	return m, nil
}

// renderBooksLevel paints the books level inside the overlay.
func (m Model) renderBooksLevel(w, h int) string {
	t := theme.Current
	dim := lipgloss.NewStyle().Foreground(t.TextDim)
	text := lipgloss.NewStyle().Foreground(t.Text)
	logo := lipgloss.NewStyle().Bold(true).Foreground(primaryColor).Render("lumi")
	dot := lipgloss.NewStyle().Foreground(t.Separator).Render(" · ")
	var reg []books.Book
	if m.bookMgr != nil {
		reg = m.bookMgr.Registry()
	}
	head := " " + logo + dot + lipgloss.NewStyle().Foreground(accentColor).Bold(true).Render("livros") +
		dot + lipgloss.NewStyle().Foreground(mutedColor).Render(fmt.Sprintf("%d livros", len(reg)))
	out := []string{padVisual(head, w), lipgloss.NewStyle().Foreground(t.Separator).Render(strings.Repeat("─", w))}
	colH := max(1, h-2)
	if len(reg) == 0 {
		out = append(out, "", text.Render("  Nenhum livro ainda."), "",
			dim.Render("  lumi books link <pasta>  ·  lumi login <servidor> + lumi books refresh"))
		return strings.Join(out, "\n")
	}
	bk := m.bk
	idx := m.bkBooks()
	left, center, right, _ := treeColumnWidths(w, false)
	if left == 0 {
		left = min(24, w/3)
		center = w - left - 3
		right = 0
	}
	sel := func(s string, width int, on, focused bool) string {
		st := lipgloss.NewStyle().Width(width)
		switch {
		case on && focused:
			st = st.Foreground(t.Background).Background(accentColor).Bold(true)
		case on:
			st = st.Foreground(t.Text).Background(t.SelectedBg)
		}
		return st.Render(s)
	}
	var acol []string
	for i, a := range bk.accounts {
		glyph := "▸ "
		if a == "local" {
			glyph = "◇ "
		}
		acol = append(acol, sel(" "+glyph+truncate(a, left-4), left, i == bk.acct, bk.pane == 0))
	}
	var bcol []string
	rows := m.revealRows(colH)
	for i, ix := range idx {
		b := reg[ix]
		v, _ := m.bookMgr.View(b.ID)
		num := "  "
		if ix < 9 {
			num = fmt.Sprintf("%d ", ix+1)
		}
		name := truncate(b.Name, max(4, center-8))
		if b.ID == m.activeBookID {
			name += " •"
		}
		line := " " + num + v.Status.State.Glyph() + " " + name
		if i >= rows {
			line = ""
		}
		bcol = append(bcol, sel(line, center, i == bk.cursor, bk.pane == 1))
	}
	if len(idx) == 0 {
		bcol = append(bcol, dim.Render("  (nenhum livro nesta conta)"))
	}
	var pcol []string
	if right > 0 && bk.cursor < len(idx) {
		b := reg[idx[bk.cursor]]
		v, _ := m.bookMgr.View(b.ID)
		st := v.Status.State
		row := func(k, val string) string {
			return dim.Render(" "+padRight(k, 12)) + text.Render(truncate(val, max(4, right-14)))
		}
		pcol = append(pcol, lipgloss.NewStyle().Bold(true).Foreground(primaryColor).Render(" "+truncate(b.Name, right-2)), "")
		pcol = append(pcol, row("estado", st.Glyph()+" "+st.Label()))
		if b.IsBound() {
			pcol = append(pcol, row("servidor", b.ServerURL), row("conta", b.Account))
		}
		if b.Path == "" {
			pcol = append(pcol, row("pasta", "(sem pasta)"))
		} else {
			pcol = append(pcol, row("pasta", b.Path))
		}
		if n := len(v.Pending); n > 0 {
			pcol = append(pcol, row("pendentes", pendingLabel(n)))
		}
		if !v.LastSynced.IsZero() {
			pcol = append(pcol, row("sincronizou", "há "+humanSince(time.Since(v.LastSynced))))
		}
		pcol = append(pcol, "", dim.Render(truncate(" ⏎ abrir · J/K ordem · s detalhes", max(4, right-1))))
	}
	sep := lipgloss.NewStyle().Foreground(t.Separator).Render(" │ ")
	cols := []string{strings.Join(fitBlock(acol, left, colH), "\n"), sep, strings.Join(fitBlock(bcol, center, colH), "\n")}
	if right > 0 {
		cols = append(cols, sep, strings.Join(fitBlock(pcol, right, colH), "\n"))
	}
	out = append(out, lipgloss.JoinHorizontal(lipgloss.Top, cols...))
	return strings.Join(out, "\n")
}

// revealRows is how many list rows the stagger has revealed so far
// (all of them when settled).
func (m Model) revealRows(rows int) int {
	a, ok := m.mo.get(animReveal)
	if !ok || !a.active {
		return rows + 1<<20
	}
	return int(a.value(0, 1)*float64(rows)*1.4) + 1
}
