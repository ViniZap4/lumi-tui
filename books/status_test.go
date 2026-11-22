package books

import "testing"

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
		{"feed disabled (servidor sem /api/events) conta como desconectado", with(func(i *StatusInput) { i.Feed = FeedDisabled }), StateOffline},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := BookStatus(c.in).State; got != c.want {
				t.Errorf("BookStatus = %s, want %s", got.Label(), c.want.Label())
			}
		})
	}
}
