package ui

import (
	"strings"

	"github.com/vinizap/lumi/tui-client/config"
)

// Modo zen (PHASE3-PROPOSAL §7): no legend and no status line. The
// body takes every row; the chrome comes back, drawn over the bottom
// rows so the text never reflows, only while it carries something the
// user is in the middle of: a pending key prefix (g, z, <leader>, ⌃w,
// a count, a vim operator), the `?` help, a prompt, a confirmation, the
// filter or the `:` command line. Toggles: <leader>z, :zen, the palette
// ("Modo zen"). Persisted in config.yaml (`zen: true`).

// toggleZen flips zen and persists it per device.
func (m *Model) toggleZen() {
	m.zen = !m.zen
	on := m.zen
	m.persist(func(c *config.Config) { c.Zen = on })
	if on {
		m.statusMsg = ""
	}
}

// zenReveal: in zen, the chrome shows while input is in flight.
func (m Model) zenReveal() bool {
	if m.showHelp || m.showInput || m.showConfirm || m.filterTyping || m.winPending {
		return true
	}
	if m.pendingInput() != "" {
		return true
	}
	if m.viewMode == ViewFullNote && m.ws != nil && m.ws.focus != nil {
		if e := m.focusEngine(); e != nil && e.Pending() != "" {
			return true
		}
		if m.legendContext() == CtxCmdline {
			return true
		}
	}
	return false
}

// overlayBottom draws rows over the last len(rows) lines of body
// (body is already fitted to the screen height).
func overlayBottom(body string, rows []string) string {
	if len(rows) == 0 {
		return body
	}
	lines := strings.Split(body, "\n")
	start := max(0, len(lines)-len(rows))
	for i, r := range rows {
		if start+i < len(lines) {
			lines[start+i] = r
		} else {
			lines = append(lines, r)
		}
	}
	return strings.Join(lines, "\n")
}
