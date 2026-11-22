package account

import "context"

// EditBase is what the user's edit was based on: the snapshot fetched
// before the edit started (CRDT-CLIENT-PROTOCOL §7.1 step 1). State is the
// Yjs state of that version, once the server exposes it (§10 S11).
type EditBase struct {
	Text        string
	VectorClock string
	State       []byte
}

// PushEdit is the single seam through which the TUI sends an edited note
// body to the server. Every push path (lumi vault sync today; the feed's
// pending-edit flush later) must go through here so the 3-way fix lands in
// one place.
//
// TODO(SPEC-LIVROS §6 T3, CRDT-CLIENT-PROTOCOL §7.1/§7.2): this still
// calls POST /diff with {text, base_clock}, which the server applies as a
// diff against its CURRENT text — concurrent remote edits the user never
// saw can be deleted (protocol vector V9a). Once the server's ApplyDiff
// honours the base (branch fix/wave1-security, protocol §10 S1), send
// {text, base_state: base64(base.State) | base_clock: base.VectorClock,
// origin: "tui"} so the server merges edited-vs-base. Until then, keep the
// .base cache + "pending" status (SPEC-LIVROS §4.2) unimplemented: there
// is nothing correct to retry.
func (c *Client) PushEdit(ctx context.Context, vaultID, noteID string, base EditBase, edited string) (*NoteSnapshot, error) {
	return c.ApplyNoteDiff(ctx, vaultID, noteID, base.VectorClock, edited, "tui-diff")
}
