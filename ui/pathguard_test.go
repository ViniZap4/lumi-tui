package ui

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// pathIsInsideVault: same-dir note is in the vault.
func TestPathIsInsideVault_SameDir(t *testing.T) {
	root := t.TempDir()
	cand := filepath.Join(root, "note.md")
	if err := os.WriteFile(cand, []byte("hi"), 0644); err != nil {
		t.Fatal(err)
	}
	if !pathIsInsideVault(root, cand) {
		t.Fatalf("expected %q inside %q", cand, root)
	}
}

// pathIsInsideVault: deeper subdir is in the vault.
func TestPathIsInsideVault_Subdir(t *testing.T) {
	root := t.TempDir()
	sub := filepath.Join(root, "a", "b")
	if err := os.MkdirAll(sub, 0755); err != nil {
		t.Fatal(err)
	}
	cand := filepath.Join(sub, "note.md")
	if err := os.WriteFile(cand, []byte("hi"), 0644); err != nil {
		t.Fatal(err)
	}
	if !pathIsInsideVault(root, cand) {
		t.Fatalf("expected %q inside %q", cand, root)
	}
}

// Lexical escape via "..": `../outside.md` from inside the vault must
// be rejected. This is the canonical wikilink path-traversal attack.
func TestPathIsInsideVault_LexicalEscape(t *testing.T) {
	root := t.TempDir()
	// Construct an outside path lexically — it doesn't need to exist
	// for the escape attempt to count as a vulnerability.
	cand := filepath.Join(root, "..", "outside.md")
	if pathIsInsideVault(root, cand) {
		t.Fatalf("expected %q rejected; it escapes %q", cand, root)
	}
}

// Absolute paths to /etc/* are not inside the vault. The wikilink
// `[[/etc/passwd]]` would land here.
func TestPathIsInsideVault_AbsoluteSystemPath(t *testing.T) {
	root := t.TempDir()
	cand := "/etc/passwd.md"
	if pathIsInsideVault(root, cand) {
		t.Fatalf("expected /etc/passwd.md rejected against vault %q", root)
	}
}

// Non-existent candidate inside the vault: still inside (lexical
// check holds; ReadNote will simply fail later — that's the right
// failure mode for a wikilink pointing at a note the user hasn't
// created yet).
func TestPathIsInsideVault_NonexistentLocal(t *testing.T) {
	root := t.TempDir()
	cand := filepath.Join(root, "not-yet-created.md")
	if !pathIsInsideVault(root, cand) {
		t.Fatalf("expected %q to be accepted (lexically inside)", cand)
	}
}

// Symlink planted inside the vault that points OUT must be rejected.
// This is the second leg of the attack: even with IsLocal-clean
// target, a malicious file inside the vault can pivot out.
func TestPathIsInsideVault_SymlinkEscape(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks require admin on Windows")
	}
	root := t.TempDir()
	outsideDir := t.TempDir()
	outside := filepath.Join(outsideDir, "secret.md")
	if err := os.WriteFile(outside, []byte("topsecret"), 0644); err != nil {
		t.Fatal(err)
	}
	// poison.md inside the vault is a symlink to a file outside.
	link := filepath.Join(root, "poison.md")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	if pathIsInsideVault(root, link) {
		t.Fatalf("expected symlink %q → %q to be rejected", link, outside)
	}
}

// Vault root that is itself a symlink (e.g. ~/notes → /mnt/data/notes)
// must not produce a false-negative on legitimate files inside.
func TestPathIsInsideVault_SymlinkedRoot(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks require admin on Windows")
	}
	realRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(realRoot, "note.md"), []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	parent := t.TempDir()
	linkedRoot := filepath.Join(parent, "vault-link")
	if err := os.Symlink(realRoot, linkedRoot); err != nil {
		t.Fatal(err)
	}
	cand := filepath.Join(linkedRoot, "note.md")
	if !pathIsInsideVault(linkedRoot, cand) {
		t.Fatalf("expected note inside symlinked vault root to be accepted")
	}
}

// Empty rootDir is rejected — there's no vault to be inside of.
func TestPathIsInsideVault_EmptyRoot(t *testing.T) {
	if pathIsInsideVault("", "/anything.md") {
		t.Fatal("expected empty rootDir to reject every candidate")
	}
}

// Nonexistent rootDir is rejected (EvalSymlinks would error).
func TestPathIsInsideVault_NonexistentRoot(t *testing.T) {
	if pathIsInsideVault("/nonexistent/lumi-vault/does/not/exist", "/anything.md") {
		t.Fatal("expected nonexistent root to reject every candidate")
	}
}
