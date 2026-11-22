package feed

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/vinizap/lumi/tui-client/account"
	"github.com/vinizap/lumi/tui-client/books"
)

// Manager runs one feed per account and keeps the per-book inputs of
// books.BookStatus up to date: it reconciles on `hello`, catches books up
// on `vault.head`, tracks access/revocation, and mirrors changes into
// book folders. One connection per account, never per book (§Performance).
type Manager struct {
	mu       sync.Mutex
	store    *books.Store
	accounts []account.Account
	cacheDir string

	// Hooks (tests override).
	ConnConfig func(a account.Account) ConnConfig
	API        func(a account.Account) (ChangesAPI, VaultAPI, error)
	ListAll    func(ctx context.Context, accts []account.Account) ([]books.Listing, []error)
	Now        func() time.Time

	feeds  map[string]*feedInfo
	bstate map[string]*bookInfo

	notify  chan struct{}
	notices []string
	ctx     context.Context
}

// VaultAPI is the REST surface for vault metadata refreshes.
type VaultAPI interface {
	GetVault(ctx context.Context, vaultID string) (*account.RemoteVault, error)
}

type feedInfo struct {
	state   books.FeedState
	err     error
	retryIn time.Duration
	rest    books.RestHealth
	hello   map[string]HelloVault
}

type bookInfo struct {
	catchUp    books.CatchUp
	revoked    bool
	reason     string
	canEdit    bool
	role       string
	lastSynced time.Time
	errs       []string
	dirty      []string
	running    bool
	again      bool
	mirrorGen  uint64
}

// BookView is a snapshot for the UI.
type BookView struct {
	Book       books.Book
	Status     books.Status
	Feed       books.FeedState
	FeedErr    string
	RetryIn    time.Duration
	Role       string
	Cursor     int64
	Head       int64
	LastSynced time.Time
	Errors     []string // last 5
	Dirty      []string // notes the mirror left alone
	MirrorGen  uint64   // bumps whenever the mirror changed files
}

// NewManager builds a manager over a loaded registry and accounts.
func NewManager(store *books.Store, accounts []account.Account) *Manager {
	dir, _ := CacheDir()
	m := &Manager{
		store:    store,
		accounts: accounts,
		cacheDir: dir,
		feeds:    map[string]*feedInfo{},
		bstate:   map[string]*bookInfo{},
		notify:   make(chan struct{}, 1),
		Now:      time.Now,
	}
	m.ConnConfig = func(a account.Account) ConnConfig {
		return ConnConfig{ServerURL: a.Server, Token: a.Token}
	}
	m.API = func(a account.Account) (ChangesAPI, VaultAPI, error) {
		c, err := account.NewClient(a.Server)
		if err != nil {
			return nil, nil, err
		}
		c.SetToken(a.Token)
		return c, c, nil
	}
	m.ListAll = func(ctx context.Context, accts []account.Account) ([]books.Listing, []error) {
		return books.FetchListings(ctx, accts, nil)
	}
	return m
}

// SetCacheDir overrides where per-book caches live (tests).
func (m *Manager) SetCacheDir(d string) { m.cacheDir = d }

func acctKey(server, user string) string {
	return strings.ToLower(strings.TrimRight(server, "/")) + "#" + strings.ToLower(user)
}

// Start opens one stream per account. Returns immediately.
func (m *Manager) Start(ctx context.Context) {
	m.mu.Lock()
	m.ctx = ctx
	for _, b := range m.store.Registry.Books {
		m.infoLocked(b)
	}
	accts := append([]account.Account(nil), m.accounts...)
	for _, a := range accts {
		m.feeds[acctKey(a.Server, a.Username)] = &feedInfo{state: books.FeedConnecting}
	}
	m.mu.Unlock()
	for _, a := range accts {
		a := a
		ch := make(chan Update, 16)
		go Run(ctx, m.ConnConfig(a), ch)
		go func() {
			for {
				select {
				case <-ctx.Done():
					return
				case u := <-ch:
					m.handle(a, u)
				}
			}
		}()
	}
	m.ping()
}

// Notify fires (coalesced) whenever any view may have changed.
func (m *Manager) Notify() <-chan struct{} { return m.notify }

func (m *Manager) ping() {
	select {
	case m.notify <- struct{}{}:
	default:
	}
}

// PopNotices returns and clears one-time user notices (new books,
// federation banners).
func (m *Manager) PopNotices() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := m.notices
	m.notices = nil
	return n
}

// Registry returns a copy of the books in picker order.
func (m *Manager) Registry() []books.Book {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]books.Book(nil), m.store.Registry.Books...)
}

func (m *Manager) infoLocked(b books.Book) *bookInfo {
	bi, ok := m.bstate[b.ID]
	if !ok {
		bi = &bookInfo{canEdit: true}
		if b.IsBound() && b.VaultID != "" {
			c := LoadCache(m.cacheDir, b.ID, b.VaultID, b.ServerURL)
			bi.catchUp.Cursor = c.Cursor
			bi.lastSynced = c.LastSyncedAt
		}
		m.bstate[b.ID] = bi
	}
	return bi
}

func (m *Manager) accountFor(b books.Book) (account.Account, bool) {
	for _, a := range m.accounts {
		if account.SameServer(a.Server, b.ServerURL) && strings.EqualFold(a.Username, b.Account) {
			return a, true
		}
	}
	return account.Account{}, false
}

// View computes the snapshot for one book (nil-safe for unknown ids).
func (m *Manager) View(bookID string) (BookView, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	b := m.store.Registry.Get(bookID)
	if b == nil {
		return BookView{}, false
	}
	bi := m.infoLocked(*b)
	// Editor stays zero (closed, nothing pending): the TUI has no live
	// CRDT session, and "pending" needs the .base push of
	// CRDT-CLIENT-PROTOCOL §7.1 — see the TODO on account.PushEdit.
	in := books.StatusInput{Book: *b, Revoked: bi.revoked, RevokeReason: bi.reason, CanEdit: bi.canEdit, CatchUp: bi.catchUp}
	v := BookView{Book: *b, Role: bi.role, Cursor: bi.catchUp.Cursor, Head: bi.catchUp.Head,
		LastSynced: bi.lastSynced, Errors: append([]string(nil), bi.errs...), Dirty: append([]string(nil), bi.dirty...),
		MirrorGen: bi.mirrorGen}
	if a, ok := m.accountFor(*b); ok {
		in.HasAccount = true
		in.TokenPresent = a.Token != ""
		if f := m.feeds[acctKey(a.Server, a.Username)]; f != nil {
			in.Feed, in.Rest = f.state, f.rest
			v.Feed, v.RetryIn = f.state, f.retryIn
			if f.err != nil {
				v.FeedErr = f.err.Error()
			}
		} else {
			in.Feed = books.FeedDisabled
		}
	}
	v.Status = books.BookStatus(in)
	return v, true
}

// bookFor returns the bound book for vaultID reached through account a;
// events for a vault arriving via an alternate account are ignored
// (§2.4.4).
func (m *Manager) bookForLocked(a account.Account, vaultID string) *books.Book {
	b := m.store.Registry.ByVaultID(vaultID)
	if b == nil || !account.SameServer(b.ServerURL, a.Server) || !strings.EqualFold(b.Account, a.Username) {
		return nil
	}
	return b
}

func (m *Manager) handle(a account.Account, u Update) {
	defer m.ping()
	m.mu.Lock()
	f := m.feeds[acctKey(a.Server, a.Username)]
	f.state, f.retryIn = u.State, u.RetryIn
	if u.Err != nil {
		f.err = u.Err
	}
	if u.State == books.FeedOpen {
		f.rest.NetworkError = false
	}
	var schedule []books.Book
	refresh := false
	switch p := u.Payload.(type) {
	case *Hello:
		f.err = nil
		f.hello = map[string]HelloVault{}
		for _, v := range p.Vaults {
			f.hello[v.VaultID] = v
			if m.store.Registry.ByVaultID(v.VaultID) == nil {
				refresh = true
			}
		}
		for _, b := range m.store.Registry.Books {
			if !b.IsBound() || b.VaultID == "" || !account.SameServer(b.ServerURL, a.Server) || !strings.EqualFold(b.Account, a.Username) {
				continue
			}
			bi := m.infoLocked(b)
			hv, ok := f.hello[b.VaultID]
			if !ok {
				bi.revoked, bi.reason = true, "removed"
				continue
			}
			bi.revoked, bi.reason = false, ""
			bi.canEdit, bi.role = hv.CanEdit, hv.Role
			bi.catchUp.Head = hv.Seq
			if hv.Seq > bi.catchUp.Cursor || bi.catchUp.Phase == books.CatchUpFailed {
				schedule = append(schedule, b)
			} else {
				bi.lastSynced = m.Now()
			}
		}
	case *VaultHead:
		if b := m.bookForLocked(a, p.VaultID); b != nil {
			bi := m.infoLocked(*b)
			if p.Seq > bi.catchUp.Head {
				bi.catchUp.Head = p.Seq
			}
			if bi.catchUp.Head > bi.catchUp.Cursor {
				schedule = append(schedule, *b)
			}
		}
	case *VaultAccess:
		if b := m.bookForLocked(a, p.VaultID); b != nil {
			bi := m.infoLocked(*b)
			bi.canEdit, bi.role, bi.revoked, bi.reason = p.CanEdit, p.Role, false, ""
		} else if m.store.Registry.ByVaultID(p.VaultID) == nil {
			refresh = true
		}
	case *VaultRevoked:
		if b := m.bookForLocked(a, p.VaultID); b != nil {
			bi := m.infoLocked(*b)
			bi.revoked, bi.reason = true, p.Reason
			m.notices = append(m.notices, fmt.Sprintf("Você não tem mais acesso a %q", b.Name))
		}
	case *VaultUpdated:
		if b := m.bookForLocked(a, p.VaultID); b != nil {
			go m.refreshVault(a, *b)
		}
	}
	m.mu.Unlock()
	for _, b := range schedule {
		m.scheduleCatchUp(a, b)
	}
	if refresh {
		go m.refreshBooks()
	}
}

func (m *Manager) refreshVault(a account.Account, b books.Book) {
	_, vapi, err := m.API(a)
	if err != nil {
		return
	}
	ctx, cancel := context.WithTimeout(m.ctx, 30*time.Second)
	defer cancel()
	v, err := vapi.GetVault(ctx, b.VaultID)
	if err != nil || v.Name == "" {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if cur := m.store.Registry.Get(b.ID); cur != nil && cur.Name != v.Name {
		cur.Name = v.Name
		_ = m.store.Save()
	}
	if v.Federation != nil && v.Federation.Status == "severed" {
		m.notices = append(m.notices, fmt.Sprintf("Este livro deixou de sincronizar com %s", books.Host(v.Federation.HomeURL)))
	}
	m.ping()
}

// refreshBooks lists every account's vaults and reconciles (§2.4).
func (m *Manager) refreshBooks() {
	ctx, cancel := context.WithTimeout(m.ctx, 60*time.Second)
	defer cancel()
	listings, _ := m.ListAll(ctx, m.accounts)
	m.mu.Lock()
	res := books.Reconcile(m.store.Registry, listings, m.Now(), func() string { return newUUID() })
	if res.Changed() && !m.store.Registry.ReadOnly {
		_ = m.store.Save()
	}
	for srv, n := range res.AddedByServer {
		m.notices = append(m.notices, fmt.Sprintf("%d livro(s) adicionado(s) de %s", n, books.Host(srv)))
	}
	for _, bn := range res.Banners {
		m.notices = append(m.notices, bn.Text())
	}
	m.mu.Unlock()
	m.ping()
}

func (m *Manager) scheduleCatchUp(a account.Account, b books.Book) {
	m.mu.Lock()
	bi := m.infoLocked(b)
	if bi.running {
		bi.again = true
		m.mu.Unlock()
		return
	}
	bi.running = true
	bi.catchUp.Phase = books.CatchUpRunning
	m.mu.Unlock()
	go m.runCatchUp(a, b)
}

func (m *Manager) runCatchUp(a account.Account, b books.Book) {
	for {
		api, _, err := m.API(a)
		var res CatchUpResult
		cache := LoadCache(m.cacheDir, b.ID, b.VaultID, b.ServerURL)
		if err == nil {
			root := ""
			if p := b.AbsPath(); p != "" {
				if fi, serr := os.Stat(p); serr == nil && fi.IsDir() {
					root = p
				}
			}
			ctx, cancel := context.WithTimeout(m.ctx, 5*time.Minute)
			res, err = CatchUp(ctx, api, b.VaultID, root, cache)
			cancel()
		}
		m.mu.Lock()
		bi := m.infoLocked(b)
		f := m.feeds[acctKey(a.Server, a.Username)]
		bi.catchUp.Cursor = cache.Cursor
		if res.Head > bi.catchUp.Head {
			bi.catchUp.Head = res.Head
		}
		if res.Stats.Changed() {
			bi.mirrorGen++
		}
		bi.dirty = res.Stats.Dirty
		switch {
		case err == nil:
			bi.catchUp.Phase, bi.catchUp.Failures, bi.catchUp.Reason = books.CatchUpIdle, 0, ""
			if f != nil {
				f.rest = books.RestHealth{}
			}
			if bi.catchUp.Cursor >= bi.catchUp.Head {
				bi.lastSynced = m.Now()
				cache.LastSyncedAt = bi.lastSynced
			}
		case errors.Is(err, account.ErrUnauthorized):
			bi.catchUp.Phase = books.CatchUpIdle
			if f != nil {
				f.rest.Unauthorized = true
			}
		case isNetworkErr(err):
			bi.catchUp.Phase = books.CatchUpIdle
			if f != nil {
				f.rest.NetworkError = true
			}
		default:
			bi.catchUp.Phase = books.CatchUpFailed
			bi.catchUp.Failures++
			bi.catchUp.Reason = err.Error()
		}
		if err != nil {
			bi.errs = append(bi.errs, m.Now().Format("15:04:05")+" "+err.Error())
			if len(bi.errs) > 5 {
				bi.errs = bi.errs[len(bi.errs)-5:]
			}
		}
		again := bi.again && err == nil
		bi.again = false
		if !again {
			bi.running = false
		} else {
			bi.catchUp.Phase = books.CatchUpRunning
		}
		m.mu.Unlock()
		_ = cache.Save(m.cacheDir, b.ID)
		m.ping()
		if !again {
			return
		}
	}
}

func isNetworkErr(err error) bool {
	return err != nil && (strings.HasPrefix(err.Error(), "network:") || errors.Is(err, context.DeadlineExceeded))
}

// Ensure *account.Client satisfies the interfaces.
var (
	_ ChangesAPI = (*account.Client)(nil)
	_ VaultAPI   = (*account.Client)(nil)
)

// SetActive records the active book in books.yaml (TUI-only `active`).
func (m *Manager) SetActive(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.store.Registry.SetActive(id, m.Now()); err != nil {
		return err
	}
	return m.store.Save()
}

// Move reorders a book in picker order (J/K) and saves.
func (m *Manager) Move(id string, delta int) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.store.Registry.Move(id, delta) {
		return nil
	}
	return m.store.Save()
}

// BookByPath returns the registered book whose folder is absPath.
func (m *Manager) BookByPath(absPath string) (books.Book, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if b := m.store.Registry.ByPath(absPath); b != nil {
		return *b, true
	}
	return books.Book{}, false
}
