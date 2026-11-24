package ui

import (
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/vinizap/lumi/tui-client/theme"
)

// Pane zoom (SPEC-PARTE2 §3, tmux-style): ⌃w z, :zoom, the palette, the
// ⤢ button in a pane header or a double-click on the header toggle the
// focused pane between filling the note area and the exact layout it
// came from. The other panes are hidden, not closed. Moving to another
// pane, splitting or closing unzooms first. Not persisted. The pane
// grows from its rect to the full area (spring); instant under Reduce
// motion.

// zoomButton is the header button (zoom / unzoom).
const (
	zoomGlyph   = "⤢"
	unzoomGlyph = "⤡"
	zoomBtnW    = 2 // glyph + space
)

// zoomed is the zoomed pane (nil: the whole layout shows).
func (m Model) zoomed() *notePane {
	if m.ws == nil || m.ws.zoom == nil || m.ws.nodeOf(m.ws.zoom) == nil {
		return nil
	}
	return m.ws.zoom
}

// toggleZoom zooms the focused pane or restores the layout.
func (m *Model) toggleZoom() {
	if m.ws == nil || m.ws.focus == nil {
		return
	}
	if m.zoomed() != nil {
		m.unzoom()
		m.statusMsg = "painéis restaurados"
		return
	}
	if len(m.ws.leaves()) < 2 {
		m.statusMsg = "só há um painel"
		return
	}
	p := m.ws.focus
	from := m.layoutRects()[p]
	m.ws.zoom = p
	if m.mo != nil && !m.mo.reduced {
		m.mo.zoomPane, m.mo.zoomFrom = p, from
		m.mo.to(animZoom, 0, 1)
	}
	m.statusMsg = "zoom: " + paneTitle(p)
}

// unzoom shows the whole layout again (no-op when not zoomed).
func (m *Model) unzoom() {
	p := m.zoomed()
	if m.ws != nil {
		m.ws.zoom = nil
	}
	if p == nil {
		return
	}
	if m.mo != nil && !m.mo.reduced {
		m.mo.zoomPane, m.mo.zoomFrom = p, m.layoutRects()[p]
		m.mo.to(animZoom, 1, 0)
	}
}

func paneTitle(p *notePane) string {
	if p.buf.note != nil && p.buf.note.Title != "" {
		return p.buf.note.Title
	}
	return p.buf.path
}

// zoomFrame is the zoom animation's pane and rect while it moves.
func (m Model) zoomFrame() (*notePane, rect, bool) {
	a, ok := m.mo.get(animZoom)
	if !ok || !a.active || m.mo.zoomPane == nil || m.ws.nodeOf(m.mo.zoomPane) == nil {
		return nil, rect{}, false
	}
	full := rect{0, 0, m.width, m.contentHeight()}
	from := m.mo.zoomFrom
	t := a.value(0, 1)
	r := rect{lerp(from.x, full.x, t), lerp(from.y, full.y, t), lerp(from.w, full.w, t), lerp(from.h, full.h, t)}
	r.w, r.h = max(4, r.w), max(2, r.h)
	return m.mo.zoomPane, r, true
}

// headerClick: a click on a pane header's ⤢ button, or a double-click
// on the header, toggles the zoom (and focuses the pane).
func (m Model) headerClick(p *notePane, r rect, msg tea.MouseMsg) (Model, tea.Cmd, bool) {
	onBtn := m.zoomButtonShown(p) && msg.X >= r.x+r.w-zoomBtnW-1 && msg.X < r.x+r.w
	dbl := m.isDoubleClick(msg.X, msg.Y)
	if !onBtn && !dbl {
		return m, nil, false
	}
	m.setFocusKeepZoom(p)
	m.toggleZoom()
	return m, nil, true
}

// zoomButtonShown: the header shows ⤢ when there is something to zoom.
func (m Model) zoomButtonShown(p *notePane) bool {
	return m.ws != nil && (len(m.ws.leaves()) > 1 || m.ws.zoom == p)
}

// renderZoomButton is ⤢ (or ⤡ while zoomed), right-aligned in headers.
func (m Model) renderZoomButton(p *notePane) string {
	g := zoomGlyph
	st := lipgloss.NewStyle().Foreground(theme.Current.TextDim)
	if m.zoomed() == p {
		g = unzoomGlyph
		st = st.Foreground(theme.Current.Accent).Bold(true)
	}
	return st.Render(g + " ")
}

// setFocusKeepZoom focuses p without the unzoom setFocus does.
func (m *Model) setFocusKeepZoom(p *notePane) {
	z := m.ws.zoom
	m.ws.zoom = nil
	m.setFocus(p)
	m.ws.zoom = z
}
