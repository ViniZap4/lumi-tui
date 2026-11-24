package account

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// Shared fixtures (SPEC-LIVROS §8.7), byte-identical copies of the
// lumi-apple Tests/Fixtures/accounts files.
func fixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "accounts", name))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestRegistryGoldenRoundTrip(t *testing.T) {
	for _, name := range []string{"accounts.v3.yaml", "accounts.v3.empty.yaml"} {
		want := fixture(t, name)
		accs, v, err := DecodeRegistry(want)
		if err != nil || v != 3 {
			t.Fatalf("%s: v=%d err=%v", name, v, err)
		}
		if got := EncodeRegistry(accs); !bytes.Equal(got, want) {
			t.Fatalf("%s round trip differs:\n--- got ---\n%s\n--- want ---\n%s", name, got, want)
		}
	}
	if got := EncodeRegistry(nil); !bytes.Equal(got, fixture(t, "accounts.v3.empty.yaml")) {
		t.Fatalf("empty registry: %q", got)
	}
}

func TestRegistryGoldenFields(t *testing.T) {
	accs, _, err := DecodeRegistry(fixture(t, "accounts.v3.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if len(accs) != 4 {
		t.Fatalf("rows: %d", len(accs))
	}
	friend := findIn(accs, "https://notes.friend.org/lumi", "alice")
	if friend == nil || friend.UserID != "legacy-7" || friend.DisplayName != "Alice\tS. — Ação" ||
		!friend.SignedOutAt.Equal(time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)) || !friend.ExpiresAt.IsZero() {
		t.Fatalf("friend row: %+v", friend)
	}
	work := findIn(accs, "https://lumi.work.com", "alice")
	if work == nil || work.DisplayName != `Alice "A." Souza` || work.WebOrigin != "https://notes.work.com" {
		t.Fatalf("work row: %+v", work)
	}
}

func TestRegistryNoncanonicalNormalises(t *testing.T) {
	accs, v, err := DecodeRegistry(fixture(t, "accounts.v3.noncanonical.yaml"))
	if err != nil || v != 3 {
		t.Fatalf("v=%d err=%v", v, err)
	}
	want := fixture(t, "accounts.v3.yaml")
	if got := EncodeRegistry(accs); !bytes.Equal(got, want) {
		t.Fatalf("noncanonical -> canonical differs:\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

func TestRegistryV2DecodesToMigratedFixture(t *testing.T) {
	accs, v, err := DecodeRegistry(fixture(t, "accounts.v2.yaml"))
	if err != nil || v != 2 {
		t.Fatalf("v=%d err=%v", v, err)
	}
	if a := findIn(accs, "https://lumi.work.com", "alice"); a == nil || a.Token != "tok-alice-work" {
		t.Fatalf("v2 token not surfaced for migration: %+v", a)
	}
	if got, want := EncodeRegistry(accs), fixture(t, "accounts.v2.migrated.yaml"); !bytes.Equal(got, want) {
		t.Fatalf("v2 -> v3 differs:\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

func TestRegistryTooNewIsReadOnly(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "accounts.yaml")
	body := strings.Replace(string(fixture(t, "accounts.v3.yaml")), "version: 3", "version: 4", 1)
	if err := os.WriteFile(path, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	f, err := LoadFrom(path)
	if err != nil {
		t.Fatal(err)
	}
	if !f.ReadOnly || len(f.Accounts) != 4 {
		t.Fatalf("readOnly=%v rows=%d", f.ReadOnly, len(f.Accounts))
	}
	f.Upsert(Account{Server: "https://x", Username: "y"})
	if err := f.Save(); err != ErrRegistryTooNew {
		t.Fatalf("save on a newer file: %v", err)
	}
	if data, _ := os.ReadFile(path); string(data) != body {
		t.Fatal("newer file was rewritten")
	}
}

// TUI v2 file with tokens → tokens.yaml + v3 registry equal to the
// shared migrated fixture.
func TestLoadMigratesV2FixtureMovingTokens(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "accounts.yaml")
	if err := os.WriteFile(path, fixture(t, "accounts.v2.yaml"), 0600); err != nil {
		t.Fatal(err)
	}
	f, err := LoadFrom(path)
	if err != nil {
		t.Fatal(err)
	}
	if a := f.Find("https://lumi.work.com", "alice"); a == nil || a.Token != "tok-alice-work" {
		t.Fatalf("alice token: %+v", a)
	}
	if a := f.Find("http://192.168.1.20:8080", "bob"); a == nil || a.Token != "tok-bob-lab" {
		t.Fatalf("bob token: %+v", a)
	}
	got, _ := os.ReadFile(path)
	if want := fixture(t, "accounts.v2.migrated.yaml"); !bytes.Equal(got, want) {
		t.Fatalf("registry after migration:\n%s", got)
	}
	tok, _ := os.ReadFile(filepath.Join(dir, "tokens.yaml"))
	if !bytes.Contains(tok, []byte("tok-alice-work")) || !bytes.Contains(tok, []byte("tok-bob-lab")) {
		t.Fatalf("tokens.yaml:\n%s", tok)
	}
}

// Lumi.app moved a v2 file aside as accounts.v2.yaml (it never reads
// tokens); the TUI imports the tokens and deletes the file.
func TestLoadImportsAppMovedV2File(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "accounts.yaml")
	if err := os.WriteFile(filepath.Join(dir, "accounts.v2.yaml"), fixture(t, "accounts.v2.yaml"), 0600); err != nil {
		t.Fatal(err)
	}
	// The app's v3 file already has alice (and an app-only account), not bob.
	app := []Account{
		{Server: "https://lumi.work.com", Username: "alice", UserID: "0192f7a1-3b4c-7d5e-8f60-718293a4b5c6",
			DisplayName: "Alice", AddedAt: time.Date(2026, 11, 20, 8, 30, 0, 0, time.UTC)},
		{Server: "https://apple.only", Username: "zed", AddedAt: time.Date(2026, 11, 24, 0, 0, 0, 0, time.UTC)},
	}
	if err := os.WriteFile(path, EncodeRegistry(app), 0600); err != nil {
		t.Fatal(err)
	}
	f, err := LoadFrom(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "accounts.v2.yaml")); !os.IsNotExist(err) {
		t.Fatalf("accounts.v2.yaml should be gone: %v", err)
	}
	if a := f.Find("https://lumi.work.com", "alice"); a == nil || a.Token != "tok-alice-work" {
		t.Fatalf("alice: %+v", a)
	}
	if a := f.Find("http://192.168.1.20:8080", "bob"); a == nil || a.Token != "tok-bob-lab" {
		t.Fatalf("bob row/token not restored: %+v", a)
	}
	if a := f.Find("https://apple.only", "zed"); a == nil || a.Token != "" {
		t.Fatalf("app-only account: %+v", a)
	}
}

// Another process (Lumi.app) adds a row and changes a field while this
// TUI holds an older snapshot; Save keeps both sides' changes.
func TestSaveMergesConcurrentChanges(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "accounts.yaml")
	seed := &File{}
	seed.Upsert(Account{Server: "https://a.example", Username: "alice", Token: "tok-a", AddedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)})
	seed.Upsert(Account{Server: "https://b.example", Username: "bob", Token: "tok-b", AddedAt: time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)})
	if err := seed.SaveTo(path); err != nil {
		t.Fatal(err)
	}
	mine, err := LoadFrom(path)
	if err != nil {
		t.Fatal(err)
	}

	// "The app": adds carol, sets alice's display name, signs bob out on this Mac.
	disk, _, _ := readRegistry(path)
	disk = append(disk, Account{Server: "https://c.example", Username: "carol", AddedAt: time.Date(2026, 1, 3, 0, 0, 0, 0, time.UTC)})
	findIn(disk, "https://a.example", "alice").DisplayName = "Alice (app)"
	findIn(disk, "https://b.example", "bob").SignedOutAt = time.Now().UTC().Add(time.Minute)
	if err := writeFileAtomic(path, EncodeRegistry(disk)); err != nil {
		t.Fatal(err)
	}

	// The TUI, on its stale snapshot: changes alice's web origin and removes nobody.
	mine.Find("https://a.example", "alice").WebOrigin = "https://web.a.example"
	if err := mine.Save(); err != nil {
		t.Fatal(err)
	}
	got, err := LoadFrom(path)
	if err != nil {
		t.Fatal(err)
	}
	a := got.Find("https://a.example", "alice")
	if a == nil || a.DisplayName != "Alice (app)" || a.WebOrigin != "https://web.a.example" || a.Token != "tok-a" {
		t.Fatalf("alice merge: %+v", a)
	}
	if got.Find("https://c.example", "carol") == nil {
		t.Fatal("row added by the other process was lost")
	}
	b := got.Find("https://b.example", "bob")
	if b == nil || b.Token != "" || b.SignedOutAt.IsZero() {
		t.Fatalf("bob should be signed out here: %+v", b)
	}
	if len(got.Dropped) != 1 || got.Dropped[0].Token != "tok-b" {
		t.Fatalf("bob's token should be reported for revocation: %+v", got.Dropped)
	}
}

// Sign-out propagation (SPEC-LIVROS §8.5): a row removed elsewhere, or
// signed out on this Mac after the TUI signed in, drops the TUI token
// and reports it for revocation.
func TestLoadDropsTokensSignedOutOrRemovedElsewhere(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "accounts.yaml")
	f := &File{}
	f.Upsert(Account{Server: "https://a.example", Username: "alice", Token: "tok-a"})
	f.Upsert(Account{Server: "https://b.example", Username: "bob", Token: "tok-b"})
	f.Upsert(Account{Server: "https://c.example", Username: "carol", Token: "tok-c"})
	if err := f.SaveTo(path); err != nil {
		t.Fatal(err)
	}
	disk, _, _ := readRegistry(path)
	// app removes alice; signs bob out on this Mac now; carol's sign-out is older than the TUI token.
	var keep []Account
	for _, a := range disk {
		switch a.Username {
		case "alice":
			continue
		case "bob":
			a.SignedOutAt = time.Now().UTC().Add(2 * time.Second)
		case "carol":
			a.SignedOutAt = time.Now().UTC().Add(-time.Hour)
		}
		keep = append(keep, a)
	}
	if err := writeFileAtomic(path, EncodeRegistry(keep)); err != nil {
		t.Fatal(err)
	}
	got, err := LoadFrom(path)
	if err != nil {
		t.Fatal(err)
	}
	dropped := map[string]string{}
	for _, d := range got.Dropped {
		dropped[d.Username] = d.Token
	}
	if dropped["alice"] != "tok-a" || dropped["bob"] != "tok-b" || len(dropped) != 2 {
		t.Fatalf("dropped: %v", dropped)
	}
	if c := got.Find("https://c.example", "carol"); c == nil || c.Token != "tok-c" {
		t.Fatalf("carol signed in after the sign-out and must keep her token: %+v", c)
	}
	tok, _ := os.ReadFile(filepath.Join(dir, "tokens.yaml"))
	if bytes.Contains(tok, []byte("tok-a")) || bytes.Contains(tok, []byte("tok-b")) {
		t.Fatalf("dropped tokens still on disk:\n%s", tok)
	}
}

func TestSignOutMacAndOptOut(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "accounts.yaml")
	f := &File{}
	f.Upsert(Account{Server: "https://a.example", Username: "alice", Token: "tok-a"})
	if err := f.SaveTo(path); err != nil {
		t.Fatal(err)
	}
	f, _ = LoadFrom(path)
	at := time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)
	f.SignOutMac("https://a.example", "alice", at)
	f.SetLinkOptOut("https://a.example", "alice", true)
	if err := f.Save(); err != nil {
		t.Fatal(err)
	}
	got, _ := LoadFrom(path)
	a := got.Find("https://a.example", "alice")
	if a == nil || a.Token != "" || !a.SignedOutAt.Equal(at) {
		t.Fatalf("after sign out on this Mac: %+v", a)
	}
	if !got.LinkOptedOut("https://a.example", "alice") || len(got.NeedsLink()) != 0 {
		t.Fatal("opt-out not persisted")
	}
	// Signing in again keeps signed_out_at (the new session is newer) and clears nothing else.
	got.Upsert(Account{Server: "https://a.example", Username: "alice", Token: "tok-new"})
	got.SetLinkOptOut("https://a.example", "alice", false)
	if err := got.Save(); err != nil {
		t.Fatal(err)
	}
	again, _ := LoadFrom(path)
	if a := again.Find("https://a.example", "alice"); a == nil || a.Token != "tok-new" || !a.SignedOutAt.Equal(at) {
		t.Fatalf("re-sign-in: %+v", a)
	}
}

// Many goroutines each add their own account through Load → Upsert →
// Save; none is lost (the flock serialises the read-modify-write).
func TestConcurrentWritersGoroutines(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "accounts.yaml")
	const n = 24
	var wg sync.WaitGroup
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			f, err := LoadFrom(path)
			if err != nil {
				errs <- err
				return
			}
			f.Upsert(Account{Server: fmt.Sprintf("https://s%02d.example", i), Username: "u", Token: fmt.Sprintf("t%d", i)})
			errs <- f.Save()
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	got, err := LoadFrom(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Accounts) != n {
		t.Fatalf("rows = %d, want %d", len(got.Accounts), n)
	}
	for _, a := range got.Accounts {
		if a.Token == "" {
			t.Fatalf("token lost for %s", a.Server)
		}
	}
	if info, _ := os.Stat(path); info.Mode().Perm() != 0600 {
		t.Fatalf("perm %o", info.Mode().Perm())
	}
}

// Same, across processes (re-executing the test binary), which is what
// Lumi.app and the TUI actually are.
func TestConcurrentWritersProcesses(t *testing.T) {
	if os.Getenv("LUMI_REGISTRY_HELPER") != "" {
		return
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "accounts.yaml")
	const procs, each = 4, 8
	var wg sync.WaitGroup
	outs := make([][]byte, procs)
	errs := make([]error, procs)
	for p := 0; p < procs; p++ {
		wg.Add(1)
		go func(p int) {
			defer wg.Done()
			cmd := exec.Command(os.Args[0], "-test.run=^TestRegistryHelperProcess$")
			cmd.Env = append(os.Environ(), "LUMI_REGISTRY_HELPER=1", "LUMI_REGISTRY_PATH="+path,
				"LUMI_REGISTRY_ID="+strconv.Itoa(p), "LUMI_REGISTRY_EACH="+strconv.Itoa(each))
			outs[p], errs[p] = cmd.CombinedOutput()
		}(p)
	}
	wg.Wait()
	for p := range errs {
		if errs[p] != nil {
			t.Fatalf("helper %d: %v\n%s", p, errs[p], outs[p])
		}
	}
	got, err := LoadFrom(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Accounts) != procs*each {
		t.Fatalf("rows = %d, want %d", len(got.Accounts), procs*each)
	}
}

func TestRegistryHelperProcess(t *testing.T) {
	if os.Getenv("LUMI_REGISTRY_HELPER") == "" {
		t.Skip("helper")
	}
	path := os.Getenv("LUMI_REGISTRY_PATH")
	id := os.Getenv("LUMI_REGISTRY_ID")
	each, _ := strconv.Atoi(os.Getenv("LUMI_REGISTRY_EACH"))
	for i := 0; i < each; i++ {
		f, err := LoadFrom(path)
		if err != nil {
			t.Fatal(err)
		}
		f.Upsert(Account{Server: fmt.Sprintf("https://p%s-%d.example", id, i), Username: "u", Token: "t"})
		if err := f.Save(); err != nil {
			t.Fatal(err)
		}
	}
}
