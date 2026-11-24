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
//
// Reduce motion (Settings, or NO_MOTION / REDUCE_MOTION in the
// environment) snaps every value to its target: no frames are scheduled.

const (
	frameRate     = 60
	frameInterval = time.Second / frameRate
	// breatheInterval paces the sync spinner when nothing else moves
	// (10 fps is plenty for a breathing dot and keeps CPU low).
	breatheInterval = 100 * time.Millisecond
	// spring tuning: settles visually in ~200 ms with a slight
	// overshoot ("settle with a slight spring").
	springFreq    = 18.0
	springDamping = 0.72
	settleEps     = 0.004
)

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
	animCount
)

// motion is shared by Model copies (pointer), like the workspace.
type motion struct {
	reduced bool
	spring  harmonica.Spring

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
		spring:  harmonica.NewSpring(harmonica.FPS(frameRate), springFreq, springDamping),
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
	for _, a := range mo.all() {
		a.step(mo.spring)
	}
	if !mo.a[animSplit].active {
		mo.splitPane = nil
	}
	if !mo.a[animZoom].active {
		mo.zoomPane = nil
	}
	if mo.syncing || mo.spinning {
		if sp {
			mo.breathe++ // ~60 fps: advance the spinner every 6th frame
		} else {
			mo.breathe += 6
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
	return breatheFrames[(mo.breathe/6)%len(breatheFrames)]
}
