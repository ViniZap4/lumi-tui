package books

// Sync status per book — SPEC-LIVROS §4. Status is a pure function of the
// book, its account's feed, the catch-up loop, the editor session and the
// account's REST health, with the spec's fixed precedence:
//
//	Não ligado → Precisa entrar → Erro → Offline → Sincronizando →
//	Somente leitura → Atualizado

// State is one of the seven book states.
type State int

const (
	StateLocal    State = iota // Não ligado / Local
	StateSignIn                // Precisa entrar / Sign in
	StateError                 // Erro / Error
	StateOffline               // Offline
	StateSyncing               // Sincronizando / Syncing
	StateReadOnly              // Somente leitura / Read-only
	StateUpToDate              // Atualizado / Up to date
)

// Label returns the short pt-BR UI label (lowercase, as the TUI shows it).
func (s State) Label() string {
	switch s {
	case StateLocal:
		return "não ligado"
	case StateSignIn:
		return "precisa entrar"
	case StateError:
		return "erro"
	case StateOffline:
		return "offline"
	case StateSyncing:
		return "sincronizando"
	case StateReadOnly:
		return "somente leitura"
	default:
		return "atualizado"
	}
}

// Glyph is the status-line indicator.
func (s State) Glyph() string {
	switch s {
	case StateLocal:
		return "▫"
	case StateSignIn:
		return "●" // amber
	case StateError:
		return "●" // red
	case StateOffline:
		return "◌"
	case StateSyncing:
		return "↻"
	case StateReadOnly:
		return "⊘" // lock: kept single-width for the status line
	default:
		return "●" // green
	}
}

// FeedState is the per-account feed connection state (§4.2).
type FeedState int

const (
	FeedConnecting FeedState = iota
	FeedOpen                 // after `hello`
	FeedBackoff
	FeedAuthDead
	FeedDisabled
)

func (f FeedState) String() string {
	return [...]string{"connecting", "open", "backoff", "auth_dead", "disabled"}[f]
}

// CatchUpPhase is the `changes` loop state.
type CatchUpPhase int

const (
	CatchUpIdle CatchUpPhase = iota
	CatchUpRunning
	CatchUpFailed
)

// CatchUp carries the `changes` loop state plus cursor/head.
type CatchUp struct {
	Phase    CatchUpPhase
	Failures int    // consecutive non-transient failures (Failed only)
	Reason   string // human-readable, for the details panel
	Cursor   int64
	Head     int64
}

// EditorState is the CRDT session state (CRDT-CLIENT-PROTOCOL §2.1).
type EditorState int

const (
	EditorClosed EditorState = iota
	EditorConnecting
	EditorSyncing
	EditorLive
	EditorOffline
)

// Editor carries the editor session and the "pending" flag: local edits
// not yet confirmed by the server. In the TUI that is a `.base` in
// `.lumi/cache` whose push has not returned 2xx (CRDT §7.1).
type Editor struct {
	State   EditorState
	Pending bool
}

// RestHealth is the last REST outcome for the account.
type RestHealth struct {
	NetworkError bool // a network error sends the book Offline at once
	Unauthorized bool // a 401 on REST
	Failures     int  // consecutive non-transient REST failures (5xx, bad JSON)
}

// StatusInput is everything bookStatus depends on.
type StatusInput struct {
	Book         Book
	HasAccount   bool // an account for (server_url, account) exists in this client
	TokenPresent bool
	Revoked      bool   // `vault.revoked` received, or absent from `hello`
	RevokeReason string // removed | deleted | severed_unreadable
	CanEdit      bool
	Feed         FeedState
	CatchUp      CatchUp
	Editor       Editor
	Rest         RestHealth
}

// ErrorThreshold is the number of consecutive non-transient failures
// that turn a book to Erro (§4.1).
const ErrorThreshold = 3

// Status is the result: the state plus a readable reason for Erro.
type Status struct {
	State  State
	Reason string
}

// BookStatus computes the state with the §4.1 precedence.
func BookStatus(in StatusInput) Status {
	b := in.Book
	// 1. Não ligado
	if !b.IsBound() {
		return Status{State: StateLocal}
	}
	// 2. Precisa entrar
	if !in.HasAccount || !in.TokenPresent || in.Feed == FeedAuthDead || in.Rest.Unauthorized {
		return Status{State: StateSignIn}
	}
	// 3. Erro
	if b.VaultID == "" {
		return Status{State: StateError, Reason: "ligação incompleta"}
	}
	if in.Revoked {
		reason := "sem acesso a este livro"
		if in.RevokeReason != "" {
			reason += " (" + in.RevokeReason + ")"
		}
		return Status{State: StateError, Reason: reason}
	}
	if in.CatchUp.Phase == CatchUpFailed && in.CatchUp.Failures >= ErrorThreshold {
		return Status{State: StateError, Reason: in.CatchUp.Reason}
	}
	if in.Rest.Failures >= ErrorThreshold {
		return Status{State: StateError, Reason: "o servidor está respondendo com erro"}
	}
	// 4. Offline: any feed state other than open — including `disabled`
	// (server without /api/events): with no feed and no polling the client
	// cannot know it is up to date (web case table).
	if in.Feed != FeedOpen || in.Rest.NetworkError {
		return Status{State: StateOffline}
	}
	// 5. Sincronizando (an editor OFFLINE while the feed is open is
	// reconnecting, hence syncing — web case table)
	if in.CatchUp.Phase == CatchUpRunning || in.CatchUp.Head > in.CatchUp.Cursor ||
		in.Editor.State == EditorConnecting || in.Editor.State == EditorSyncing ||
		in.Editor.State == EditorOffline || in.Editor.Pending {
		return Status{State: StateSyncing}
	}
	// 6. Somente leitura
	if !in.CanEdit {
		return Status{State: StateReadOnly}
	}
	// 7. Atualizado
	return Status{State: StateUpToDate}
}
