package main

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/vinizap/lumi/tui-client/books"
)

// vaultPickerModel is a tiny bubbletea program that shows the user the
// books registered in `~/.config/lumi/books.yaml` (SPEC-LIVROS §5; it
// imports vaults.yaml once) that have a folder here, and lets them pick
// one before the main TUI launches. It runs only when the user invoked
// `lumi` with no path argument and no $LUMI_NOTES_DIR — i.e. the
// shell-less "where did I leave off" case.
//
// The picker is intentionally not a view of the main Model: this is
// pre-launch UX, the vault list is read once at startup, and bundling
// it into `ui.Model` would mean threading picker state through every
// downstream key/Update path. A separate ~150-line tea.Program is the
// smaller change.
type vaultPickerModel struct {
	vaults []books.Book
	cursor int

	// terminated by the user. `chosen` is non-nil when the user pressed
	// Enter on a row; `cancelled` is true when they pressed q / ESC.
	// Both nil/false until then.
	chosen    *books.Book
	cancelled bool

	width  int
	height int
}

// newVaultPickerModel takes a non-empty vault list. Caller is responsible
// for falling back to the splash when no vaults are registered.
func newVaultPickerModel(vaults []books.Book) vaultPickerModel {
	return vaultPickerModel{vaults: vaults}
}

func (m vaultPickerModel) Init() tea.Cmd { return nil }

func (m vaultPickerModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "q", "esc":
			m.cancelled = true
			return m, tea.Quit
		case "up", "k":
			if m.cursor > 0 {
				m.cursor--
			}
		case "down", "j":
			if m.cursor < len(m.vaults)-1 {
				m.cursor++
			}
		case "home", "g":
			m.cursor = 0
		case "end", "G":
			m.cursor = len(m.vaults) - 1
		case "1", "2", "3", "4", "5", "6", "7", "8", "9":
			if i := int(msg.String()[0] - '1'); i < len(m.vaults) {
				v := m.vaults[i]
				m.chosen = &v
				return m, tea.Quit
			}
		case "enter":
			if m.cursor >= 0 && m.cursor < len(m.vaults) {
				v := m.vaults[m.cursor]
				m.chosen = &v
			}
			return m, tea.Quit
		}
	}
	return m, nil
}

func (m vaultPickerModel) View() string {
	if len(m.vaults) == 0 {
		return "No vaults to pick.\n"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Pick a book  (%d available — j/k to move, enter to open, q to skip)\n\n", len(m.vaults))
	for i, v := range m.vaults {
		marker := "  "
		if i == m.cursor {
			marker = "▸ "
		}
		bind := "local"
		if v.IsBound() {
			bind = redactURLPath(v.ServerURL)
			if v.Account != "" {
				bind = v.Account + "@" + bind
			}
		}
		num := "  "
		if i < 9 {
			num = fmt.Sprintf("%d ", i+1)
		}
		fmt.Fprintf(&b, "%s%s%-30s  %s\n", marker, num, truncateMiddle(v.Name, 30), bind)
		fmt.Fprintf(&b, "      %s\n", v.Path)
	}
	return b.String()
}

// runVaultPicker boots a small tea.Program that lets the user pick a
// vault from the supplied list. Returns the chosen entry, or nil when
// the user cancelled (in which case the caller should fall back to the
// splash). An error means the program failed to run — treat as a
// cancellation (so we don't strand the user with no UI).
func runVaultPicker(vaults []books.Book) (*books.Book, error) {
	if len(vaults) == 0 {
		return nil, nil
	}
	m := newVaultPickerModel(vaults)
	p := tea.NewProgram(m)
	finalModel, err := p.Run()
	if err != nil {
		return nil, err
	}
	final, ok := finalModel.(vaultPickerModel)
	if !ok {
		return nil, nil
	}
	return final.chosen, nil
}

// shouldRunVaultPicker decides whether `lumi` (no args, no env) should
// show the vault picker before falling back to the splash. Pure for
// testability — the actual I/O (LoadVaults) happens at the call site.
//
// Rule: only when the user gave lumi no hint at all (splash=true AND
// initialNote empty) AND the registry has at least one entry. Anything
// less specific keeps the previous splash-on-cwd behaviour so users
// without server-bound vaults aren't ambushed by a picker they didn't
// ask for. A single registered vault still gets the picker (rather
// than auto-opening) so the user has a moment to cancel — saves them
// from accidentally trusting a stale registry row.
func shouldRunVaultPicker(splash bool, initialNote string, numVaults int) bool {
	if !splash || initialNote != "" {
		return false
	}
	return numVaults > 0
}
