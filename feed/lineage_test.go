package feed

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/vinizap/lumi/tui-client/filesystem"
	"github.com/vinizap/lumi/tui-client/internal/fakeserver"
)

// CRDT-CLIENT-PROTOCOL §12.5 against the fake server (httptest).

func (e *pushEnv) conflictCopies() []string {
	e.t.Helper()
	m, _ := filepath.Glob(filepath.Join(e.root, "*"+ConflictSuffix+"*.md"))
	return m
}

func TestLineageSameEpochMergesWithCheapProbe(t *testing.T) {
	e := newPushEnv(t)
	e.srv.AddNote("a", "a.md", "hello world")
	e.pull("a", "a.md")
	if b, _ := e.s.Bases.Load("a"); b.CRDTEpoch != e.srv.Epoch("a") || b.CRDTEpoch == "" {
		t.Fatalf("base epoch = %q, server %q", b.CRDTEpoch, e.srv.Epoch("a"))
	}
	snaps := e.srv.Snapshots()
	e.srv.RemoteEdit("a", "# hello world")
	e.editLocal("a.md", "hello brave world")
	res := e.push("a.md")
	if res.Outcome != PushMerged || res.Lineage {
		t.Fatalf("res = %+v", res)
	}
	if e.srv.StateVectors() != 1 || e.srv.Snapshots() != snaps {
		t.Fatalf("probe used %d state-vector / %d extra snapshots; want 1 / 0",
			e.srv.StateVectors(), e.srv.Snapshots()-snaps)
	}
	if got := e.srv.Text("a"); got != "# hello brave world" {
		t.Fatalf("server text = %q", got)
	}
}

// The note was deleted and recreated while the TUI was offline: the
// edit must not be merged into the new history.
func TestLineageChangedSavesOfflineConflictCopy(t *testing.T) {
	e := newPushEnv(t)
	e.srv.AddNote("a", "a.md", "hello world")
	e.pull("a", "a.md")
	old := e.srv.Epoch("a")
	e.srv.Recreate("a", "brand new text")
	e.editLocal("a.md", "hello offline world")

	res := e.push("a.md")
	if res.Outcome != PushConflict || !res.Lineage || res.ConflictPath == "" {
		t.Fatalf("res = %+v", res)
	}
	if len(e.srv.Diffs()) != 0 {
		t.Fatalf("a base from the old lineage reached /diff: %+v", e.srv.Diffs())
	}
	if got := e.srv.Text("a"); got != "brand new text" {
		t.Fatalf("server text changed: %q", got)
	}
	if n := e.read("a.md"); n.Content != "brand new text" || n.ID != "a" {
		t.Fatalf("note = %+v", n)
	}
	cp, err := filesystem.ReadNote(filepath.Join(e.root, res.ConflictPath))
	if err != nil {
		t.Fatal(err)
	}
	if cp.Content != "hello offline world" || cp.ID == "a" || !strings.HasSuffix(cp.Title, " (conflito offline)") {
		t.Fatalf("copy = %+v", cp)
	}
	b, ok := e.s.Bases.Load("a")
	if !ok || b.CRDTEpoch == old || b.CRDTEpoch != e.srv.Epoch("a") || b.Text != "brand new text" {
		t.Fatalf("base not refreshed: %+v", b)
	}
	if p := e.s.Pending(); len(p) != 0 {
		t.Fatalf("pending = %v", p)
	}
}

func TestLineageChangedSameTextNoCopy(t *testing.T) {
	e := newPushEnv(t)
	e.srv.AddNote("a", "a.md", "hello")
	e.pull("a", "a.md")
	e.srv.Recreate("a", "same text")
	e.editLocal("a.md", "same text")
	res := e.push("a.md")
	if res.Outcome != PushNoop || res.Lineage || len(e.conflictCopies()) != 0 {
		t.Fatalf("res = %+v, copies %v", res, e.conflictCopies())
	}
	if b, _ := e.s.Bases.Load("a"); b.CRDTEpoch != e.srv.Epoch("a") {
		t.Fatalf("base epoch %q, want %q", b.CRDTEpoch, e.srv.Epoch("a"))
	}
}

// A base saved before the server had epochs: the state-vector fallback
// accepts it when the server's sv covers it, and adopts the epoch.
func TestLineageLegacyBaseAdoptsEpochWhenCovered(t *testing.T) {
	e := newPushEnv(t)
	e.srv.AddNote("a", "a.md", "hello world")
	e.pull("a", "a.md")
	b, _ := e.s.Bases.Load("a")
	b.CRDTEpoch = ""
	if err := e.s.Bases.Save(*b); err != nil {
		t.Fatal(err)
	}
	e.srv.RemoteEdit("a", "# hello world")
	e.editLocal("a.md", "hello brave world")
	if res := e.push("a.md"); res.Outcome != PushMerged {
		t.Fatalf("res = %+v", res)
	}
	if got := e.srv.Text("a"); got != "# hello brave world" {
		t.Fatalf("server text = %q", got)
	}
	if b, _ := e.s.Bases.Load("a"); b.CRDTEpoch != e.srv.Epoch("a") {
		t.Fatalf("epoch not adopted: %+v", b)
	}
}

func TestLineageLegacyBaseRecreatedIsChanged(t *testing.T) {
	e := newPushEnv(t)
	e.srv.AddNote("a", "a.md", "hello world")
	e.pull("a", "a.md")
	b, _ := e.s.Bases.Load("a")
	b.CRDTEpoch = ""
	_ = e.s.Bases.Save(*b)
	e.srv.Recreate("a", "other")
	e.editLocal("a.md", "hello offline")
	res := e.push("a.md")
	if res.Outcome != PushConflict || !res.Lineage || len(e.srv.Diffs()) != 0 {
		t.Fatalf("res = %+v, diffs %d", res, len(e.srv.Diffs()))
	}
}

// Servers before 0008: no epoch anywhere → the push behaves as before.
func TestLineagePreEpochServerMerges(t *testing.T) {
	e := newPushEnv(t)
	e.srv.NoEpochs = true
	e.srv.AddNote("a", "a.md", "hello world")
	e.pull("a", "a.md")
	e.srv.RemoteEdit("a", "# hello world")
	e.editLocal("a.md", "hello brave world")
	if res := e.push("a.md"); res.Outcome != PushMerged {
		t.Fatalf("res = %+v", res)
	}
}

// No /state-vector route: the probe falls back to /snapshot and still
// refuses a changed lineage (and reuses that snapshot).
func TestLineageFallsBackToSnapshot(t *testing.T) {
	e := newPushEnv(t)
	e.srv.NoStateVector = true
	e.srv.AddNote("a", "a.md", "hello world")
	e.pull("a", "a.md")
	e.srv.Recreate("a", "new")
	e.editLocal("a.md", "mine")
	snaps := e.srv.Snapshots()
	res := e.push("a.md")
	if res.Outcome != PushConflict || !res.Lineage {
		t.Fatalf("res = %+v", res)
	}
	if got := e.srv.Snapshots() - snaps; got != 1 {
		t.Fatalf("snapshots = %d, want 1 (fallback reused)", got)
	}
}

// The note is recreated between the probe and the /diff: the 409
// base_mismatch re-pull sees a new epoch and must not retry the merge.
func TestLineageChangeDuringPushNeverRetriesMerge(t *testing.T) {
	e := newPushEnv(t)
	e.srv.AddNote("a", "a.md", "hello world")
	e.pull("a", "a.md")
	e.srv.OnDiff = func(fakeserver.DiffRequest) {
		e.srv.OnDiff = nil
		e.srv.Recreate("a", "hello world") // same text, new history
	}
	e.editLocal("a.md", "hello brave world")
	res := e.push("a.md")
	if res.Outcome != PushConflict || !res.Lineage {
		t.Fatalf("res = %+v", res)
	}
	if n := len(e.srv.Diffs()); n != 1 {
		t.Fatalf("diffs = %d, want 1 (no retry into the new lineage)", n)
	}
	if got := e.srv.Text("a"); got != "hello world" {
		t.Fatalf("server text = %q", got)
	}
}

func TestJudgeLineageTable(t *testing.T) {
	sv := func(m map[uint64]uint64) string { return EncodeStateVector(m) }
	cases := []struct {
		name  string
		base  Base
		srv   ServerLineage
		want  Lineage
		adopt bool
	}{
		{"same", Base{CRDTEpoch: "E"}, ServerLineage{Epoch: "E"}, LineageSame, false},
		{"changed", Base{CRDTEpoch: "E1"}, ServerLineage{Epoch: "E2"}, LineageChanged, false},
		{"server pre-epoch", Base{CRDTEpoch: "E"}, ServerLineage{}, LineageSame, false},
		{"legacy covered", Base{VectorClock: sv(map[uint64]uint64{7: 3})}, ServerLineage{Epoch: "E", StateVector: sv(map[uint64]uint64{7: 5, 9: 1})}, LineageSame, true},
		{"legacy not covered", Base{VectorClock: sv(map[uint64]uint64{7: 3})}, ServerLineage{Epoch: "E", StateVector: sv(map[uint64]uint64{8: 9})}, LineageChanged, false},
		{"legacy garbage sv", Base{VectorClock: "!!"}, ServerLineage{Epoch: "E", StateVector: sv(nil)}, LineageChanged, false},
	}
	for _, c := range cases {
		got, adopt := JudgeLineage(&c.base, &c.srv)
		if got != c.want || adopt != c.adopt {
			t.Errorf("%s: got %v/%v, want %v/%v", c.name, got, adopt, c.want, c.adopt)
		}
	}
}

func TestStateVectorRoundTrip(t *testing.T) {
	in := map[uint64]uint64{1: 0, 300: 128, 1 << 40: 1<<32 + 5}
	out, err := decodeStateVector(EncodeStateVector(in))
	if err != nil || len(out) != len(in) {
		t.Fatalf("out = %v, err = %v", out, err)
	}
	for k, v := range in {
		if out[k] != v {
			t.Fatalf("client %d: %d != %d", k, out[k], v)
		}
	}
	if _, err := decodeStateVector("AgE="); err == nil { // count 2, truncated
		t.Fatal("truncated sv accepted")
	}
	if got := EncodeStateVector(in); got != fakeserver.EncodeSV(in) {
		t.Fatalf("encoders disagree: %s vs %s", got, fakeserver.EncodeSV(in))
	}
}
