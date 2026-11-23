// tui-client/config/config.go
package config

import (
	"fmt"
	"os"
	"path/filepath"
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
`, editorCmd, c.ThemeMode, c.DarkTheme, c.LightTheme, showLn, c.CursorStyle, c.PreviewLines, c.SearchType,
		b(c.ShowPreview), b(c.ShowLegend), b(c.PaneNav), b(c.Mouse), b(c.ShowHidden))

	return os.WriteFile(filepath.Join(configDir, "config.yaml"), []byte(content), 0644)
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
