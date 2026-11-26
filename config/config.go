// tui-client/config/config.go
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

type Config struct {
	Editor     string
	EditorArgs []string

	// Theme settings
	ThemeMode  string // "dark", "light", "auto"
	DarkTheme  string
	LightTheme string

	// TUI settings
	ShowLineNumbers bool
	CursorStyle     string // "block", "underline", "bar"
	PreviewLines    int
	SearchType      string // "filename" or "content"

	// Explorer (web/Apple parity)
	ShowPreview bool // preview column (zp toggles it for the session)
	ShowLegend  bool // one-line command legend above the status line
	PaneNav     bool // Ctrl-h/j/k/l pane focus
	Mouse       bool // click / wheel / double-click
	ShowHidden  bool // dot files and folders in listings (`.`)
	Zen         bool // Modo zen: no legend / status line at rest

	// Notes (web/Apple parity, SPEC-PARTE2 §9)
	DefaultEditor   string   // "internal" (built-in vim) | "external" ($VISUAL/$EDITOR or Editor)
	OpenIn          string   // "reader" | "source": what Enter on a note opens
	EscapeSequences []string // insert-mode sequences acting as <Esc> ("jj", "jk")
	EscapeTimeoutMs int      // ms between the keys of a sequence
	Vimrc           bool     // apply ~/.config/lumi/vimrc (safe subset)
	Autosave        bool     // debounced save of built-in edits
	AutosaveMs      int
	ClipboardSync   bool // yank/delete also go to the system clipboard
	ReduceMotion    bool // no spring animations (also NO_MOTION / REDUCE_MOTION)
	// WebOrigins maps a server URL to its web client origin, for
	// `lumi open --web` (default: lumi-api.<host> → lumi.<host>).
	WebOrigins map[string]string
	// BooksDir is where clone-in-place proposes new book folders
	// (`<books_dir>/<slug>`); `~` is expanded at use.
	BooksDir string
}

// VimrcPath is where the built-in editor reads the user's vimrc.
func VimrcPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".config", "lumi", "vimrc")
}

func Load() *Config {
	cfg := &Config{
		Editor:          "nvim",
		EditorArgs:      []string{},
		ThemeMode:       "dark",
		DarkTheme:       "tokyo-night",
		LightTheme:      "catppuccin-latte",
		ShowLineNumbers: false,
		CursorStyle:     "block",
		PreviewLines:    10,
		SearchType:      "filename",
		ShowPreview:     true,
		ShowLegend:      true,
		PaneNav:         true,
		Mouse:           true,
		DefaultEditor:   "internal",
		OpenIn:          "reader",
		EscapeTimeoutMs: 300,
		Vimrc:           true,
		Autosave:        true,
		AutosaveMs:      1000,
		ClipboardSync:   true,
		WebOrigins:      map[string]string{},
		BooksDir:        "~/lumi",
	}

	// UserHomeDir falls back to $HOME on Unix and the registry on
	// Windows. Older code used os.Getenv("HOME") directly, which
	// silently produced paths like "/.config/lumi/..." when the env
	// was unset — a clearer failure mode is to skip loading and
	// return the defaults.
	home, err := os.UserHomeDir()
	if err != nil {
		return cfg
	}
	configPath := filepath.Join(home, ".config", "lumi", "config.yaml")
	data, err := os.ReadFile(configPath)
	if err != nil {
		return cfg
	}

	// Simple YAML parsing
	lines := strings.Split(string(data), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "#") || line == "" {
			continue
		}

		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			continue
		}

		key := strings.TrimSpace(parts[0])
		value := unquoteValue(parts[1])

		switch key {
		case "editor":
			editorParts := strings.Fields(value)
			if len(editorParts) > 0 {
				cfg.Editor = editorParts[0]
				if len(editorParts) > 1 {
					cfg.EditorArgs = editorParts[1:]
				}
			}
		case "theme_mode":
			if value == "dark" || value == "light" || value == "auto" {
				cfg.ThemeMode = value
			}
		case "dark_theme":
			cfg.DarkTheme = value
		case "light_theme":
			cfg.LightTheme = value
		// Legacy: map old "theme" field to theme_mode
		case "theme":
			if value == "dark" || value == "light" || value == "auto" {
				cfg.ThemeMode = value
			}
		case "show_line_numbers":
			cfg.ShowLineNumbers = value == "true"
		case "cursor_style":
			cfg.CursorStyle = value
		case "preview_lines":
			if value == "5" || value == "10" || value == "15" || value == "20" {
				cfg.PreviewLines = parseInt(value)
			}
		case "default_search_type":
			if value == "filename" || value == "content" {
				cfg.SearchType = value
			}
		case "show_preview":
			cfg.ShowPreview = value != "false"
		case "show_legend":
			cfg.ShowLegend = value != "false"
		case "pane_nav":
			cfg.PaneNav = value != "false"
		case "mouse":
			cfg.Mouse = value != "false"
		case "show_hidden":
			cfg.ShowHidden = value == "true"
		case "zen":
			cfg.Zen = value == "true"
		case "default_editor":
			if value == "internal" || value == "external" {
				cfg.DefaultEditor = value
			}
		case "open_in":
			if value == "reader" || value == "source" {
				cfg.OpenIn = value
			}
		case "escape_sequences":
			cfg.EscapeSequences = nil
			for _, s := range strings.Split(value, ",") {
				s = strings.TrimSpace(s)
				if r := []rune(s); len(r) >= 2 && len(r) <= 4 && !strings.ContainsAny(s, " <") && len(cfg.EscapeSequences) < 8 {
					cfg.EscapeSequences = append(cfg.EscapeSequences, s)
				}
			}
		case "escape_timeout_ms":
			if n, err := strconv.Atoi(value); err == nil && n >= 100 && n <= 1000 {
				cfg.EscapeTimeoutMs = n
			}
		case "vimrc":
			cfg.Vimrc = value != "false"
		case "autosave":
			cfg.Autosave = value != "false"
		case "autosave_ms":
			if n, err := strconv.Atoi(value); err == nil && n >= 200 && n <= 10000 {
				cfg.AutosaveMs = n
			}
		case "clipboard_sync":
			cfg.ClipboardSync = value != "false"
		case "reduce_motion":
			cfg.ReduceMotion = value == "true"
		case "books_dir":
			if value != "" {
				cfg.BooksDir = value
			}
		case "web_origins":
			for _, pair := range strings.Fields(value) {
				if k, v, ok := strings.Cut(pair, "="); ok && strings.HasPrefix(v, "http") {
					cfg.WebOrigins[strings.TrimRight(k, "/")] = strings.TrimRight(v, "/")
				}
			}
		}
	}

	return cfg
}

// Save writes the config back to ~/.config/lumi/config.yaml.
func (c *Config) Save() error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("locate home dir: %w", err)
	}
	configDir := filepath.Join(home, ".config", "lumi")
	if err := os.MkdirAll(configDir, 0755); err != nil {
		return err
	}

	editorCmd := c.Editor
	if len(c.EditorArgs) > 0 {
		editorCmd += " " + strings.Join(c.EditorArgs, " ")
	}

	b := func(v bool) string {
		if v {
			return "true"
		}
		return "false"
	}
	showLn := b(c.ShowLineNumbers)

	content := fmt.Sprintf(`# Lumi Configuration

# Editor command with args
editor: %s

# Theme
theme_mode: %s
dark_theme: %s
light_theme: %s

# TUI Settings
show_line_numbers: %s
cursor_style: %s
preview_lines: %d
default_search_type: %s

# Explorer
show_preview: %s
show_legend: %s
pane_nav: %s
mouse: %s
show_hidden: %s
zen: %s

# Notes and the built-in editor
default_editor: %s
open_in: %s
escape_sequences: %s
escape_timeout_ms: %d
vimrc: %s
autosave: %s
autosave_ms: %d
clipboard_sync: %s
reduce_motion: %s
web_origins: %s

# Books: where clone-in-place proposes new book folders
books_dir: %s
`, editorCmd, c.ThemeMode, c.DarkTheme, c.LightTheme, showLn, c.CursorStyle, c.PreviewLines, c.SearchType,
		b(c.ShowPreview), b(c.ShowLegend), b(c.PaneNav), b(c.Mouse), b(c.ShowHidden), b(c.Zen),
		orDefault(c.DefaultEditor, "internal"), orDefault(c.OpenIn, "reader"), strings.Join(c.EscapeSequences, ","),
		c.EscapeTimeoutMs, b(c.Vimrc), b(c.Autosave), c.AutosaveMs, b(c.ClipboardSync), b(c.ReduceMotion), c.webOrigins(),
		orDefault(c.BooksDir, "~/lumi"))

	return os.WriteFile(filepath.Join(configDir, "config.yaml"), []byte(content), 0644)
}

func orDefault(v, d string) string {
	if v == "" {
		return d
	}
	return v
}

func (c *Config) webOrigins() string {
	keys := make([]string, 0, len(c.WebOrigins))
	for k := range c.WebOrigins {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var parts []string
	for _, k := range keys {
		parts = append(parts, k+"="+c.WebOrigins[k])
	}
	return strings.Join(parts, " ")
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
