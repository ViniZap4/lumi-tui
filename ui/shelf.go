package ui

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/vinizap/lumi/tui-client/account"
	"github.com/vinizap/lumi/tui-client/books"
	"github.com/vinizap/lumi/tui-client/theme"
)

// The Livros shelf: the floating explorer's books level, the same one
// the web and the Mac ship (DESIGN-LANGUAGE status v2; web
// lib/books-nav.ts). Books are a *scope*, not a folder:
//
//   - "Livros": ONE list of every book, grouped under account headers.
//     Headers are not rows: j/k skip them and cross groups.
//   - "Contas e servidores": a layer above it, only with 3+ accounts
//     (shelfAccountsMin). h on Livros climbs to it, l / ⏎ on an account
//     lands on that account's first book. With fewer accounts h stays
//     on Livros and says so.
//
// Book numbers (g1–g9) follow the selector order (the registry order),
// so the number on a row is the one that opens it.

// shelfAccountsMin: below this many accounts the grouped list is enough.
const shelfAccountsMin = 3

// booksLevel is the shelf's cursor.
type booksLevel struct {
	accounts bool   // the "Contas e servidores" layer is current
	acct     int    // account under the cursor (accounts layer)
	cursor   int    // flat book index across the groups (Livros)
	prefix   string // pending g chord
}

// shelfGroup is one account's books, in selector order.
type shelfGroup struct {
	key     string // "local" or "user@host"
	server  string
	account string
	idx     []int // registry indexes
}

// bookGroup is the account a book belongs to ("local" when unbound).
func bookGroup(b books.Book) string {
	if !b.IsBound() {
		return "local"
	}
	return b.Account + "@" + books.Host(b.ServerURL)
}

// shelfGroups groups the registry by account, each account where its
// first book appears (local books are just another group).
func shelfGroups(reg []books.Book) []shelfGroup {
	var out []shelfGroup
	at := map[string]int{}
	for i, b := range reg {
		k := bookGroup(b)
		g, ok := at[k]
		if !ok {
			g = len(out)
			at[k] = g
			out = append(out, shelfGroup{key: k, server: b.ServerURL, account: b.Account})
		}
		out[g].idx = append(out[g].idx, i)
	}
	return out
}

// shelfFlat is every book's registry index in shelf order.
func shelfFlat(groups []shelfGroup) []int {
	var out []int
	for _, g := range groups {
		out = append(out, g.idx...)
	}
	return out
}

// groupOf is the group holding flat index i.
func groupOf(groups []shelfGroup, i int) int {
	for g := range groups {
		if i < len(groups[g].idx) {
			return g
		}
		i -= len(groups[g].idx)
	}
	return max(0, len(groups)-1)
}

// firstOf is the flat index of group g's first book.
func firstOf(groups []shelfGroup, g int) int {
	n := 0
	for i := 0; i < g && i < len(groups); i++ {
		n += len(groups[i].idx)
	}
	return n
}

func (m Model) shelfRegistry() []books.Book {
	if m.bookMgr == nil {
		return nil
	}
	return m.bookMgr.Registry()
}

// openBooksLevel switches the overlay to the shelf, the active book
// under the cursor.
func (m *Model) openBooksLevel() {
	if !m.floatExp {
		m.openOverlay()
	}
	bk := &booksLevel{}
	reg := m.shelfRegistry()
	for i, ix := range shelfFlat(shelfGroups(reg)) {
		if reg[ix].ID == m.activeBookID {
			bk.cursor = i
		}
	}
	m.bk = bk
	m.filterTyping = false
	m.startReveal()
}

// shelfBook is the registry index under the cursor (-1 for none).
func (m Model) shelfBook() int {
	if m.bk == nil || m.bk.accounts {
		return -1
	}
	flat := shelfFlat(shelfGroups(m.shelfRegistry()))
	if m.bk.cursor < 0 || m.bk.cursor >= len(flat) {
		return -1
	}
	return flat[m.bk.cursor]
}

func (m Model) updateBooksLevel(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	bk := m.bk
	key := msg.String()
	reg := m.shelfRegistry()
	groups := shelfGroups(reg)
	flat := shelfFlat(groups)
	if bk.prefix != "" {
		pre := bk.prefix
		bk.prefix = ""
		switch pre + key {
		case "gg":
			if bk.accounts {
				bk.acct = 0
			} else {
				bk.cursor = 0
			}
			return m, nil
		case "gb":
			return m, nil
		case "gh":
			// this book: back to its files
			m.bk = nil
			m.startReveal()
			return m, m.loadItems
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
		if bk.accounts {
			bk.acct = clamp(bk.acct+d, 0, max(0, len(groups)-1))
		} else {
			bk.cursor = clamp(bk.cursor+d, 0, max(0, len(flat)-1))
		}
	case "G":
		if bk.accounts {
			bk.acct = max(0, len(groups)-1)
		} else {
			bk.cursor = max(0, len(flat)-1)
		}
	case "h", "left", "backspace":
		if bk.accounts {
			return m, nil
		}
		if len(groups) < shelfAccountsMin {
			return m, m.showToast("Livros é o topo — sem camada de contas", ToastInfo, 2400*time.Millisecond)
		}
		bk.accounts = true
		bk.acct = groupOf(groups, bk.cursor)
		m.startReveal()
	case "l", "right", "enter":
		if bk.accounts {
			bk.accounts = false
			bk.cursor = firstOf(groups, bk.acct)
			m.startReveal()
			return m, nil
		}
		if bk.cursor >= len(flat) {
			return m, nil
		}
		if reg[flat[bk.cursor]].ID == m.activeBookID {
			// this book: back to its files
			m.bk = nil
			m.startReveal()
			return m, m.loadItems
		}
		return m.switchToBook(flat[bk.cursor])
	case "J", "K":
		if !bk.accounts && bk.cursor < len(flat) {
			id := reg[flat[bk.cursor]].ID
			d := 1
			if key == "K" {
				d = -1
			}
			if err := m.bookMgr.Move(id, d); err != nil {
				return m, m.showToast("Livro: "+err.Error(), ToastError, 4*time.Second)
			}
			// the cursor follows the book
			reg = m.shelfRegistry()
			for i, ix := range shelfFlat(shelfGroups(reg)) {
				if reg[ix].ID == id {
					bk.cursor = i
				}
			}
		}
	case "s":
		if ix := m.shelfBook(); ix >= 0 && reg[ix].ID == m.activeBookID {
			m.showBookDetails = true
		}
	case "c", "L":
		if ix := m.shelfBook(); ix >= 0 {
			kind := cloneKindClone
			if key == "L" {
				kind = cloneKindLink
			}
			return m.beginBookFlow(reg[ix], kind)
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

// --- rendering -----------------------------------------------------------

// shelfRow is one line of the shelf list: a header (not selectable), a
// book or an account card, or a blank spacer.
type shelfRow struct {
	kind  byte // 'h' header, 'b' book, 'a'/'A' account card lines, ' ' spacer
	group int
	flat  int // book: flat index; account: group index
}

// shelfRows lays out the current layer's list.
func shelfRows(groups []shelfGroup, accounts bool) []shelfRow {
	var out []shelfRow
	if accounts {
		for g := range groups {
			if g > 0 {
				out = append(out, shelfRow{kind: ' '})
			}
			// a card: name and status, then host and count
			out = append(out, shelfRow{kind: 'a', group: g, flat: g}, shelfRow{kind: 'A', group: g, flat: g})
		}
		return out
	}
	n := 0
	for g, gr := range groups {
		if g > 0 {
			out = append(out, shelfRow{kind: ' '})
		}
		out = append(out, shelfRow{kind: 'h', group: g})
		for range gr.idx {
			out = append(out, shelfRow{kind: 'b', group: g, flat: n})
			n++
		}
	}
	return out
}

// shelfScroll is the first list row shown so the cursor row stays in
// view (rows above it scroll away).
func shelfScroll(rows []shelfRow, bk *booksLevel, h int) int {
	at := 0
	for i, r := range rows {
		if (bk.accounts && r.kind == 'A' && r.flat == bk.acct) || (!bk.accounts && r.kind == 'b' && r.flat == bk.cursor) {
			at = i
		}
	}
	return max(0, at-h+1)
}

// shelfWidths splits the overlay between the list and the preview.
func shelfWidths(w int) (list, preview int) {
	if w < 70 {
		return w, 0
	}
	preview = max(28, w*38/100)
	return w - preview - 3, preview
}

// acctState is whether this device holds a session for an account.
type acctState int

const (
	acctLocal acctState = iota
	acctOK
	acctNeedsLogin
)

func shelfAccountStates() map[string]acctState {
	out := map[string]acctState{}
	f, err := account.Load()
	if err != nil {
		return out
	}
	for _, a := range f.Accounts {
		st := acctNeedsLogin
		if a.Token != "" {
			st = acctOK
		}
		out[a.Username+"@"+books.Host(a.Server)] = st
	}
	return out
}

// groupState: local books have no account; a bound group without a
// session on this device needs a sign-in.
func groupState(g shelfGroup, states map[string]acctState) acctState {
	if g.key == "local" {
		return acctLocal
	}
	if st, ok := states[g.key]; ok {
		return st
	}
	return acctNeedsLogin
}

func acctDot(st acctState) (glyph, label string, c lipgloss.Color) {
	t := theme.Current
	switch st {
	case acctOK:
		return "●", "conectado", t.Accent
	case acctNeedsLogin:
		return "◌", "precisa entrar", t.Warning
	}
	return "", "", t.TextDim
}

// groupTitle is the header's name and host ("Local", "este aparelho").
func groupTitle(g shelfGroup) (name, host string) {
	if g.key == "local" {
		return "Local", "este aparelho"
	}
	return g.account, books.Host(g.server)
}

// renderBooksLevel paints the shelf inside the overlay (w × h).
func (m Model) renderBooksLevel(w, h int) string {
	t := theme.Current
	dim := lipgloss.NewStyle().Foreground(t.TextDim)
	faint := lipgloss.NewStyle().Foreground(t.Separator)
	text := lipgloss.NewStyle().Foreground(t.Text)
	reg := m.shelfRegistry()
	groups := shelfGroups(reg)
	bk := m.bk
	layer := len(groups) >= shelfAccountsMin

	// breadcrumb: Contas › user@host › Livros  (or just Livros)
	crumb := lipgloss.NewStyle().Foreground(primaryColor).Bold(true)
	sep := faint.Render(" › ")
	var head string
	switch {
	case bk.accounts:
		head = crumb.Render("⌂ Contas e servidores")
	case layer:
		g := groups[groupOf(groups, bk.cursor)]
		name, host := groupTitle(g)
		who := name + "@" + host
		if g.key == "local" {
			who = "Local"
		}
		head = dim.Render("⌂ Contas") + sep + dim.Render(who) + sep + crumb.Render("◫ Livros")
	default:
		head = crumb.Render("◫ Livros")
	}
	stat := dim.Render(plural(len(reg), "livro", "livros") + " · " + plural(len(groups), "conta", "contas") + " ")
	head = " " + head
	head = padVisual(head, max(0, w-lipgloss.Width(stat))) + stat
	out := []string{cutVisual(head, 0, w), faint.Render(strings.Repeat("─", w))}
	colH := max(1, h-2)
	if len(reg) == 0 {
		out = append(out, "", text.Render("  Nenhum livro ainda."), "",
			dim.Render("  lumi books link <pasta>  ·  lumi login <servidor> + lumi books refresh"))
		return strings.Join(out, "\n")
	}

	listW, prevW := shelfWidths(w)
	states := shelfAccountStates()
	rows := shelfRows(groups, bk.accounts)
	top := shelfScroll(rows, bk, colH)
	shown := m.revealRows(colH)
	var list []string
	for i := top; i < len(rows) && len(list) < colH; i++ {
		if len(list) >= shown {
			list = append(list, "")
			continue
		}
		list = append(list, m.shelfLine(rows[i], groups, reg, states, listW))
	}

	var prev []string
	if prevW > 0 {
		if bk.accounts {
			if bk.acct < len(groups) {
				prev = m.shelfAccountPreview(groups[bk.acct], reg, states, prevW)
			}
		} else if ix := m.shelfBook(); ix >= 0 {
			prev = m.bkPreviewLines(reg[ix], prevW)
		}
	}
	cols := []string{strings.Join(fitBlock(list, listW, colH), "\n")}
	if prevW > 0 {
		cols = append(cols, faint.Render(strings.Repeat(" │ \n", colH-1)+" │ "), strings.Join(fitBlock(prev, prevW, colH), "\n"))
	}
	out = append(out, lipgloss.JoinHorizontal(lipgloss.Top, cols...))
	return strings.Join(out, "\n")
}

// shelfLine renders one list row.
func (m Model) shelfLine(r shelfRow, groups []shelfGroup, reg []books.Book, states map[string]acctState, w int) string {
	t := theme.Current
	dim := lipgloss.NewStyle().Foreground(t.TextDim)
	bk := m.bk
	switch r.kind {
	case ' ':
		return ""
	case 'h':
		g := groups[r.group]
		name, host := groupTitle(g)
		avatar := lipgloss.NewStyle().Foreground(t.Background).Background(avatarColor(g.key)).Bold(true).
			Render(" " + strings.ToUpper(firstRune(name)) + " ")
		line := " " + avatar + " " + lipgloss.NewStyle().Foreground(t.Text).Bold(true).Render(name) +
			"  " + dim.Render("⌂ "+host)
		if glyph, _, c := acctDot(groupState(g, states)); glyph != "" {
			line += " " + lipgloss.NewStyle().Foreground(c).Render(glyph)
		}
		count := dim.Render(plural(len(g.idx), "livro", "livros") + " ")
		return padVisual(cutVisual(line, 0, w-lipgloss.Width(count)), w-lipgloss.Width(count)) + count
	case 'a', 'A':
		g := groups[r.group]
		name, host := groupTitle(g)
		on := bk.acct == r.flat
		var left, right string
		if r.kind == 'a' {
			left = "   " + lipgloss.NewStyle().Foreground(avatarColor(g.key)).Bold(true).Render(strings.ToUpper(firstRune(name))) +
				"  " + lipgloss.NewStyle().Foreground(t.Text).Bold(true).Render(name)
			if glyph, label, c := acctDot(groupState(g, states)); glyph != "" {
				right = lipgloss.NewStyle().Foreground(c).Render(glyph+" "+label) + " "
			}
			if on {
				left = " › " + strings.ToUpper(firstRune(name)) + "  " + name
			}
		} else {
			left = "      " + dim.Render("⌂ "+host)
			right = dim.Render(plural(len(g.idx), "livro", "livros") + " ")
		}
		rw := lipgloss.Width(right)
		line := padVisual(cutVisual(left, 0, w-rw), w-rw)
		if on {
			return selectRow(stripAnsi(line)+stripAnsi(right), w)
		}
		return line + right
	}
	// a book
	ix := groups[r.group].idx[r.flat-firstOf(groups, r.group)]
	b := reg[ix]
	v, _ := m.bookMgr.View(b.ID)
	st := v.Status.State
	key := "    "
	if ix < 9 {
		key = fmt.Sprintf("g%d  ", ix+1)
	}
	sub := st.Label()
	if !b.LastOpenedAt.IsZero() {
		sub += " · há " + humanSince(time.Since(b.LastOpenedAt))
	}
	open := ""
	if b.ID == m.activeBookID {
		open = "aberto  "
	}
	right := open + key
	nameW := max(4, min(lipgloss.Width(b.Name), w/2))
	// the cursor row: an accent slab and a › marker (not colour alone)
	plain := " › ▤ " + truncate(b.Name, nameW) + "  " + m.syncGlyphPlain(st) + " " + sub
	if r.flat == bk.cursor {
		return selectRow(padVisual(cutVisual(plain, 0, w-lipgloss.Width(right)), w-lipgloss.Width(right))+right, w)
	}
	left := "   " + lipgloss.NewStyle().Foreground(t.Primary).Render("▤") + " " +
		lipgloss.NewStyle().Foreground(t.Text).Render(truncate(b.Name, nameW)) + "  " + m.syncGlyph(st) + " " + dim.Render(sub)
	rs := lipgloss.NewStyle().Foreground(accentColor).Render(open) + lipgloss.NewStyle().Foreground(secondaryColor).Render(key)
	return padVisual(cutVisual(left, 0, w-lipgloss.Width(right)), w-lipgloss.Width(right)) + rs
}

// syncGlyphPlain is syncGlyph without colour (inside a selected row).
func (m Model) syncGlyphPlain(st books.State) string { return stripAnsi(m.syncGlyph(st)) }

// selectRow paints the cursor row (accent slab).
func selectRow(s string, w int) string {
	t := theme.Current
	return lipgloss.NewStyle().Width(w).Foreground(t.Background).Background(accentColor).Bold(true).Render(cutVisual(s, 0, w))
}

func firstRune(s string) string {
	for _, r := range s {
		return string(r)
	}
	return "?"
}

// avatarColor is a stable theme colour per account (web: avatarSlot).
func avatarColor(key string) lipgloss.Color {
	t := theme.Current
	var h uint32
	for _, c := range key {
		h = h*31 + uint32(c)
	}
	return [...]lipgloss.Color{t.Primary, t.Secondary, t.Accent, t.Info}[h%4]
}

// shelfAccountPreview is the preview of an account card.
func (m Model) shelfAccountPreview(g shelfGroup, reg []books.Book, states map[string]acctState, w int) []string {
	t := theme.Current
	dim := lipgloss.NewStyle().Foreground(t.TextDim)
	name, host := groupTitle(g)
	_, label, _ := acctDot(groupState(g, states))
	if g.key == "local" {
		label = "livros sem servidor"
	}
	out := []string{
		lipgloss.NewStyle().Bold(true).Foreground(primaryColor).Render(" " + truncate(name, w-2)),
		dim.Render(" " + truncate(host+" · "+label, w-2)), "",
	}
	for _, ix := range g.idx {
		out = append(out, " ▤ "+truncate(reg[ix].Name, w-4))
	}
	return append(out, "", dim.Render(truncate(" ⏎ ou l mostra os livros desta conta", w-1)))
}

// bkPreviewLines is the preview column of book b; its last line holds
// the clickable actions.
func (m Model) bkPreviewLines(b books.Book, right int) []string {
	t := theme.Current
	dim := lipgloss.NewStyle().Foreground(t.TextDim)
	text := lipgloss.NewStyle().Foreground(t.Text)
	v, _ := m.bookMgr.View(b.ID)
	st := v.Status.State
	row := func(k, val string) string {
		return dim.Render(" "+padRight(k, 12)) + text.Render(truncate(val, max(4, right-14)))
	}
	where := "Local · este aparelho"
	if b.IsBound() {
		where = b.Account + "@" + books.Host(b.ServerURL)
	}
	pcol := []string{
		lipgloss.NewStyle().Bold(true).Foreground(primaryColor).Render(" ▤ " + truncate(b.Name, right-4)),
		dim.Render(" " + truncate(where, right-2)), "",
	}
	pcol = append(pcol, row("estado", st.Glyph()+" "+st.Label()))
	opened := "nunca neste aparelho"
	if !b.LastOpenedAt.IsZero() {
		opened = "há " + humanSince(time.Since(b.LastOpenedAt))
	}
	pcol = append(pcol, row("aberto", opened))
	if b.IsBound() {
		pcol = append(pcol, row("servidor", b.ServerURL))
	}
	if b.Path == "" {
		pcol = append(pcol, row("pasta", "(sem pasta neste aparelho)"))
	} else {
		pcol = append(pcol, row("pasta", b.Path))
	}
	if n := len(v.Pending); n > 0 {
		pcol = append(pcol, row("pendentes", pendingLabel(n)))
	}
	if !v.LastSynced.IsZero() {
		pcol = append(pcol, row("sincronizou", "há "+humanSince(time.Since(v.LastSynced))))
	}
	hint := " ⏎ ou l abre este livro"
	if b.ID == m.activeBookID {
		hint = " aberto agora — ⏎ ou l volta às notas"
	}
	if b.IsBound() && b.Path == "" && st == books.StateSignIn {
		hint = " ⏎ entra na conta e depois clona"
	}
	pcol = append(pcol, "", dim.Render(truncate(hint, max(4, right-1))))
	pcol = append(pcol, "", bkPreviewActionLine(b, right))
	return pcol
}

// booksLevelMouse: click a book or an account to select it (double-click
// opens), click a preview action to run it. x, y are content
// coordinates inside the overlay.
func (m Model) booksLevelMouse(x, y int, iw int) (tea.Model, tea.Cmd) {
	bk := m.bk
	if bk == nil || m.bookMgr == nil || y < 2 {
		return m, nil
	}
	reg := m.shelfRegistry()
	groups := shelfGroups(reg)
	listW, prevW := shelfWidths(iw)
	// as renderFloatExplorer sizes it: inner height - footer - head
	colH := max(1, max(1, m.overlayRect().h-3)-2)
	rows := shelfRows(groups, bk.accounts)
	row := y - 2 + shelfScroll(rows, bk, colH)
	dbl := m.isDoubleClick(x, y)
	switch {
	case x < listW:
		if row < 0 || row >= len(rows) {
			return m, nil
		}
		switch sr := rows[row]; sr.kind {
		case 'b':
			bk.cursor = sr.flat
		case 'a', 'A':
			bk.acct = sr.flat
		default:
			return m, nil
		}
		if dbl {
			return m.updateBooksLevel(keyFor("enter"))
		}
	case prevW > 0 && x >= listW+3 && !bk.accounts:
		ix := m.shelfBook()
		if ix < 0 {
			return m, nil
		}
		lines := m.bkPreviewLines(reg[ix], prevW)
		if y-2 == len(lines)-1 {
			if k := bkPreviewActionAt(reg[ix], x-(listW+3)); k != "" {
				return m.updateBooksLevel(keyFor(k))
			}
		}
	}
	return m, nil
}
