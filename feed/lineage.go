package feed

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"

	"github.com/vinizap/lumi/tui-client/account"
)

// StateVectorAPI is the optional cheap lineage probe (GET
// …/state-vector, CRDT-CLIENT-PROTOCOL §12.4). *account.Client
// implements it; a NoteAPI without it skips the pre-push lineage check.
type StateVectorAPI interface {
	GetStateVector(ctx context.Context, vaultID, noteID string) (*account.NoteStateVector, error)
}

// Lineage is the verdict of the §12.5 check for a stored base.
type Lineage int

const (
	// LineageSame: the base descends from the server's current CRDT
	// history (or the server predates epochs): a 3-way merge is safe.
	LineageSame Lineage = iota
	// LineageChanged: the note's history was replaced (delete +
	// recreate, re-seed). The base must never be merged into it.
	LineageChanged
)

// ServerLineage is what the server said about a note's history.
type ServerLineage struct {
	Epoch       string
	StateVector string // base64 lib0-v1
	// Snap is set when the probe had to fall back to /snapshot (old
	// server without /state-vector); callers reuse it.
	Snap *account.NoteSnapshot
}

// probeLineage asks the server for the note's epoch and state vector,
// preferring /state-vector (no text, no full state) and falling back to
// /snapshot on servers without the route.
func (s *Syncer) probeLineage(ctx context.Context, id string) (*ServerLineage, error) {
	if sv, ok := s.API.(StateVectorAPI); ok {
		r, err := sv.GetStateVector(ctx, s.VaultID, id)
		if err == nil {
			return &ServerLineage{Epoch: r.CRDTEpoch, StateVector: r.StateVector}, nil
		}
		if !errors.Is(err, account.ErrStateVectorUnsupported) {
			return nil, err
		}
	}
	snap, err := s.fetch(ctx, id)
	if err != nil {
		return nil, err
	}
	return &ServerLineage{Epoch: snap.CRDTEpoch, StateVector: snap.VectorClock, Snap: snap}, nil
}

// JudgeLineage applies the §12.5 table to a stored base and the
// server's answer:
//
//	base E,  server E  → same
//	base E1, server E2 → changed
//	server without epoch (pre-0008) → same (nothing to compare)
//	base without epoch (saved before 0008) → fallback: same only when
//	  the server's state vector covers every clock the base had; the
//	  caller then adopts the server's epoch.
//
// adopt reports that fallback adoption.
func JudgeLineage(base *Base, srv *ServerLineage) (verdict Lineage, adopt bool) {
	if srv.Epoch == "" {
		return LineageSame, false
	}
	if base.CRDTEpoch != "" {
		if base.CRDTEpoch == srv.Epoch {
			return LineageSame, false
		}
		return LineageChanged, false
	}
	covered, err := svCovers(srv.StateVector, base.VectorClock)
	if err != nil || !covered {
		return LineageChanged, false
	}
	return LineageSame, true
}

// svCovers reports whether state vector `outer` has, for every client
// in `inner`, a clock >= inner's (both base64 lib0-v1).
func svCovers(outerB64, innerB64 string) (bool, error) {
	outer, err := decodeStateVector(outerB64)
	if err != nil {
		return false, err
	}
	inner, err := decodeStateVector(innerB64)
	if err != nil {
		return false, err
	}
	for client, clock := range inner {
		if outer[client] < clock {
			return false, nil
		}
	}
	return true, nil
}

// decodeStateVector parses a lib0-v1 state vector: varuint count, then
// count × (varuint client, varuint clock).
func decodeStateVector(b64 string) (map[uint64]uint64, error) {
	raw, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		return nil, fmt.Errorf("state vector: %w", err)
	}
	if len(raw) == 0 {
		return map[uint64]uint64{}, nil
	}
	pos := 0
	n, err := readVarUint(raw, &pos)
	if err != nil {
		return nil, err
	}
	if n > uint64(len(raw)) {
		return nil, fmt.Errorf("state vector: bad length %d", n)
	}
	out := make(map[uint64]uint64, n)
	for i := uint64(0); i < n; i++ {
		client, err := readVarUint(raw, &pos)
		if err != nil {
			return nil, err
		}
		clock, err := readVarUint(raw, &pos)
		if err != nil {
			return nil, err
		}
		out[client] = clock
	}
	if pos != len(raw) {
		return nil, fmt.Errorf("state vector: %d trailing bytes", len(raw)-pos)
	}
	return out, nil
}

func readVarUint(b []byte, pos *int) (uint64, error) {
	var v uint64
	for shift := uint(0); shift < 64; shift += 7 {
		if *pos >= len(b) {
			return 0, fmt.Errorf("state vector: truncated varuint")
		}
		c := b[*pos]
		*pos++
		v |= uint64(c&0x7f) << shift
		if c < 0x80 {
			return v, nil
		}
	}
	return 0, fmt.Errorf("state vector: varuint overflow")
}

// EncodeStateVector is the inverse of decodeStateVector (tests and the
// fake server). Clients are written in ascending order.
func EncodeStateVector(sv map[uint64]uint64) string {
	keys := make([]uint64, 0, len(sv))
	for k := range sv {
		keys = append(keys, k)
	}
	for i := 1; i < len(keys); i++ {
		for j := i; j > 0 && keys[j] < keys[j-1]; j-- {
			keys[j], keys[j-1] = keys[j-1], keys[j]
		}
	}
	buf := appendVarUint(nil, uint64(len(keys)))
	for _, k := range keys {
		buf = appendVarUint(buf, k)
		buf = appendVarUint(buf, sv[k])
	}
	return base64.StdEncoding.EncodeToString(buf)
}

func appendVarUint(b []byte, v uint64) []byte {
	for v >= 0x80 {
		b = append(b, byte(v)|0x80)
		v >>= 7
	}
	return append(b, byte(v))
}
