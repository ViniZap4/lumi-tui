package books

import (
	"net/url"
	"sort"
	"strings"
	"time"
)

// RemoteVault is the slice of a `GET /api/vaults` row that dedupe needs.
type RemoteVault struct {
	VaultID        string
	Name           string
	FederationRole string // "home", "follower" or ""
}

// Listing is one account's `GET /api/vaults` result.
type Listing struct {
	ServerURL      string
	Account        string
	AccountAddedAt time.Time // "oldest account" tie-break (§2.4.3)
	Vaults         []RemoteVault
}

// Banner is the one-time notice of §2.4.3: "Este livro também está em
// <others>. Ele sincroniza por <bound>."
type Banner struct {
	BookID      string
	BookName    string
	BoundServer string
	Others      []string
}

// Text renders the banner in pt-BR, as the spec words it.
func (b Banner) Text() string {
	return "Este livro também está em " + strings.Join(hosts(b.Others), ", ") +
		". Ele sincroniza por " + host(b.BoundServer) + "."
}

// ReconcileResult reports what Reconcile changed.
type ReconcileResult struct {
	Added   []string // ids of new books, in insertion order
	Renamed []string // ids whose cached name changed
	// Missing lists bound books whose own account was listed but no longer
	// sees the vault (state "revoked"); they are kept, never auto-removed.
	Missing []string
	Banners []Banner
	// AddedByServer counts new books per bound server, for the "3 livros
	// adicionados de lumi.work.com" notice.
	AddedByServer map[string]int
}

// Changed reports whether the registry needs saving.
func (r ReconcileResult) Changed() bool { return len(r.Added)+len(r.Renamed) > 0 }

type candidate struct {
	listing *Listing
	vault   RemoteVault
}

// Reconcile merges every account's vault listing into the registry,
// enforcing SPEC-LIVROS §2.4:
//
//  1. at most one bound book per vault_id;
//  2. a vault_id seen through one account only becomes a book silently;
//  3. a vault_id seen through several accounts binds to the account whose
//     server reports `federation.role = "home"`; failing that, to the
//     oldest account; a Banner is emitted;
//  4. the other origins become in-memory Alternates (no feed of their own).
//
// Existing bindings are never moved (that is the explicit "Trocar
// servidor" action, see SwitchServer). newID is used when a vault_id
// collides with an unrelated book id.
func Reconcile(reg *Registry, listings []Listing, now time.Time, newID func() string) ReconcileResult {
	res := ReconcileResult{AddedByServer: map[string]int{}}
	byVault := map[string][]candidate{}
	var order []string
	for i := range listings {
		l := &listings[i]
		for _, v := range l.Vaults {
			id := strings.ToLower(v.VaultID)
			if id == "" {
				continue
			}
			if _, ok := byVault[id]; !ok {
				order = append(order, id)
			}
			byVault[id] = append(byVault[id], candidate{listing: l, vault: v})
		}
	}

	// Revocation: a bound book whose own account was listed without it.
	for i := range reg.Books {
		b := &reg.Books[i]
		b.Alternates = nil
		if !b.IsBound() || b.VaultID == "" {
			continue
		}
		listed := false
		for _, l := range listings {
			if sameAccount(l.ServerURL, l.Account, b.ServerURL, b.Account) {
				listed = true
				break
			}
		}
		if !listed {
			continue
		}
		seen := false
		for _, c := range byVault[b.VaultID] {
			if sameAccount(c.listing.ServerURL, c.listing.Account, b.ServerURL, b.Account) {
				seen = true
				break
			}
		}
		if !seen {
			res.Missing = append(res.Missing, b.ID)
		}
	}

	// New books are added in name order (as the web does on first list).
	type pending struct {
		book   Book
		others []candidate
	}
	var adds []pending
	for _, vid := range order {
		cands := byVault[vid]
		if b := reg.ByVaultID(vid); b != nil {
			for _, c := range cands {
				if sameAccount(c.listing.ServerURL, c.listing.Account, b.ServerURL, b.Account) {
					if c.vault.Name != "" && c.vault.Name != b.Name {
						b.Name = c.vault.Name
						res.Renamed = append(res.Renamed, b.ID)
					}
					continue
				}
				b.Alternates = append(b.Alternates, Alternate{ServerURL: c.listing.ServerURL, Account: c.listing.Account, Role: c.vault.FederationRole})
			}
			continue
		}
		win := pickWinner(cands)
		id := vid
		if reg.Get(id) != nil && newID != nil {
			id = newID()
		}
		bk := Book{
			ID:        id,
			Name:      win.vault.Name,
			Kind:      KindBound,
			ServerURL: win.listing.ServerURL,
			Account:   win.listing.Account,
			VaultID:   vid,
			AddedAt:   now.UTC().Truncate(time.Second),
		}
		var others []candidate
		for _, c := range cands {
			if c.listing != win.listing {
				others = append(others, c)
				bk.Alternates = append(bk.Alternates, Alternate{ServerURL: c.listing.ServerURL, Account: c.listing.Account, Role: c.vault.FederationRole})
			}
		}
		adds = append(adds, pending{book: bk, others: others})
	}
	sort.SliceStable(adds, func(i, j int) bool {
		return strings.ToLower(adds[i].book.Name) < strings.ToLower(adds[j].book.Name)
	})
	for _, p := range adds {
		added, err := reg.Add(p.book)
		if err != nil {
			continue
		}
		res.Added = append(res.Added, added.ID)
		res.AddedByServer[added.ServerURL]++
		if len(p.others) > 0 {
			bn := Banner{BookID: added.ID, BookName: added.Name, BoundServer: added.ServerURL}
			for _, o := range p.others {
				bn.Others = append(bn.Others, o.listing.ServerURL)
			}
			res.Banners = append(res.Banners, bn)
		}
	}
	return res
}

func pickWinner(cands []candidate) candidate {
	for _, c := range cands {
		if c.vault.FederationRole == "home" {
			return c
		}
	}
	best := cands[0]
	for _, c := range cands[1:] {
		if !c.listing.AccountAddedAt.IsZero() && (best.listing.AccountAddedAt.IsZero() || c.listing.AccountAddedAt.Before(best.listing.AccountAddedAt)) {
			best = c
		}
	}
	return best
}

// SwitchServer rebinds a book to one of its alternates (§2.4.5). The
// caller must reset the book's cursor to 0 and drop its list cache;
// locally persisted Y.Docs / note files are kept.
func (b *Book) SwitchServer(serverURL, account string) {
	var rest []Alternate
	for _, a := range b.Alternates {
		if !sameAccount(a.ServerURL, a.Account, serverURL, account) {
			rest = append(rest, a)
		}
	}
	rest = append(rest, Alternate{ServerURL: b.ServerURL, Account: b.Account})
	b.ServerURL, b.Account, b.Alternates = serverURL, account, rest
}

func sameAccount(s1, a1, s2, a2 string) bool {
	return strings.EqualFold(strings.TrimRight(s1, "/"), strings.TrimRight(s2, "/")) && strings.EqualFold(a1, a2)
}

func host(s string) string {
	if u, err := url.Parse(s); err == nil && u.Host != "" {
		return u.Host + strings.TrimRight(u.Path, "/")
	}
	return s
}

func hosts(ss []string) []string {
	out := make([]string, len(ss))
	for i, s := range ss {
		out[i] = host(s)
	}
	return out
}

// Host returns the display host of a server URL ("lumi.work.com").
func Host(serverURL string) string { return host(serverURL) }
