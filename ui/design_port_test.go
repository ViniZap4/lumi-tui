package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/harmonica"
	"github.com/vinizap/lumi/tui-client/theme"
	"github.com/vinizap/lumi/tui-client/vim"
)

// DESIGN-LANGUAGE §6.1: five named springs, the doc's ω0 and ζ, and
// each animated value rides one of them.
func TestNamedSpringsMatchTheDesignLanguage(t *testing.T) {
	want := map[springName][2]float64{
		springSnappy: {26.5, 0.72}, springGlide: {19.5, 0.67}, springGentle: {16.1, 0.87},
		springBouncy: {20.0, 0.50}, springSoft: {12.2, 0.90},
	}
	for n, p := range want {
		if got := springParams[n]; got.omega != p[0] || got.zeta != p[1] {
			t.Errorf("%s: ω0 %v ζ %v, want %v", n, got.omega, got.zeta, p)
		}
	}
	// overshoot and settle, integrated at 60 Hz like the frame tick
	run := func(n springName) (peak float64, frames int) {
		s := harmonica.NewSpring(harmonica.FPS(frameRate), springParams[n].omega, springParams[n].zeta)
		a := anim{target: 1, active: true}
		for a.active && frames < 600 {
			a.step(s)
			peak = max(peak, a.pos)
			frames++
		}
		return peak, frames
	}
	// §6.1: bouncy ≈ 16 % overshoot, glide ≈ 6 %, gentle ≈ 0.2 %, soft 0
	for n, lim := range map[springName][2]float64{
		springBouncy: {1.12, 1.20}, springGlide: {1.03, 1.09}, springSnappy: {1.02, 1.06},
		springGentle: {1.0, 1.01}, springSoft: {1.0, 1.001},
	} {
		peak, frames := run(n)
		if peak < lim[0] || peak > lim[1] {
			t.Errorf("%s overshoot peak %.3f, want %.3f–%.3f", n, peak, lim[0], lim[1])
		}
		// settles well under a second (the doc's settle ≤ 560 ms, plus
		// the tight rest threshold)
		if frames > 60 {
			t.Errorf("%s settles in %d frames", n, frames)
		}
	}
	for k := animKind(0); k < animCount; k++ {
		if animSpring[k] >= springCount {
			t.Errorf("anim %d has no spring", k)
		}
	}
	if animSpring[animOverlay] != springBouncy || animSpring[animSplit] != springGlide || animSpring[animReveal] != springGentle {
		t.Error("overlay pops (bouncy), panes travel (glide), rows rise (gentle)")
	}
}

// §6.7 / §8 row 16: the mode colour is the pill's and the gutter mark's.
func TestModeColourOnPillAndGutter(t *testing.T) {
	th := theme.Current
	for md, want := range map[vim.Mode]string{
		vim.ModeNormal: string(th.Primary), vim.ModeInsert: string(th.Accent),
		vim.ModeVisual: string(th.Secondary), vim.ModeVisualLine: string(th.Secondary), vim.ModeReplace: string(th.Error),
	} {
		if got := string(modeColor(md)); got != want {
			t.Errorf("%v: %s want %s", md, got, want)
		}
	}
	m := newTreeModel(t, notesVault(t))
	m = openNamed(t, m, "One")
	m = keys(t, m, "e") // source view
	if m.ws.focus.view != viewSource && m.ws.focus.view != viewSplit {
		m.ws.focus.view = viewSource
	}
	if string(m.modeTint()) != string(th.Primary) {
		t.Fatal("normal mode: primary")
	}
	m = keys(t, m, "i")
	if string(m.modeTint()) != string(th.Accent) {
		t.Fatal("insert mode: accent pill")
	}
	if !strings.Contains(stripAnsi(m.View()), "▎") {
		t.Fatal("the cursor line carries the mode mark")
	}
}

// §8 row 7: the divider beside the focused pane is lit (┃, accent); the
// others rest thin (│).
func TestFocusLightsTheDividerBesideTheFocusedPane(t *testing.T) {
	m := newTreeModel(t, notesVault(t))
	m = openNamed(t, m, "One")
	m.mo.setReduced(true)
	m = keys(t, m, "ctrl+w", "v", "ctrl+w", "v")
	if n := len(m.ws.leaves()); n != 3 {
		t.Fatalf("%d panes", n)
	}
	dividers := func() string {
		row := strings.Split(stripAnsi(m.renderWorkspace()), "\n")[3]
		var out []rune
		for _, r := range row {
			if r == '┃' || r == '│' {
				out = append(out, r)
			}
		}
		return string(out)
	}
	// panes: [A | B | C]; focus is the newest (left-most after two vsplits
	// or right-most, depending on splitright) — find it from the rects
	rects := m.layoutRects()
	fr := rects[m.ws.focus]
	lit := 0
	for _, r := range dividers() {
		if r == '┃' {
			lit++
		}
	}
	middle := fr.x > 0 && fr.x+fr.w < m.width
	if (middle && lit != 2) || (!middle && lit != 1) {
		t.Fatalf("focus at x=%d w=%d: %d lit dividers (%q)", fr.x, fr.w, lit, dividers())
	}
	// moving focus moves the light
	before := dividers()
	m = keys(t, m, "ctrl+w", "w")
	if dividers() == before && len(before) > 2 {
		t.Fatalf("the light should move with focus: %q", before)
	}
}

// §8 row 5: opening a note from the explorer flashes its title, briefly,
// and the ticks stop once it is done.
func TestNoteOpenFlashesTitleThenIdles(t *testing.T) {
	m := motionModel(t)
	m = moveTo(t, m, "One")
	next, _ := m.Update(keyMsg("enter"))
	m = next.(Model)
	if a, _ := m.mo.get(animTitle); !a.active {
		t.Fatal("title flash starts")
	}
	m, n := runFrames(t, m, nil)
	if n == 0 || m.mo.busy() || m.mo.a[animTitle].active {
		t.Fatalf("flash settles and the ticker stops (%d frames)", n)
	}
}
