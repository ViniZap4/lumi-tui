package ui

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/glamour"
	"github.com/vinizap/lumi/tui-client/config"
	"github.com/vinizap/lumi/tui-client/theme"
	"github.com/vinizap/lumi/tui-client/vim"
)

// enterConfig sets up the config view and transitions to it.
func (m *Model) enterConfig() {
	cfg := config.Load()
	m.configCfg = cfg
	m.configCursor = 1 // skip first header
	m.overlayBeforeConfig = m.floatExp
	if m.floatExp {
		m.closeOverlay()
	}
	m.viewMode = ViewConfig
	m.configItems = m.buildConfigItems(cfg)
}

func (m Model) buildConfigItems(cfg *config.Config) []ConfigItem {
	darkNames := theme.ThemeNamesForMode("dark")
	lightNames := theme.ThemeNamesForMode("light")

	editorFull := cfg.Editor
	if len(cfg.EditorArgs) > 0 {
		editorFull += " " + strings.Join(cfg.EditorArgs, " ")
	}

	return []ConfigItem{
		{Label: "Theme", Kind: ConfigHeader},
		{Label: "Mode", Kind: ConfigCycle, Key: "theme_mode", Value: cfg.ThemeMode, Options: []string{"dark", "light", "auto"}},
		{Label: "Dark theme", Kind: ConfigCycle, Key: "dark_theme", Value: cfg.DarkTheme, Options: darkNames},
		{Label: "Light theme", Kind: ConfigCycle, Key: "light_theme", Value: cfg.LightTheme, Options: lightNames},

		{Label: "Editor", Kind: ConfigHeader},
		{Label: "Editor padrão (i e)", Kind: ConfigCycle, Key: "default_editor", Value: cfg.DefaultEditor, Options: []string{"internal", "external"}},
		{Label: "External command", Kind: ConfigInput, Key: "editor", Value: editorFull},
		{Label: "Open notes in", Kind: ConfigCycle, Key: "open_in", Value: cfg.OpenIn, Options: []string{"reader", "source"}},
		{Label: "Escape sequences", Kind: ConfigInput, Key: "escape", Value: escValue(cfg.EscapeSequences)},
		{Label: "Escape timeout ms", Kind: ConfigCycle, Key: "escape_timeout_ms", Value: fmt.Sprint(cfg.EscapeTimeoutMs), Options: []string{"150", "200", "300", "500", "800"}},
		{Label: "Autosave", Kind: ConfigCycle, Key: "autosave", Value: boolStr(cfg.Autosave), Options: []string{"off", "on"}},
		{Label: "Yank to clipboard", Kind: ConfigCycle, Key: "clipboard_sync", Value: boolStr(cfg.ClipboardSync), Options: []string{"off", "on"}},
		{Label: "vimrc " + vimrcSummary(), Kind: ConfigCycle, Key: "vimrc", Value: boolStr(cfg.Vimrc), Options: []string{"off", "on"}},
		{Label: "Reduce motion", Kind: ConfigCycle, Key: "reduce_motion", Value: boolStr(cfg.ReduceMotion), Options: []string{"off", "on"}},
		{Label: "Open config file", Kind: ConfigAction, Key: "open_editor"},

		{Label: "Servers", Kind: ConfigHeader},
		{Label: "Accounts and servers (ga)", Kind: ConfigAction, Key: "accounts"},
		{Label: "Pasta dos livros (clonar)", Kind: ConfigInput, Key: "books_dir", Value: cfg.BooksDir},

		{Label: "Explorer", Kind: ConfigHeader},
		{Label: "Preview column", Kind: ConfigCycle, Key: "show_preview", Value: boolStr(!m.previewHidden), Options: []string{"off", "on"}},
		{Label: "Command legend", Kind: ConfigCycle, Key: "show_legend", Value: boolStr(m.legendOn), Options: []string{"off", "on"}},
		{Label: "Modo zen (<leader>z)", Kind: ConfigCycle, Key: "zen", Value: boolStr(m.zen), Options: []string{"off", "on"}},
		{Label: "Pane navigation", Kind: ConfigCycle, Key: "pane_nav", Value: boolStr(m.paneNav), Options: []string{"off", "on"}},
		{Label: "Mouse", Kind: ConfigCycle, Key: "mouse", Value: boolStr(m.mouseOn), Options: []string{"off", "on"}},
		{Label: "Hidden files", Kind: ConfigCycle, Key: "show_hidden", Value: boolStr(m.showHidden), Options: []string{"off", "on"}},

		{Label: "Display", Kind: ConfigHeader},
		{Label: "Line numbers", Kind: ConfigCycle, Key: "show_line_numbers", Value: boolStr(cfg.ShowLineNumbers), Options: []string{"off", "on"}},
		{Label: "Cursor style", Kind: ConfigCycle, Key: "cursor_style", Value: cfg.CursorStyle, Options: []string{"block", "underline", "bar"}},
		{Label: "Preview lines", Kind: ConfigCycle, Key: "preview_lines", Value: fmt.Sprintf("%d", cfg.PreviewLines), Options: []string{"5", "10", "15", "20"}},

		{Label: "Search", Kind: ConfigHeader},
		{Label: "Default type", Kind: ConfigCycle, Key: "default_search_type", Value: cfg.SearchType, Options: []string{"filename", "content"}},

		// Server accounts live in ~/.config/lumi/accounts.yaml (lumi
		// login) and books in books.yaml (gb / lumi books). Tokens are
		// never stored in a notes folder (SPEC-LIVROS §2.5).
	}
}

func (m Model) updateConfig(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc", ",", "q":
		m.viewMode = ViewHome
		if m.ws != nil && m.ws.root != nil {
			m.viewMode = ViewFullNote
		}
		if m.overlayBeforeConfig {
			m.overlayBeforeConfig = false
			m.openOverlay()
		}
		return m, tea.Batch(m.loadItems, m.shapeCmd())
	case "ctrl+c":
		return m, tea.Quit
	case "g":
		for i, it := range m.configItems {
			if it.Kind != ConfigHeader {
				m.configCursor = i
				break
			}
		}
	case "G":
		for i := len(m.configItems) - 1; i >= 0; i-- {
			if m.configItems[i].Kind != ConfigHeader {
				m.configCursor = i
				break
			}
		}
	case "j", "down":
		m.configMoveDown()
	case "k", "up":
		m.configMoveUp()
	case "l", "right", "enter":
		return m.configCycleForward()
	case "h", "left":
		return m.configCycleBackward()
	}
	return m, nil
}

func (m *Model) configMoveDown() {
	for i := m.configCursor + 1; i < len(m.configItems); i++ {
		if m.configItems[i].Kind != ConfigHeader {
			m.configCursor = i
			return
		}
	}
}

func (m *Model) configMoveUp() {
	for i := m.configCursor - 1; i >= 0; i-- {
		if m.configItems[i].Kind != ConfigHeader {
			m.configCursor = i
			return
		}
	}
}

func (m Model) configCycleForward() (Model, tea.Cmd) {
	if m.configCursor >= len(m.configItems) {
		return m, nil
	}
	item := &m.configItems[m.configCursor]

	switch item.Kind {
	case ConfigCycle:
		item.Value = cycleOption(item.Options, item.Value, 1)
		return m, m.applyConfigChange(item.Key, item.Value)
	case ConfigAction:
		if item.Key == "open_editor" {
			return m, m.openConfig()
		}
		if item.Key == "accounts" {
			m.openAccounts()
			return m, nil
		}
	case ConfigInput:
		m.showInput = true
		m.inputMode = "config_" + item.Key
		if item.Value == "(none)" || strings.HasPrefix(item.Value, "***") {
			m.inputValue = ""
		} else {
			m.inputValue = item.Value
		}
		return m, nil
	}
	return m, nil
}

func (m Model) configCycleBackward() (Model, tea.Cmd) {
	if m.configCursor >= len(m.configItems) {
		return m, nil
	}
	item := &m.configItems[m.configCursor]

	if item.Kind == ConfigCycle {
		item.Value = cycleOption(item.Options, item.Value, -1)
		return m, m.applyConfigChange(item.Key, item.Value)
	}
	return m, nil
}

func (m *Model) applyConfigChange(key, value string) tea.Cmd {
	cfg := m.configCfg
	if cfg == nil {
		cfg = config.Load()
		m.configCfg = cfg
	}
	themeChanged := false
	var cmd tea.Cmd

	switch key {
	case "theme_mode":
		cfg.ThemeMode = value
		themeChanged = true
		// Rebuild items to update available theme lists
		m.configItems = m.buildConfigItems(cfg)
	case "dark_theme":
		cfg.DarkTheme = value
		themeChanged = true
	case "light_theme":
		cfg.LightTheme = value
		themeChanged = true
	case "show_line_numbers":
		cfg.ShowLineNumbers = value == "on"
	case "cursor_style":
		cfg.CursorStyle = value
	case "preview_lines":
		cfg.PreviewLines = parseInt(value)
	case "default_search_type":
		cfg.SearchType = value
		m.searchType = value
	case "editor":
		if parts := strings.Fields(value); len(parts) > 0 {
			cfg.Editor, cfg.EditorArgs = parts[0], parts[1:]
		}
	case "show_preview":
		cfg.ShowPreview = value == "on"
		m.previewHidden = !cfg.ShowPreview
	case "show_legend":
		cfg.ShowLegend = value == "on"
		m.legendOn = cfg.ShowLegend
	case "zen":
		cfg.Zen = value == "on"
		m.zen = cfg.Zen
	case "pane_nav":
		cfg.PaneNav = value == "on"
		m.paneNav = cfg.PaneNav
		if !m.paneNav {
			m.pane = PaneList
		}
	case "mouse":
		cfg.Mouse = value == "on"
		m.mouseOn = cfg.Mouse
		cmd = tea.DisableMouse
		if m.mouseOn {
			cmd = tea.EnableMouseCellMotion
		}
	case "show_hidden":
		cfg.ShowHidden = value == "on"
		m.showHidden = cfg.ShowHidden
	case "books_dir":
		if v := strings.TrimSpace(value); v != "" && !strings.Contains(v, "..") {
			cfg.BooksDir = strings.TrimRight(v, "/")
		}
	case "default_editor":
		cfg.DefaultEditor = value
	case "open_in":
		cfg.OpenIn = value
	case "escape":
		cfg.EscapeSequences = nil
		for _, part := range strings.Split(value, ",") {
			if v, err := vim.ValidateEscapeSequence(part); err == nil && len(cfg.EscapeSequences) < 8 {
				cfg.EscapeSequences = append(cfg.EscapeSequences, v)
			}
		}
	case "escape_timeout_ms":
		fmt.Sscan(value, &cfg.EscapeTimeoutMs)
	case "autosave":
		cfg.Autosave = value == "on"
	case "clipboard_sync":
		cfg.ClipboardSync = value == "on"
	case "vimrc":
		cfg.Vimrc = value == "on"
	case "reduce_motion":
		cfg.ReduceMotion = value == "on"
		m.mo.setReduced(cfg.ReduceMotion)
	}

	_ = cfg.Save()
	m.cfg = cfg
	m.applyEditorSettings()

	if themeChanged {
		theme.Resolve(cfg.ThemeMode, cfg.DarkTheme, cfg.LightTheme)
		ApplyTheme()
		// Rebuild the glamour renderer with the new theme style
		renderer, err := glamour.NewTermRenderer(
			glamour.WithStylePath(glamourStyle()),
			glamour.WithWordWrap(100),
		)
		if err == nil {
			m.renderer = renderer
		}
	}
	return cmd
}

// escValue shows the escape sequences ("jj, jk" or "(none)").
func escValue(seqs []string) string {
	if len(seqs) == 0 {
		return "(none)"
	}
	return strings.Join(seqs, ",")
}

// vimrcSummary is "(~/.config/lumi/vimrc: 3 maps, 1 error)".
func vimrcSummary() string {
	data, err := os.ReadFile(config.VimrcPath())
	if err != nil {
		return "(no ~/.config/lumi/vimrc)"
	}
	rc := vim.ParseVimrc(string(data), vim.DefaultOptions())
	s := fmt.Sprintf("(%d maps", len(rc.Maps))
	if n := rc.ErrorCount(); n > 0 {
		s += fmt.Sprintf(", %d error(s)", n)
	}
	return s + ")"
}

// applyEditorSettings pushes changed settings into the open buffers.
func (m *Model) applyEditorSettings() {
	if m.ws == nil {
		return
	}
	for _, b := range m.ws.bufs {
		b.eng.Opts = m.engineOptions()
	}
}

func cycleOption(options []string, current string, direction int) string {
	if len(options) == 0 {
		return current
	}
	idx := 0
	for i, o := range options {
		if o == current {
			idx = i
			break
		}
	}
	idx += direction
	if idx < 0 {
		idx = len(options) - 1
	}
	if idx >= len(options) {
		idx = 0
	}
	return options[idx]
}

func parseInt(s string) int {
	switch s {
	case "5":
		return 5
	case "10":
		return 10
	case "15":
		return 15
	case "20":
		return 20
	default:
		return 10
	}
}

func boolStr(b bool) string {
	if b {
		return "on"
	}
	return "off"
}

// openConfig opens ~/.config/lumi/config.yaml in $EDITOR (written
// with defaults first when missing).
func (m Model) openConfig() tea.Cmd {
	home, err := os.UserHomeDir()
	if err != nil {
		// No home dir, no global config — silently bail. Pre-existing
		// code already swallowed MkdirAll errors here.
		return nil
	}
	configDir := filepath.Join(home, ".config", "lumi")
	configPath := filepath.Join(configDir, "config.yaml")

	os.MkdirAll(configDir, 0755)

	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		defaultConfig := strings.Join([]string{
			"# Lumi Configuration",
			"",
			"# Editor command with args",
			"editor: nvim",
			"",
			"# Theme (dark or light)",
			"theme: dark",
			"",
			"# TUI Settings",
			"show_line_numbers: false",
			"cursor_style: block",
			"preview_lines: 10",
			"default_search_type: filename",
			"",
		}, "\n")
		os.WriteFile(configPath, []byte(defaultConfig), 0644)
	}

	cfg := config.Load()
	args := append(cfg.EditorArgs, configPath)
	return tea.ExecProcess(exec.Command(cfg.Editor, args...), nil)
}
