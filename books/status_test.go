package books

import (
	"encoding/json"
	"os"
	"strconv"
	"strings"
	"testing"
)

// The case names quote SPEC-LIVROS §4.1 verbatim (the "Condição" column
// and the precedence line). The spec gives conditions, not literal input
// vectors, so each row below is the minimal input that satisfies the
// quoted condition; the precedence rows combine two conditions and assert
// the earlier state wins. Keep these names identical across clients.
func TestBookStatusTable(t *testing.T) {
	bound := Book{ID: "b", Kind: KindBound, ServerURL: "https://lumi.work.com", Account: "alice", VaultID: "v"}
	// healthy: Feed conectado, cursor = head, editor LIVE ou fechado, nada pendente.
	ok := StatusInput{Book: bound, HasAccount: true, TokenPresent: true, CanEdit: true, Feed: FeedOpen,
		CatchUp: CatchUp{Phase: CatchUpIdle, Cursor: 42, Head: 42}, Editor: Editor{State: EditorLive}}
	with := func(f func(*StatusInput)) StatusInput { in := ok; f(&in); return in }

	cases := []struct {
		name string
		in   StatusInput
		want State
	}{
		// Não ligado / Local
		{"kind: local", with(func(i *StatusInput) { i.Book = Book{ID: "l", Kind: KindLocal} }), StateLocal},
		// Precisa entrar / Sign in
		{"Livro sem conta neste cliente", with(func(i *StatusInput) { i.HasAccount = false }), StateSignIn},
		{"token ausente", with(func(i *StatusInput) { i.TokenPresent = false }), StateSignIn},
		{"401", with(func(i *StatusInput) { i.Rest.Unauthorized = true }), StateSignIn},
		{"session.ended", with(func(i *StatusInput) { i.Feed = FeedAuthDead }), StateSignIn},
		// Erro / Error
		{"3 falhas seguidas não transitórias (5xx, JSON inválido)", with(func(i *StatusInput) {
			i.CatchUp = CatchUp{Phase: CatchUpFailed, Failures: 3, Reason: "HTTP 500", Cursor: 1, Head: 2}
		}), StateError},
		{"vault_id nulo", with(func(i *StatusInput) { i.Book.VaultID = "" }), StateError},
		{"vault.revoked", with(func(i *StatusInput) { i.Revoked = true; i.RevokeReason = "removed" }), StateError},
		// Offline
		{"Feed da conta desconectado", with(func(i *StatusInput) { i.Feed = FeedBackoff }), StateOffline},
		{"ou rede indisponível", with(func(i *StatusInput) { i.Rest.NetworkError = true }), StateOffline},
		// Sincronizando / Syncing
		{"changes em andamento", with(func(i *StatusInput) { i.CatchUp.Phase = CatchUpRunning }), StateSyncing},
		{"head > cursor", with(func(i *StatusInput) { i.CatchUp.Head = 43 }), StateSyncing},
		{"editor ativo em CONNECTING/SYNCING", with(func(i *StatusInput) { i.Editor.State = EditorSyncing }), StateSyncing},
		{"edições locais ainda não confirmadas", with(func(i *StatusInput) { i.Editor.Pending = true }), StateSyncing},
		// Somente leitura / Read-only
		{"Atualizado e can_edit = false", with(func(i *StatusInput) { i.CanEdit = false }), StateReadOnly},
		// Atualizado / Up to date
		{"Feed conectado, cursor = head, editor LIVE ou fechado, nada pendente", ok, StateUpToDate},
		{"editor fechado", with(func(i *StatusInput) { i.Editor.State = EditorClosed }), StateUpToDate},

		// Precedence: "vence o primeiro que valer".
		{"Não ligado → Precisa entrar", with(func(i *StatusInput) { i.Book = Book{ID: "l", Kind: KindLocal}; i.HasAccount = false }), StateLocal},
		{"Precisa entrar → Erro", with(func(i *StatusInput) { i.Feed = FeedAuthDead; i.Revoked = true }), StateSignIn},
		{"Erro → Offline", with(func(i *StatusInput) { i.Revoked = true; i.Feed = FeedBackoff }), StateError},
		{"Offline → Sincronizando", with(func(i *StatusInput) { i.Feed = FeedConnecting; i.CatchUp.Head = 99 }), StateOffline},
		{"Sincronizando → Somente leitura", with(func(i *StatusInput) { i.CanEdit = false; i.Editor.Pending = true }), StateSyncing},
		{"Somente leitura → Atualizado", with(func(i *StatusInput) { i.CanEdit = false }), StateReadOnly},

		// Boundaries.
		{"2 falhas não bastam para Erro", with(func(i *StatusInput) {
			i.CatchUp = CatchUp{Phase: CatchUpFailed, Failures: 2, Cursor: 42, Head: 42}
		}), StateUpToDate},
		{"feed disabled (servidor sem /api/events) degrada para REST", with(func(i *StatusInput) { i.Feed = FeedDisabled }), StateUpToDate},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := BookStatus(c.in).State; got != c.want {
				t.Errorf("BookStatus = %s, want %s", got.Label(), c.want.Label())
			}
		})
	}
}

// TestBookStatusSharedCases runs the cross-client case table written with
// the Apple client (testdata/book-status.cases.json, copied verbatim from
// apple-client Tests/Fixtures/books). Each case's "spec" field quotes the
// SPEC-LIVROS §4.1 row it exercises.
func TestBookStatusSharedCases(t *testing.T) {
	data, err := os.ReadFile("testdata/book-status.cases.json")
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Cases []struct {
			Name string `json:"name"`
			Spec string `json:"spec"`
			Book struct {
				Kind       string `json:"kind"`
				HasAccount bool   `json:"has_account"`
				HasToken   bool   `json:"has_token"`
				HasVaultID bool   `json:"has_vault_id"`
				Revoked    bool   `json:"revoked"`
				CanEdit    bool   `json:"can_edit"`
			} `json:"book"`
			Feed    *string `json:"feed"`
			CatchUp struct {
				Phase    string  `json:"phase"`
				Failures int     `json:"failures"`
				Reason   *string `json:"reason"`
				Cursor   int64   `json:"cursor"`
				Head     *int64  `json:"head"`
			} `json:"catch_up"`
			Editor struct {
				Phase   string `json:"phase"`
				Pending bool   `json:"pending"`
			} `json:"editor"`
			Rest     string `json:"rest"`
			Expected string `json:"expected"`
		} `json:"cases"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	if len(doc.Cases) == 0 {
		t.Fatal("no cases")
	}
	feeds := map[string]FeedState{"connecting": FeedConnecting, "open": FeedOpen, "backoff": FeedBackoff, "auth_dead": FeedAuthDead, "disabled": FeedDisabled}
	phases := map[string]CatchUpPhase{"idle": CatchUpIdle, "running": CatchUpRunning, "failed": CatchUpFailed}
	editors := map[string]EditorState{"closed": EditorClosed, "connecting": EditorConnecting, "syncing": EditorSyncing, "live": EditorLive, "offline": EditorOffline}
	expected := map[string]State{"local": StateLocal, "needs_sign_in": StateSignIn, "error": StateError, "offline": StateOffline,
		"syncing": StateSyncing, "read_only": StateReadOnly, "up_to_date": StateUpToDate}
	for _, c := range doc.Cases {
		t.Run(c.Name, func(t *testing.T) {
			b := Book{ID: "b", Kind: Kind(c.Book.Kind), ServerURL: "https://s", Account: "u"}
			if c.Book.HasVaultID {
				b.VaultID = "8c5a1d9f-2e3b-4c4d-9e5f-6a7b8c9d0e1f"
			}
			in := StatusInput{Book: b, HasAccount: c.Book.HasAccount, TokenPresent: c.Book.HasToken,
				Revoked: c.Book.Revoked, CanEdit: c.Book.CanEdit, Feed: FeedDisabled}
			if c.Feed != nil {
				f, ok := feeds[*c.Feed]
				if !ok {
					t.Fatalf("unknown feed %q", *c.Feed)
				}
				in.Feed = f
			}
			in.CatchUp = CatchUp{Phase: phases[c.CatchUp.Phase], Failures: c.CatchUp.Failures, Cursor: c.CatchUp.Cursor, Head: c.CatchUp.Cursor}
			if c.CatchUp.Reason != nil {
				in.CatchUp.Reason = *c.CatchUp.Reason
			}
			if c.CatchUp.Head != nil { // null head = unknown: nothing known to be behind
				in.CatchUp.Head = *c.CatchUp.Head
			}
			in.Editor = Editor{State: editors[c.Editor.Phase], Pending: c.Editor.Pending}
			switch {
			case c.Rest == "ok":
			case c.Rest == "network":
				in.Rest.NetworkError = true
			case c.Rest == "unauthorized":
				in.Rest.Unauthorized = true
			case strings.HasPrefix(c.Rest, "failing:"):
				n, err := strconv.Atoi(strings.TrimPrefix(c.Rest, "failing:"))
				if err != nil {
					t.Fatal(err)
				}
				in.Rest.Failures = n
			default:
				t.Fatalf("unknown rest %q", c.Rest)
			}
			want, ok := expected[c.Expected]
			if !ok {
				t.Fatalf("unknown expected %q", c.Expected)
			}
			if got := BookStatus(in).State; got != want {
				t.Errorf("BookStatus = %s, want %s (spec: %s)", got.Label(), want.Label(), c.Spec)
			}
		})
	}
}
