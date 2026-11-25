package ui

import (
	"math"
	"os"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/harmonica"
)

// Motion (SPEC-PARTE2 §1, "the app should feel alive"), terminal-native:
// harmonica springs stepped by a frame tick that runs only while
// something moves. Idle means no tick at all (zero CPU). Keys are never
// queued behind an animation: every animated value is render-only
// state, Update applies key effects at once and the next frame simply
// draws the spring a step closer to where the state already is.
//
//	overlay   the floating explorer grows from the centre / shrinks away
//	reveal    explorer rows stagger in on open and on every folder change
//	split     a new pane slides open from zero width
//	toast     toasts slide in from the right edge
//	pill      the mode pill morphs its width between modes
//	pulse     the sync dot pulses on a remote change
//	hover     the K card grows out of the link
//	breathe   the sync dot breathes (braille) while a book is syncing
//	title     an opened note's title flashes accent once
//
// Each value rides one of the five named springs of DESIGN-LANGUAGE
// §6.1 (animSpring), integrated by harmonica at 60 Hz.
//
// Reduce motion (Settings, or NO_MOTION / REDUCE_MOTION in the
// environment) snaps every value to its target: no frames are scheduled.

const (
	frameRate     = 60
	frameInterval = time.Second / frameRate
	// breatheInterval paces the sync spinner when nothing else moves:
	// DESIGN-LANGUAGE §8 row 21, braille frames at 80 ms (12.5 fps keeps
	// the CPU low; it only runs while a book syncs).
	breatheInterval = 80 * time.Millisecond
	// breatheEvery: while springs run at 60 fps the spinner advances
	// every 5th frame (≈ 80 ms).
	breatheEvery = 5
	settleEps    = 0.004
)

// springName is one of the five named springs of DESIGN-LANGUAGE §6.1.
// The TUI uses harmonica with the doc's ω0 and ζ, so a spring here
// rises and overshoots like the same spring on the web and the Mac.
type springName int

const (
	springSnappy springName = iota // press, small UI, pills
	springGlide                    // things that travel: panes, zoom
	springGentle                   // content rising in, rows, views
	springBouncy                   // things that pop: overlays, toasts, cards
	springSoft                     // veils, large surfaces
	springCount
)

// springParams are §6.1's ω0 (rad/s) and ζ per named spring.
var springParams = [springCount]struct{ omega, zeta float64 }{
	springSnappy: {26.5, 0.72},
	springGlide:  {19.5, 0.67},
	springGentle: {16.1, 0.87},
	springBouncy: {20.0, 0.50},
	springSoft:   {12.2, 0.90},
}

func (n springName) String() string {
	return [...]string{"snappy", "glide", "gentle", "bouncy", "soft"}[n]
}

// newSprings builds the five springs for a 60 Hz frame step.
func newSprings() [springCount]harmonica.Spring {
	var out [springCount]harmonica.Spring
	for i, p := range springParams {
		out[i] = harmonica.NewSpring(harmonica.FPS(frameRate), p.omega, p.zeta)
	}
	return out
}

// frameMsg advances the springs by one frame.
type frameMsg struct{}

// anim is one spring-driven value.
type anim struct {
	pos, vel, target float64
	active           bool
}

func (a *anim) step(s harmonica.Spring) {
	if !a.active {
		return
	}
	a.pos, a.vel = s.Update(a.pos, a.vel, a.target)
	if math.Abs(a.pos-a.target) < settleEps && math.Abs(a.vel) < settleEps*4 {
		a.pos, a.vel, a.active = a.target, 0, false
	}
}

// animKind names an animated value.
type animKind int

const (
	animOverlay animKind = iota // floating explorer 0 closed … 1 open
	animReveal                  // explorer rows 0 … 1 revealed
	animSplit                   // new pane 0 … 1 of its width
	animToast                   // toast 0 off-screen … 1 in place
	animPill                    // mode pill 0 old width … 1 new width
	animPulse                   // remote change 1 … 0
	animHover                   // K card 0 … 1 grown
	animZoom                    // zoomed pane 0 in its rect … 1 full area
	animTitle                   // opened note's title flash 1 … 0
	animCount
)

// animSpring is the named spring each value rides (§6.10's column
// "springs / timing").
var animSpring = [animCount]springName{
	animOverlay: springBouncy, // the explorer grows from its trigger
	animReveal:  springGentle, // rows ripple in
	animSplit:   springGlide,  // a pane unfolds from the divider
	animToast:   springBouncy, // toasts spring in
	animPill:    springBouncy, // the mode pill gets a kick
	animPulse:   springBouncy, // the sync dot takes the hit
	animHover:   springBouncy, // the K card grows out of the link
	animZoom:    springGlide,  // the pane expands from its box
	animTitle:   springSnappy, // the title flashes accent, briefly
}

// motion is shared by Model copies (pointer), like the workspace.
type motion struct {
	reduced bool
	springs [springCount]harmonica.Spring

	a [animCount]anim

	// splitPane is the pane sliding open (nil when none).
	splitPane *notePane
	// zoomPane grows from zoomFrom to the note area (or back).
	zoomPane *notePane
	zoomFrom rect
	// revealDir is the folder whose rows last staggered in.
	revealDir string
	// pillFrom is the label the pill morphs away from; pillMode the
	// mode that is showing now.
	pillFrom, pillMode string

	// breathe counts frames while a book syncs (spinner phase).
	breathe int
	// syncing is set by the model from the book state.
	syncing bool
	// spinning: work in progress on a card (clone, sign-in, server
	// check) — the spinner breathes like the sync dot.
	spinning bool
	// ticking: a frame tick is in flight; at most one at a time.
	ticking bool
	// frames counts scheduled ticks (tests assert idle = no ticks).
	frames int
}

// motionReducedByEnv reports NO_MOTION / REDUCE_MOTION (any value but
// "", "0", "false", "off").
func motionReducedByEnv() bool {
	for _, k := range []string{"NO_MOTION", "REDUCE_MOTION"} {
		switch strings.ToLower(strings.TrimSpace(os.Getenv(k))) {
		case "", "0", "false", "off", "no":
		default:
			return true
		}
	}
	return false
}

func newMotion(reduced bool) *motion {
	return &motion{
		reduced: reduced || motionReducedByEnv(),
		springs: newSprings(),
	}
}

// setReduced applies the setting (env always wins).
func (mo *motion) setReduced(on bool) {
	if mo == nil {
		return
	}
	mo.reduced = on || motionReducedByEnv()
	if mo.reduced {
		mo.settle()
	}
}

// to animates k toward target, starting at from (NaN: where it is).
// nil-safe: a model without motion has nothing to animate.
func (mo *motion) to(k animKind, from, target float64) {
	if mo == nil {
		return
	}
	a := &mo.a[k]
	if !math.IsNaN(from) {
		a.pos, a.vel = from, 0
	}
	a.target = target
	if mo.reduced || a.pos == target && a.vel == 0 {
		a.pos, a.vel, a.active = target, 0, false
		return
	}
	a.active = true
}

func (mo *motion) all() []*anim {
	out := make([]*anim, animCount)
	for i := range mo.a {
		out[i] = &mo.a[i]
	}
	return out
}

// get is k's state; ok=false without motion (callers then draw the
// settled state).
func (mo *motion) get(k animKind) (anim, bool) {
	if mo == nil {
		return anim{}, false
	}
	return mo.a[k], true
}

// springing: some spring has not settled.
func (mo *motion) springing() bool {
	for _, a := range mo.all() {
		if a.active {
			return true
		}
	}
	return false
}

// busy: a frame is wanted (a spring moves, or the sync dot breathes).
func (mo *motion) busy() bool {
	if mo == nil || mo.reduced {
		return false
	}
	return mo.springing() || mo.syncing || mo.spinning
}

// settle snaps everything to its target.
func (mo *motion) settle() {
	if mo == nil {
		return
	}
	for _, a := range mo.all() {
		a.pos, a.vel, a.active = a.target, 0, false
	}
	mo.splitPane = nil
	mo.zoomPane = nil
}

// kick schedules the next frame when something moves and no frame is
// in flight. nil when idle: that is the "zero ticks when idle" rule.
func (mo *motion) kick() tea.Cmd {
	if mo == nil || mo.ticking || !mo.busy() {
		return nil
	}
	mo.ticking = true
	mo.frames++
	d := frameInterval
	if !mo.springing() {
		d = breatheInterval
	}
	return tea.Tick(d, func(time.Time) tea.Msg { return frameMsg{} })
}

// frame advances one step and schedules the next one if needed.
func (mo *motion) frame() tea.Cmd {
	if mo == nil {
		return nil
	}
	mo.ticking = false
	if mo.reduced {
		mo.settle()
		return nil
	}
	sp := mo.springing()
	for k := range mo.a {
		mo.a[k].step(mo.springs[animSpring[k]])
	}
	if !mo.a[animSplit].active {
		mo.splitPane = nil
	}
	if !mo.a[animZoom].active {
		mo.zoomPane = nil
	}
	if mo.syncing || mo.spinning {
		if sp {
			mo.breathe++ // 60 fps: the spinner advances every 5th frame
		} else {
			mo.breathe += breatheEvery
		}
	}
	return mo.kick()
}

// value is a's current position clamped to [lo, hi].
func (a anim) value(lo, hi float64) float64 { return math.Max(lo, math.Min(hi, a.pos)) }

// lerp interpolates integers.
func lerp(a, b int, t float64) int { return a + int(math.Round(float64(b-a)*t)) }

// breatheGlyph is the sync spinner frame (braille dots).
var breatheFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

func (mo *motion) breatheGlyph() string {
	if mo == nil || mo.reduced {
		return ""
	}
	return breatheFrames[(mo.breathe/breatheEvery)%len(breatheFrames)]
}
