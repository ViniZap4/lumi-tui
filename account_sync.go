package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/vinizap/lumi/tui-client/account"
	"github.com/vinizap/lumi/tui-client/books"
	"github.com/vinizap/lumi/tui-client/feed"
	"github.com/vinizap/lumi/tui-client/ui"
)

// accountSync keeps the interactive TUI in step with Lumi.app on the same
// machine (SPEC-LIVROS §8): it notices accounts.yaml / tokens.yaml
// changes within a second, answers the app's device-link requests with
// this TUI's sessions, asks the app for a link for accounts the TUI has
// no session for, and revokes tokens dropped because the app removed the
// account or signed it out on this Mac.
type accountSync struct {
	ch    *account.LinkChannel
	mgr   *feed.Manager
	mu    sync.Mutex
	send  func(tea.Msg)
	known map[string]string // account key -> token
	asked map[string]bool
	stamp string
	needs []account.Account
	ready bool
}

func startAccountSync(ctx context.Context, mgr *feed.Manager, initial []account.Account) *accountSync {
	ch, err := account.NewLinkChannel()
	if err != nil {
		return nil
	}
	s := &accountSync{ch: ch, mgr: mgr, known: map[string]string{}, asked: map[string]bool{}}
	for _, a := range initial {
		s.known[syncKey(a)] = a.Token
	}
	if err := ch.WritePresence(); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: link presence: %v\n", err)
	}
	go s.loop(ctx)
	return s
}

func (s *accountSync) stop() {
	if s != nil {
		s.ch.RemovePresence()
	}
}

// setSend points UI notifications at the current Bubbletea program.
func (s *accountSync) setSend(f func(tea.Msg)) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.send = f
	s.mu.Unlock()
}

func (s *accountSync) notify(msg tea.Msg) {
	s.mu.Lock()
	f := s.send
	s.mu.Unlock()
	if f != nil {
		f(msg)
	}
}

func syncKey(a account.Account) string { return a.Server + "#" + a.Username }

func fileStamp(paths ...string) string {
	out := ""
	for _, p := range paths {
		if st, err := os.Stat(p); err == nil {
			out += fmt.Sprintf("%s:%d:%d;", p, st.ModTime().UnixNano(), st.Size())
		}
	}
	return out
}

func (s *accountSync) loop(ctx context.Context) {
	t := time.NewTicker(time.Second)
	defer t.Stop()
	s.tick(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.tick(ctx)
		}
	}
}

func (s *accountSync) tick(ctx context.Context) {
	reg, err := account.Path()
	if err != nil {
		return
	}
	dir := filepath.Dir(reg)
	stamp := fileStamp(reg, filepath.Join(dir, "tokens.yaml"))
	pending := len(s.ch.Pending()) > 0
	if stamp != s.stamp || pending || !s.ready {
		f, err := account.Load()
		if err != nil {
			return
		}
		s.ready = true
		if len(f.Dropped) > 0 {
			go func(d []account.Account) {
				cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
				defer cancel()
				account.RevokeDropped(cctx, d)
			}(f.Dropped)
		}
		if pending {
			if n := account.ServeLinkRequests(ctx, s.ch, f, nil); n > 0 {
				s.notify(ui.AccountsChangedMsg{Toast: "Lumi.app entrou com a sessão do terminal"})
			}
		}
		if stamp != s.stamp {
			s.stamp = stamp
			s.reconcile(f)
		}
		s.needs = f.NeedsLink()
	}
	if len(s.needs) == 0 || !s.ch.PeerAlive(account.LinkClientApple) {
		return
	}
	for _, a := range s.needs {
		k := syncKey(a)
		if s.asked[k] {
			continue
		}
		s.asked[k] = true
		go func(a account.Account) {
			s.notify(ui.AccountsChangedMsg{Toast: "pedindo ao Lumi.app para entrar como " + a.Username + " em " + books.Host(a.Server) + " — aprove no app"})
			cctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
			defer cancel()
			if got, err := account.LinkAndSave(cctx, s.ch, a.Server, a.Username, 0, nil); err == nil {
				s.notify(ui.AccountsChangedMsg{Toast: "entrou como " + got.Username + " em " + books.Host(got.Server) + " pelo Lumi.app"})
			}
		}(a)
	}
}

// reconcile tells the feed manager about accounts added, removed or
// re-tokened by another process.
func (s *accountSync) reconcile(f *account.File) {
	seen := map[string]bool{}
	changed := false
	for _, a := range f.Accounts {
		k := syncKey(a)
		seen[k] = true
		if tok, ok := s.known[k]; ok && tok == a.Token {
			continue
		}
		s.known[k] = a.Token
		if s.mgr != nil {
			s.mgr.AddAccount(a)
		}
		changed = true
	}
	for k := range s.known {
		if seen[k] {
			continue
		}
		delete(s.known, k)
		if s.mgr != nil {
			for i := len(k) - 1; i >= 0; i-- {
				if k[i] == '#' {
					s.mgr.RemoveAccount(k[:i], k[i+1:])
					break
				}
			}
		}
		changed = true
	}
	if changed {
		s.notify(ui.AccountsChangedMsg{})
	}
}
