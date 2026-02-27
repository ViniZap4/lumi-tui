# lumi-tui

Terminal UI client for [lumi](https://github.com/ViniZap4/lumi) — a local-first, markdown-based note-taking system.

Built with [Go](https://golang.org) and [Bubbletea](https://github.com/charmbracelet/bubbletea).

## Features

- Glamour markdown rendering with syntax highlighting
- Inline image support (timg/chafa/viu)
- Vim-like navigation (hjkl, visual mode, yank)
- Wiki-link following (`[[link]]`)
- Telescope-style search (filename and content)
- Split views (horizontal/vertical)
- External editor integration (`$EDITOR`)
- Themes with live preview

## Build & Run

```bash
go build -o lumi
./lumi /path/to/notes
```

## Part of lumi

This is a component of the [lumi monorepo](https://github.com/ViniZap4/lumi).