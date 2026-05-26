package main

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/vinizap/lumi/tui-client/account"
)

const manageUsage = `lumi vault manage — interactive vault manager

Usage:
  lumi vault manage

Opens a Bubbletea TUI listing every vault from ~/.config/lumi/vaults.yaml.
Lets you open a vault (in-place exec), unlink (with confirmation), or
sync a server-bound vault — all without leaving the terminal.

Keys:
  j/k / ↑↓       Move cursor
  g / G          Jump to top / bottom
  Enter / o      Open the selected vault (re-execs 'lumi <path>')
  d              Unlink the selected vault (registry only; files untouched)
  s              Sync (server-bound only — runs 'lumi vault sync' inline)
  r              Refresh from disk
  q / Esc        Quit

Adding new vaults still goes through 'lumi vault link' or 'lumi vault clone'.
`

// vaultManageMode tracks what the manager is currently presenting.
type vaultManageMode int

const (
	manageModeList vaultManageMode = iota
	manageModeConfirmDelete
	manageModeSyncing
)

// vaultManageModel is the Bubbletea model for `lumi vault manage`.
type vaultManageModel struct {
	vaults []account.VaultEntry
	cursor int
	mode   vaultManageMode

	// Status banner shown at the bottom of the list. Cleared on the
	// next user keystroke. ok=true paints green; ok=false paints red.
	status   string
	statusOK bool

	// Sticky message rendered above the keybinds bar — survives one
	// extra refresh so users can still read the result of an action
	// that triggered a refresh.
	stickyStatus   string
	stickyStatusOK bool

	width  int
	height int

	// chosenPath is set when the user picks "open" (Enter/o) — the
	// runner translates this to an execve of `lumi <path>` after the
	// program quits. nil = no open requested.
	chosenPath string
}

// vaultListLoadedMsg arrives after a refresh; carries the fresh list.
type vaultListLoadedMsg struct {
	vaults []account.VaultEntry
	err    error
}

// vaultSyncResultMsg arrives after a sync run completes.
type vaultSyncResultMsg struct {
	vaultID string
	ok      bool
	summary string
}

func newVaultManageModel(vaults []account.VaultEntry) vaultManageModel {
	return vaultManageModel{vaults: vaults}
}

func (m vaultManageModel) Init() tea.Cmd { return nil }

func (m vaultManageModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil

	case vaultListLoadedMsg:
		if msg.err != nil {
			m.status = "Refresh failed: " + msg.err.Error()
			m.statusOK = false
			return m, nil
		}
		m.vaults = msg.vaults
		if m.cursor >= len(m.vaults) {
			m.cursor = max(0, len(m.vaults)-1)
		}
		m.mode = manageModeList
		return m, nil

	case vaultSyncResultMsg:
		// Move the result into sticky-status so it survives the
		// reload triggered below.
		m.stickyStatus = msg.summary
		m.stickyStatusOK = msg.ok
		m.status = ""
		m.mode = manageModeList
		return m, reloadVaultsCmd()

	case tea.KeyMsg:
		// Sync mode locks input — the goroutine is still running and
		// we don't want the user to fire a second sync mid-flight.
		if m.mode == manageModeSyncing {
			return m, nil
		}
		// Clear transient status on any keystroke that isn't sticky.
		m.status = ""
		return m.handleKey(msg)
	}
	return m, nil
}

// handleKey routes a key event by current mode. Pure-ish (no I/O) so
// the unit tests can drive transitions deterministically; the only
// side-effect surface is the tea.Cmd returned for async work.
func (m vaultManageModel) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.mode == manageModeConfirmDelete {
		switch msg.String() {
		case "y", "Y":
			return m.confirmDelete()
		case "n", "N", "esc":
			m.mode = manageModeList
			m.status = "Cancelled."
			m.statusOK = true
			return m, nil
		}
		return m, nil
	}

	switch msg.String() {
	case "ctrl+c", "q":
		return m, tea.Quit
	case "esc":
		// Esc in list mode also quits — gives a single-key exit on
		// keyboards where q lives somewhere awkward.
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
	case "enter", "o":
		if v, ok := m.currentVault(); ok {
			m.chosenPath = v.Path
			return m, tea.Quit
		}
	case "d":
		if _, ok := m.currentVault(); ok {
			m.mode = manageModeConfirmDelete
		}
	case "s":
		v, ok := m.currentVault()
		if !ok {
			return m, nil
		}
		if !v.IsServerBound() {
			m.status = "Sync requires a server-bound vault — this one is local."
			m.statusOK = false
			return m, nil
		}
		m.mode = manageModeSyncing
		m.status = fmt.Sprintf("Syncing %q…", v.Name)
		m.statusOK = true
		return m, syncVaultCmd(v)
	case "r":
		return m, reloadVaultsCmd()
	}
	return m, nil
}

func (m vaultManageModel) currentVault() (account.VaultEntry, bool) {
	if m.cursor < 0 || m.cursor >= len(m.vaults) {
		return account.VaultEntry{}, false
	}
	return m.vaults[m.cursor], true
}

// confirmDelete runs the unlink and triggers a refresh. The reload
// drives the cursor back into bounds and the result message renders
// at the top of the next View.
func (m vaultManageModel) confirmDelete() (tea.Model, tea.Cmd) {
	v, ok := m.currentVault()
	if !ok {
		m.mode = manageModeList
		return m, nil
	}
	removed, _, err := account.RemoveVaultByID(v.ID)
	if err != nil {
		m.mode = manageModeList
		m.status = "Unlink failed: " + err.Error()
		m.statusOK = false
		return m, nil
	}
	if !removed {
		m.mode = manageModeList
		m.status = fmt.Sprintf("Vault %q was not in the registry.", v.Name)
		m.statusOK = false
		return m, nil
	}
	m.stickyStatus = fmt.Sprintf("Unlinked %q (files on disk untouched).", v.Name)
	m.stickyStatusOK = true
	m.mode = manageModeList
	return m, reloadVaultsCmd()
}

func (m vaultManageModel) View() string {
	var b strings.Builder

	header := fmt.Sprintf("Vault Manager  (%d vaults — j/k move, enter open, d unlink, s sync, r refresh, q quit)\n\n", len(m.vaults))
	b.WriteString(header)

	if len(m.vaults) == 0 {
		b.WriteString("  No vaults registered. Add one with 'lumi vault link <path>' or 'lumi vault clone <url>/<slug>'.\n")
	}

	for i, v := range m.vaults {
		marker := "  "
		if i == m.cursor {
			marker = "▸ "
		}
		bind := "local"
		if v.IsServerBound() {
			bind = redactURLPath(v.Server)
			if v.Account != "" {
				bind = v.Account + "@" + bind
			}
		}
		fmt.Fprintf(&b, "%s%-30s  %s\n", marker, truncateMiddle(v.Name, 30), bind)
		fmt.Fprintf(&b, "    %s\n", v.Path)
		when := v.LastOpenedAt
		if when.IsZero() {
			when = v.AddedAt
		}
		if !when.IsZero() {
			fmt.Fprintf(&b, "    %s\n", when.Format(time.RFC3339))
		}
	}

	b.WriteString("\n")
	switch m.mode {
	case manageModeConfirmDelete:
		v, ok := m.currentVault()
		if ok {
			fmt.Fprintf(&b, "Unlink %q? (y/n)\n", v.Name)
		}
	case manageModeSyncing:
		v, ok := m.currentVault()
		if ok {
			fmt.Fprintf(&b, "Syncing %q…\n", v.Name)
		}
	default:
		if m.stickyStatus != "" {
			b.WriteString("  " + m.stickyStatus + "\n")
		}
		if m.status != "" {
			b.WriteString("  " + m.status + "\n")
		}
	}

	return b.String()
}

// reloadVaultsCmd is a tea.Cmd that re-reads vaults.yaml and dispatches
// a `vaultListLoadedMsg`. Used after every mutation so the on-screen
// list reflects disk reality.
func reloadVaultsCmd() tea.Cmd {
	return func() tea.Msg {
		entries, err := account.LoadVaults()
		return vaultListLoadedMsg{vaults: entries, err: err}
	}
}

// syncVaultCmd is a tea.Cmd that runs the existing `lumi vault sync`
// flow against the given vault entry. Captures stdout/stderr to a
// summary string surfaced via `vaultSyncResultMsg`.
func syncVaultCmd(v account.VaultEntry) tea.Cmd {
	return func() tea.Msg {
		// Slug is the last path segment of vaults.yaml's `Server` URL +
		// the registered Name. The actual mapping is server-side; we
		// don't have it here. Take the path's basename as a hint —
		// users running `vault clone <url>/<slug>` end up with
		// `path = <cwd>/<slug>` by default, so basename == slug for
		// fresh clones. When that's wrong, the sync command will
		// surface a clear error ("vault slug %q not found").
		slug := pathBasename(v.Path)
		arg := strings.TrimRight(v.Server, "/") + "/" + slug
		var stdout, stderr bytes.Buffer
		code := runVaultSyncCmd([]string{arg, v.Path}, &stdout, &stderr)
		summary := summariseSyncOutput(code, stdout.String(), stderr.String())
		return vaultSyncResultMsg{
			vaultID: v.ID,
			ok:      code == 0,
			summary: summary,
		}
	}
}

// summariseSyncOutput compresses the sync command's chatter into a
// single status-line. The full output is line-noise inside the
// manager's confined viewport; users who need detail can run `lumi
// vault sync` directly.
func summariseSyncOutput(code int, stdout, stderr string) string {
	// The sync command ends with a "Pushed N; pulled M; skipped K…"
	// summary line — grab the last non-empty line of stdout, fall
	// back to stderr's last line on failure.
	pick := func(s string) string {
		lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
		for i := len(lines) - 1; i >= 0; i-- {
			line := strings.TrimSpace(lines[i])
			if line != "" {
				return line
			}
		}
		return ""
	}
	if code == 0 {
		out := pick(stdout)
		if out == "" {
			return "Sync OK."
		}
		return "Sync OK — " + out
	}
	err := pick(stderr)
	if err == "" {
		err = pick(stdout)
	}
	if err == "" {
		return fmt.Sprintf("Sync failed (exit %d).", code)
	}
	return fmt.Sprintf("Sync failed (exit %d): %s", code, err)
}

// runVaultManageCmd implements `lumi vault manage`. Boots the
// Bubbletea program; on quit, optionally execs `lumi <path>` for the
// vault the user picked.
func runVaultManageCmd(args []string, stdout, stderr io.Writer) int {
	for _, a := range args {
		switch a {
		case "--help", "-h":
			fmt.Fprint(stdout, manageUsage)
			return 0
		default:
			fmt.Fprintf(stderr, "Error: unknown flag %q\n\n", a)
			fmt.Fprint(stderr, manageUsage)
			return 64
		}
	}

	entries, err := account.LoadVaults()
	if err != nil {
		fmt.Fprintf(stderr, "Error reading vaults.yaml: %v\n", err)
		return 1
	}

	prog := tea.NewProgram(newVaultManageModel(entries))
	finalModel, err := prog.Run()
	if err != nil {
		fmt.Fprintf(stderr, "Error: vault manager: %v\n", err)
		return 1
	}
	final, ok := finalModel.(vaultManageModel)
	if !ok {
		return 0
	}
	if final.chosenPath == "" {
		return 0
	}

	// Re-exec `lumi <path>` in-place so the user lands inside the main
	// note browser as if they'd run `lumi <path>` directly. Falls back
	// to a child process when syscall.Exec isn't available (rare —
	// every Unix supports it). On success this call never returns.
	execPath, lookErr := exec.LookPath(os.Args[0])
	if lookErr != nil || execPath == "" {
		execPath = os.Args[0]
	}
	if err := syscall.Exec(execPath, []string{execPath, final.chosenPath}, os.Environ()); err != nil {
		// Last-resort fallback — shouldn't happen on supported
		// platforms but degrades gracefully if it does.
		cmd := exec.Command(execPath, final.chosenPath)
		cmd.Stdin = os.Stdin
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if runErr := cmd.Run(); runErr != nil {
			fmt.Fprintf(stderr, "Error: failed to open vault %q: %v\n", final.chosenPath, runErr)
			return 1
		}
	}
	return 0
}

// pathBasename returns the trailing segment of a filesystem path,
// stripped of any trailing slash. Returns "" when path is empty or
// pure separators. Exposed for testability.
func pathBasename(p string) string {
	p = strings.TrimRight(p, "/")
	if p == "" {
		return ""
	}
	if idx := strings.LastIndex(p, "/"); idx >= 0 {
		return p[idx+1:]
	}
	return p
}
