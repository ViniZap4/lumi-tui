package books

import (
	"testing"
	"time"
)

var t0 = time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)

func TestReconcileSingleAccountAddsSilently(t *testing.T) {
	r := &Registry{}
	res := Reconcile(r, []Listing{{ServerURL: "https://lumi.work.com", Account: "alice", Vaults: []RemoteVault{
		{VaultID: "V2", Name: "Zeta"}, {VaultID: "v1", Name: "Alpha"},
	}}}, t0, nil)
	if len(res.Added) != 2 || len(res.Banners) != 0 {
		t.Fatalf("res %+v", res)
	}
	if r.Books[0].Name != "Alpha" || r.Books[0].ID != "v1" || r.Books[0].Path != "" || r.Books[0].Sort != 10 {
		t.Errorf("name order / id = vault_id: %+v", r.Books[0])
	}
	if res.AddedByServer["https://lumi.work.com"] != 2 {
		t.Errorf("count %+v", res.AddedByServer)
	}
}

func TestReconcileFederatedPrefersHome(t *testing.T) {
	r := &Registry{}
	res := Reconcile(r, []Listing{
		{ServerURL: "https://notes.friend.org", Account: "a", AccountAddedAt: t0.Add(-48 * time.Hour),
			Vaults: []RemoteVault{{VaultID: "v", Name: "Shared", FederationRole: "follower"}}},
		{ServerURL: "https://lumi.work.com", Account: "alice", AccountAddedAt: t0,
			Vaults: []RemoteVault{{VaultID: "v", Name: "Shared", FederationRole: "home"}}},
	}, t0, nil)
	if len(r.Books) != 1 {
		t.Fatalf("federated copy must not duplicate: %d books", len(r.Books))
	}
	b := r.Books[0]
	if b.ServerURL != "https://lumi.work.com" || b.Account != "alice" {
		t.Errorf("should bind to home: %+v", b)
	}
	if len(b.Alternates) != 1 || b.Alternates[0].ServerURL != "https://notes.friend.org" {
		t.Errorf("alternates %+v", b.Alternates)
	}
	if len(res.Banners) != 1 || res.Banners[0].Text() != "Este livro também está em notes.friend.org. Ele sincroniza por lumi.work.com." {
		t.Errorf("banner %+v", res.Banners)
	}
}

func TestReconcileNoHomeUsesOldestAccount(t *testing.T) {
	r := &Registry{}
	Reconcile(r, []Listing{
		{ServerURL: "https://b", Account: "u", AccountAddedAt: t0, Vaults: []RemoteVault{{VaultID: "v", Name: "S", FederationRole: "follower"}}},
		{ServerURL: "https://a", Account: "u", AccountAddedAt: t0.Add(-time.Hour), Vaults: []RemoteVault{{VaultID: "v", Name: "S", FederationRole: "follower"}}},
	}, t0, nil)
	if r.Books[0].ServerURL != "https://a" {
		t.Errorf("want oldest account, got %s", r.Books[0].ServerURL)
	}
}

func TestReconcileKeepsExistingBindingAndDetectsMissing(t *testing.T) {
	r := &Registry{}
	r.Add(Book{ID: "b1", Name: "Old", Kind: KindBound, ServerURL: "https://f", Account: "u", VaultID: "v"})
	r.Add(Book{ID: "b2", Name: "Gone", Kind: KindBound, ServerURL: "https://f", Account: "u", VaultID: "w"})
	res := Reconcile(r, []Listing{
		{ServerURL: "https://h", Account: "u", Vaults: []RemoteVault{{VaultID: "v", Name: "New", FederationRole: "home"}}},
		{ServerURL: "https://f", Account: "u", Vaults: []RemoteVault{{VaultID: "v", Name: "New", FederationRole: "follower"}}},
	}, t0, nil)
	b := r.Get("b1")
	if b.ServerURL != "https://f" {
		t.Error("existing binding must not move automatically")
	}
	if b.Name != "New" || len(res.Renamed) != 1 {
		t.Errorf("name cache not refreshed: %+v", b)
	}
	if len(b.Alternates) != 1 || b.Alternates[0].ServerURL != "https://h" {
		t.Errorf("alternates %+v", b.Alternates)
	}
	if len(res.Missing) != 1 || res.Missing[0] != "b2" || r.Get("b2") == nil {
		t.Errorf("missing %+v (book must be kept)", res.Missing)
	}
}

func TestSwitchServer(t *testing.T) {
	b := Book{Kind: KindBound, ServerURL: "https://f", Account: "u", Alternates: []Alternate{{ServerURL: "https://h", Account: "u", Role: "home"}}}
	b.SwitchServer("https://h", "u")
	if b.ServerURL != "https://h" || len(b.Alternates) != 1 || b.Alternates[0].ServerURL != "https://f" {
		t.Errorf("%+v", b)
	}
}
