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

	feeds   map[string]*feedInfo
	bstate  map[string]*bookInfo
	cancels map[string]context.CancelFunc

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
	pending    []string // notes modified since their base, not yet pushed
	conflicts  []Conflict
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
	Dirty      []string // notes the mirror left alone (read-only books)
	// Pending lists notes modified since their base and not yet pushed
	// (SPEC-LIVROS §4.2 "pendente"; CRDT-CLIENT-PROTOCOL §7.1). Offline,
	// nothing is queued: this is what the status line counts.
	Pending   []string
	Conflicts []Conflict // last pushes that had to keep both versions
	MirrorGen uint64     // bumps whenever the mirror changed files
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
		cancels:  map[string]context.CancelFunc{},
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
		m.startFeed(ctx, a)
	}
	m.ping()
}

// startFeed opens one account's stream with its own cancel.
func (m *Manager) startFeed(ctx context.Context, a account.Account) {
	fctx, cancel := context.WithCancel(ctx)
	m.mu.Lock()
	m.cancels[acctKey(a.Server, a.Username)] = cancel
	m.mu.Unlock()
	ch := make(chan Update, 16)
	go Run(fctx, m.ConnConfig(a), ch)
	go func() {
		for {
			select {
			case <-fctx.Done():
				return
			case u := <-ch:
				m.handle(a, u)
			}
		}
	}()
}

// Accounts returns a copy of the accounts the manager runs feeds for.
func (m *Manager) Accounts() []account.Account {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]account.Account(nil), m.accounts...)
}

// AddAccount starts (or restarts, after a new sign-in) an account's feed
// and reconciles its books. Safe before Start (the feed opens on Start).
func (m *Manager) AddAccount(a account.Account) {
	m.RemoveAccount(a.Server, a.Username)
	m.mu.Lock()
	m.accounts = append(m.accounts, a)
	started := m.ctx != nil
	if started {
		m.feeds[acctKey(a.Server, a.Username)] = &feedInfo{state: books.FeedConnecting}
	}
	ctx := m.ctx
	m.mu.Unlock()
	if started {
		m.startFeed(ctx, a)
		go m.refreshBooks()
	}
	m.ping()
}

// RemoveAccount stops an account's feed (sign out / remove). Its books
// stay in the registry and show "Precisa entrar".
func (m *Manager) RemoveAccount(server, username string) {
	key := acctKey(server, username)
	m.mu.Lock()
	if c := m.cancels[key]; c != nil {
		c()
		delete(m.cancels, key)
	}
	delete(m.feeds, key)
	kept := m.accounts[:0]
	for _, a := range m.accounts {
		if acctKey(a.Server, a.Username) != key {
			kept = append(kept, a)
		}
	}
	m.accounts = kept
	m.mu.Unlock()
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
			bi.pending = (&Syncer{Root: bookRoot(b), Cache: c}).Pending()
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
	// The TUI has no live CRDT session (editor closed); "pending" is the
	// set of notes modified since their base whose push has not returned
	// 2xx (SPEC-LIVROS §4.2, CRDT-CLIENT-PROTOCOL §7.1).
	in := books.StatusInput{Book: *b, Revoked: bi.revoked, RevokeReason: bi.reason, CanEdit: bi.canEdit, CatchUp: bi.catchUp,
		Editor: books.Editor{Pending: len(bi.pending) > 0}}
	v := BookView{Book: *b, Role: bi.role, Cursor: bi.catchUp.Cursor, Head: bi.catchUp.Head,
		LastSynced: bi.lastSynced, Errors: append([]string(nil), bi.errs...), Dirty: append([]string(nil), bi.dirty...),
		Pending: append([]string(nil), bi.pending...), Conflicts: append([]Conflict(nil), bi.conflicts...),
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
	if f == nil {
		// The account was removed (sign-out / remove) while its feed
		// goroutine was still delivering; drop the late update.
		m.mu.Unlock()
		return
	}
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
			// Pending local edits (made offline) are flushed on reconnect
			// through the same loop.
			if hv.Seq > bi.catchUp.Cursor || bi.catchUp.Phase == books.CatchUpFailed || len(bi.pending) > 0 {
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
	listings, _ := m.ListAll(ctx, m.Accounts())
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

// bookRoot is the book's folder when it exists on this device.
func bookRoot(b books.Book) string {
	if p := b.AbsPath(); p != "" {
		if fi, err := os.Stat(p); err == nil && fi.IsDir() {
			return p
		}
	}
	return ""
}

// LocalEdit tells the manager a note in the book was changed on disk by
// this client (editor closed, checkbox toggled). The pending set is
// recomputed at once; when the account's feed is open the edit is pushed
// right away through the same catch-up + flush loop. Offline nothing is
// queued — the pending count stays visible until the next sync.
func (m *Manager) LocalEdit(bookID string) {
	m.mu.Lock()
	b := m.store.Registry.Get(bookID)
	if b == nil || !b.IsBound() || b.VaultID == "" {
		m.mu.Unlock()
		return
	}
	book := *b
	bi := m.infoLocked(book)
	running := bi.running
	a, ok := m.accountFor(book)
	var open bool
	if ok {
		if f := m.feeds[acctKey(a.Server, a.Username)]; f != nil {
			open = f.state == books.FeedOpen && !f.rest.NetworkError
		}
	}
	m.mu.Unlock()
	if ok && open && m.ctx != nil {
		m.scheduleCatchUp(a, book)
		return
	}
	if running {
		return // the running loop recomputes pending when it ends
	}
	c := LoadCache(m.cacheDir, book.ID, book.VaultID, book.ServerURL)
	pending := (&Syncer{Root: bookRoot(book), Cache: c}).Pending()
	m.mu.Lock()
	m.infoLocked(book).pending = pending
	m.mu.Unlock()
	m.ping()
}

// flushResult reports a pending-edit flush.
type flushResult struct {
	pushed    int
	conflicts []Conflict
	errs      []string
	err       error // first network error (flush stopped)
}

// flushPending pushes every note modified since its base (Syncer.Push:
// 3-way merge, or keep-both on conflict). It stops at the first network
// error; other per-note failures are recorded and the note stays pending.
func flushPending(ctx context.Context, s *Syncer) flushResult {
	var fr flushResult
	for _, p := range s.PendingPaths() {
		res, err := s.Push(ctx, p)
		if err != nil {
			if isNetworkErr(err) || ctx.Err() != nil || errors.Is(err, account.ErrUnauthorized) {
				fr.err = err
				return fr
			}
			fr.errs = append(fr.errs, s.rel(p)+": "+err.Error())
			continue
		}
		switch res.Outcome {
		case PushMerged:
			fr.pushed++
		case PushConflict:
			fr.pushed++
			fr.conflicts = append(fr.conflicts, Conflict{Path: res.Path, Copy: res.ConflictPath, Lineage: res.Lineage})
		}
	}
	return fr
}

func (m *Manager) runCatchUp(a account.Account, b books.Book) {
	for {
		api, _, err := m.API(a)
		var res CatchUpResult
		var fr flushResult
		cache := LoadCache(m.cacheDir, b.ID, b.VaultID, b.ServerURL)
		root := bookRoot(b)
		m.mu.Lock()
		canEdit := m.infoLocked(b).canEdit
		m.mu.Unlock()
		s := &Syncer{VaultID: b.VaultID, Root: root, Cache: cache, Bases: BasesFor(m.cacheDir, b.ID), NoPush: !canEdit, Now: m.Now}
		if err == nil {
			if napi, ok := api.(NoteAPI); ok {
				s.API = napi
			} else {
				s.API, s.NoPush = pullOnly{api}, true
			}
			ctx, cancel := context.WithTimeout(m.ctx, 5*time.Minute)
			res, err = s.CatchUp(ctx, api)
			if err == nil && root != "" && !s.NoPush {
				fr = flushPending(ctx, s)
				err = fr.err
			}
			cancel()
		}
		var pending []string
		if root != "" {
			pending = s.Pending()
		}
		m.mu.Lock()
		bi := m.infoLocked(b)
		f := m.feeds[acctKey(a.Server, a.Username)]
		bi.catchUp.Cursor = cache.Cursor
		if res.Head > bi.catchUp.Head {
			bi.catchUp.Head = res.Head
		}
		if res.Stats.Changed() || fr.pushed > 0 {
			bi.mirrorGen++
		}
		bi.dirty = res.Stats.Dirty
		bi.pending = pending
		for _, c := range append(res.Stats.Conflicts, fr.conflicts...) {
			bi.conflicts = append(bi.conflicts, c)
			if c.Lineage {
				m.notices = append(m.notices, fmt.Sprintf("%s foi recriada no servidor: sua edição offline foi salva em %s (conflito offline)", c.Path, c.Copy))
			} else {
				m.notices = append(m.notices, fmt.Sprintf("Conflito em %s: a versão do servidor ficou na nota; a sua foi salva em %s", c.Path, c.Copy))
			}
		}
		if len(bi.conflicts) > 5 {
			bi.conflicts = bi.conflicts[len(bi.conflicts)-5:]
		}
		for _, e := range append(res.Stats.Errors, fr.errs...) {
			bi.errs = append(bi.errs, m.Now().Format("15:04:05")+" envio "+e)
		}
		switch {
		case err == nil:
			bi.catchUp.Phase, bi.catchUp.Failures, bi.catchUp.Reason = books.CatchUpIdle, 0, ""
			if f != nil {
				f.rest = books.RestHealth{}
			}
			if bi.catchUp.Cursor >= bi.catchUp.Head && len(pending) == 0 {
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
		}
		if len(bi.errs) > 5 {
			bi.errs = bi.errs[len(bi.errs)-5:]
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

// UpdateRegistry applies fn to the in-memory registry and, when fn reports
// a change, saves books.yaml. Books whose folder changed lose their cached
// per-book state (pending set, cursor) so it is recomputed from the new
// folder. Used by clone-in-place and "link existing folder" so the file
// and the manager never disagree.
func (m *Manager) UpdateRegistry(fn func(reg *books.Registry) bool) error {
	m.mu.Lock()
	before := map[string]string{}
	for _, b := range m.store.Registry.Books {
		before[b.ID] = b.Path
	}
	changed := fn(m.store.Registry)
	var err error
	if changed {
		if m.store.Registry.ReadOnly {
			err = books.ErrReadOnly
		} else {
			err = m.store.Save()
		}
		for _, b := range m.store.Registry.Books {
			if p, ok := before[b.ID]; !ok || p != b.Path {
				delete(m.bstate, b.ID)
			}
		}
	}
	m.mu.Unlock()
	if changed {
		m.ping()
	}
	return err
}
