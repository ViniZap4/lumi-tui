package books

import (
	"context"
	"fmt"
	"net/http"

	"github.com/vinizap/lumi/tui-client/account"
)

// FetchListings calls `GET /api/vaults` for each account. Accounts that
// fail (offline, 401) are reported in errs and left out of the listings,
// so Reconcile never mistakes an unreachable account for revoked access.
func FetchListings(ctx context.Context, accts []account.Account, hc *http.Client) (listings []Listing, errs []error) {
	for _, a := range accts {
		c, err := account.NewClient(a.Server)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s@%s: %w", a.Username, a.Server, err))
			continue
		}
		c.SetToken(a.Token)
		if hc != nil {
			c.SetHTTPClient(hc)
		}
		vs, err := c.ListVaults(ctx)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s@%s: %w", a.Username, a.Server, err))
			continue
		}
		l := Listing{ServerURL: a.Server, Account: a.Username, AccountAddedAt: a.AddedAt}
		for _, v := range vs {
			rv := RemoteVault{VaultID: v.ID, Name: v.Name}
			if v.Federation != nil {
				rv.FederationRole = v.Federation.Role
			}
			l.Vaults = append(l.Vaults, rv)
		}
		listings = append(listings, l)
	}
	return listings, errs
}
