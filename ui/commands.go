package ui

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/vinizap/lumi/tui-client/domain"
	"github.com/vinizap/lumi/tui-client/filesystem"
	"github.com/vinizap/lumi/tui-client/theme"
)

// glamourStyle returns the glamour style path based on the current theme.
func glamourStyle() string {
	if theme.Current.IsDark {
		return "dark"
	}
	return "light"
}

// --- Toast types ---

// ToastLevel controls the visual style of a toast notification.
type ToastLevel int

const (
	ToastSuccess ToastLevel = iota
	ToastError
	ToastInfo
)

// --- Messages ---

type itemsLoadedMsg struct {
	items []Item
	dir   string
}

type navItemsLoadedMsg struct {
	items []Item
}

type searchResultsMsg struct {
	results []Item
	gen     int // generation stamp; Update drops results whose gen != m.searchGen
}

// editorDoneMsg is sent after the editor exits so the note is re-read
// from disk. reopen: the edit started in the reader (show it again).
type editorDoneMsg struct {
	notePath string
	reopen   bool
	err      error
}

// toastDismissMsg auto-dismisses a toast after a timeout.
type toastDismissMsg struct {
	id int
}

// --- Commands ---

// showToast sets the toast state and returns a tick command for auto-dismiss.
func (m *Model) showToast(msg string, level ToastLevel, d time.Duration) tea.Cmd {
	m.toastID++
	m.toastMsg = msg
	m.toastLevel = level
	m.mo.to(animToast, 0, 1)
	id := m.toastID
	return tea.Tick(d, func(time.Time) tea.Msg {
		return toastDismissMsg{id: id}
	})
}

func (m Model) loadItems() tea.Msg {
	return itemsLoadedMsg{items: listDir(m.currentDir, m.showHidden), dir: m.currentDir}
}

func (m Model) loadNavItems() tea.Msg {
	var items []Item

	folders, _ := filesystem.ListFolders(m.navDir)
	for _, f := range folders {
		items = append(items, Item{
			Name:     f.Name,
			IsFolder: true,
			Path:     f.Path,
		})
	}

	notes, _ := filesystem.ListNotes(m.navDir)
	for _, n := range notes {
		items = append(items, Item{
			Name:     n.Title,
			IsFolder: false,
			Path:     n.Path,
			Note:     n,
		})
	}

	return navItemsLoadedMsg{items}
}

// dispatchSearch bumps the search generation counter and returns a Cmd
// that walks the vault asynchronously. The captured generation is
// stamped on the result message; Update drops messages whose gen no
// longer matches m.searchGen — that's how a slow walk for an earlier
// query gets discarded once the user types another character.
func (m *Model) dispatchSearch() tea.Cmd {
	m.searchGen++
	snap := *m
	return func() tea.Msg { return snap.performSearch() }
}

func (m Model) performSearch() tea.Msg {
	var results []Item

	filepath.Walk(m.rootDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !strings.HasSuffix(path, ".md") {
			return nil
		}

		note, err := filesystem.ReadNote(path)
		if err != nil {
			return nil
		}

		if m.searchQuery == "" {
			relPath, _ := filepath.Rel(m.rootDir, path)
			results = append(results, Item{
				Name:     relPath,
				IsFolder: false,
				Path:     path,
				Note:     note,
			})
			return nil
		}

		query := strings.ToLower(m.searchQuery)
		match := false
		if m.searchType == "filename" {
			match = strings.Contains(strings.ToLower(info.Name()), query)
		} else {
			match = strings.Contains(strings.ToLower(note.Content), query)
		}

		if match {
			relPath, _ := filepath.Rel(m.rootDir, path)
			results = append(results, Item{
				Name:     relPath,
				IsFolder: false,
				Path:     path,
				Note:     note,
			})
		}
		return nil
	})

	// Sort by relative path so the same query always returns the same
	// order. filepath.Walk's "lexical order per directory" still leaves
	// cross-tree order surprising to users (a/b.md beats a-z/c.md), and
	// reordering on every keystroke makes the result list feel unstable.
	sort.Slice(results, func(i, j int) bool { return results[i].Name < results[j].Name })

	return searchResultsMsg{results: results, gen: m.searchGen}
}

// findNoteByLink searches for a note matching the given target (ID, path, or relative path).
//
// Security: the `target` is attacker-influenced — it comes from a
// wikilink `[[...]]` or a markdown link inside a note. A malicious
// note containing `[[../../../etc/passwd]]` (or planting a symlink
// `notes/poison.md → /etc/secret.md`) must not cause the TUI to
// render content outside the vault. We gate the relative-path branch
// on filepath.IsLocal (lexical: rejects "..", absolute paths,
// Windows-reserved names) AND on pathIsInsideVault (resolves
// symlinks before comparing). The items match and the rootDir-bounded
// walk are inherently safe because they only inspect paths the TUI
// itself listed.
func (m *Model) findNoteByLink(target string) *domain.Note {
	// Try resolving as a relative path from the current note's directory.
	// `filepath.IsLocal` rejects absolute paths and any string that
	// lexically escapes its starting directory with `..`. We additionally
	// require the joined+resolved candidate to live inside the vault root
	// — covering the case where a symlink inside the vault tries to
	// pivot out.
	if m.fullNote != nil && filepath.IsLocal(target) {
		noteDir := filepath.Dir(m.fullNote.Path)
		candidate := filepath.Join(noteDir, target)
		// Add .md extension if not present
		if !strings.HasSuffix(candidate, ".md") {
			candidate += ".md"
		}
		if pathIsInsideVault(m.rootDir, candidate) {
			if note, err := filesystem.ReadNote(candidate); err == nil {
				return note
			}
		}
	}

	// Try matching by ID or path in current items
	for _, item := range m.items {
		if item.Note != nil && (item.Note.ID == target || strings.Contains(item.Note.Path, target)) {
			return item.Note
		}
	}

	// Walk the entire notes directory
	var found *domain.Note
	filepath.Walk(m.rootDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !strings.HasSuffix(path, ".md") {
			return nil
		}
		note, err := filesystem.ReadNote(path)
		if err != nil {
			return nil
		}
		if note.ID == target || strings.TrimSuffix(info.Name(), ".md") == target {
			found = note
			return filepath.SkipAll
		}
		return nil
	})
	return found
}

// pathIsInsideVault reports whether candidate (which may not yet
// exist) resolves to a location inside rootDir. Combines a lexical
// check with a symlink-resolution check so a symlink planted inside
// the vault cannot redirect findNoteByLink at an external file.
//
// The function returns true for non-existent candidates whose lexical
// path stays under rootDir — callers handle the read error normally.
// Both rootDir and the candidate are resolved via EvalSymlinks so a
// vault root mounted via a symlink (e.g. ~/notes → /mnt/data/notes)
// produces a consistent comparison.
func pathIsInsideVault(rootDir, candidate string) bool {
	absRoot, err := filepath.Abs(rootDir)
	if err != nil {
		return false
	}
	absCand, err := filepath.Abs(candidate)
	if err != nil {
		return false
	}

	resolvedRoot, err := filepath.EvalSymlinks(absRoot)
	if err != nil {
		return false // root must exist for any wikilink to resolve
	}

	// If the candidate exists, resolve its symlinks so we compare
	// the final destination rather than the link path itself. If it
	// doesn't exist (the wikilink target may not be created yet), we
	// resolve the parent directory instead and stitch the basename
	// back on — without that step, a vault root that itself crosses
	// a symlink (e.g. macOS `/var/folders/...` → `/private/var/...`)
	// produces a false escape verdict for paths that don't exist yet.
	if resolved, err := filepath.EvalSymlinks(absCand); err == nil {
		absCand = resolved
	} else if resolvedParent, err := filepath.EvalSymlinks(filepath.Dir(absCand)); err == nil {
		absCand = filepath.Join(resolvedParent, filepath.Base(absCand))
	}

	rel, err := filepath.Rel(resolvedRoot, absCand)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// openBrowser opens a URL in the default system browser.
func openBrowser(url string) {
	switch runtime.GOOS {
	case "darwin":
		exec.Command("open", url).Start()
	case "linux":
		exec.Command("xdg-open", url).Start()
	case "windows":
		exec.Command("rundll32", "url.dll,FileProtocolHandler", url).Start()
	}
}

// openNote sets up the model to view a note with rendered markdown.
func (m *Model) openNote(note *domain.Note) {
	if m.ws == nil || m.ws.root == nil {
		if m.restoreLayout(note.Path) {
			m.fullNote = m.ws.focus.buf.note
			return
		}
	}
	m.syncFocusOut()
	m.openNoteIn(note, "replace")
}
