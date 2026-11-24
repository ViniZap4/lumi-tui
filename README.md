# lumi-tui

> **🚧 Work in progress** — lumi is under active development. Features may change, break, or be incomplete.

Terminal UI client for [lumi](https://github.com/ViniZap4/lumi) — a local-first, markdown-based note-taking system.

Built with [Go](https://golang.org) and [Bubbletea](https://github.com/charmbracelet/bubbletea).

## Features

- yazi-style Miller columns (parent | list | preview) with the same keymap as
  the web and Apple clients (`?` shows it): `j/k gg G ⌃d ⌃u` + counts, `h/l`,
  `H/L` history, `~` root, `/` filter + `n/N`, `a` new (`a/b/name`, `dir/`),
  `r` rename/move, `d` delete, `y/p` copy, `.` hidden, `zp` preview, `R`
  refresh, `e/i` edit (default editor), `E` the other editor, `space`/`:` palette, `[ ]` books,
  `gb gs g1–9`, `⌃h/⌃l` pane focus, `,` settings
- Prose preview: glamour + chroma highlighting, task boxes, tables,
  `> [!note]` callouts as coloured boxes, footnotes
- Command legend (which-key for `g`/`z`/counts) above a status line with the
  book's sync state and pending pushes
- Mouse: click selects, double-click opens, wheel scrolls (toggle in settings)
- Offline-safe sync: 3-way `/diff` pushes guarded by the note's `crdt_epoch`
  (a recreated note never absorbs an old offline edit; it is kept as
  "(conflito offline)")
- Two first-class editors: the built-in vim editor (modes with cursor
  shapes, motions, operators, counts, text objects, registers incl. the
  system clipboard / OSC 52, `.`, undo/redo, visual `v V ⌃v`, search, `:s`
  with ranges, macros, `jj`-style escape sequences, a safe
  `~/.config/lumi/vimrc` subset) and your `$VISUAL`/`$EDITOR`.
  Settings → "Editor padrão" picks what `i`/`e` open; `E` and `:edit!`
  open the other. Both save through the epoch-checked 3-way push.
- Views: prose reader (`Tab` links, `⏎/gf` follow, `K` link card), source,
  source+preview, kanban board for `kanban-plugin` notes, JSON Canvas
  outline; `zv` cycles
- Panes: `⌃w v/s/q/o/=/hjkl/w/p/f/F`, `:vs`/`:sp`, layout kept per book;
  floating explorer with `-` / `<leader>e`
- Accounts and servers inside the TUI (`ga`): sign in/out, remove,
  web address per server; the books picker shows the sync state
- `lumi open [<book>[/<note>]] [--web]` opens the Lumi app (macOS,
  `lumi://`) or the web client
- Inline images in the reader (timg/chafa/viu), wiki-links, 12 themes

## Build & Run

```bash
go build -o lumi
./lumi /path/to/notes
```

## Documentation

Full docs at [lumi-note.vercel.app/#/docs](https://lumi-note.vercel.app/#/docs).

## Part of lumi

This is a component of the [lumi monorepo](https://github.com/ViniZap4/lumi).