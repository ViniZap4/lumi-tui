# lumi-tui

> **🚧 Work in progress** — lumi is under active development. Features may change, break, or be incomplete.

Terminal UI client for [lumi](https://github.com/ViniZap4/lumi) — a local-first, markdown-based note-taking system.

Built with [Go](https://golang.org) and [Bubbletea](https://github.com/charmbracelet/bubbletea).

## Features

- yazi-style Miller columns (parent | list | preview) with the same keymap as
  the web and Apple clients (`?` shows it): `j/k gg G ⌃d ⌃u` + counts, `h/l`,
  `H/L` history, `~` root, `/` filter + `n/N`, `a` new (`a/b/name`, `dir/`),
  `r` rename/move, `d` delete, `y/p` copy, `.` hidden, `zp` preview, `R`
  refresh, `e/i` edit in `$EDITOR`, `space`/`:` palette, `[ ]` books,
  `gb gs g1–9`, `⌃h/⌃l` pane focus, `,` settings
- Prose preview: glamour + chroma highlighting, task boxes, tables,
  `> [!note]` callouts as coloured boxes, footnotes
- Command legend (which-key for `g`/`z`/counts) above a status line with the
  book's sync state and pending pushes
- Mouse: click selects, double-click opens, wheel scrolls (toggle in settings)
- Offline-safe sync: 3-way `/diff` pushes guarded by the note's `crdt_epoch`
  (a recreated note never absorbs an old offline edit; it is kept as
  "(conflito offline)")
- Inline images (timg/chafa/viu), wiki-links, split views, 12 themes

## Build & Run

```bash
go build -o lumi
./lumi /path/to/notes
```

## Documentation

Full docs at [lumi-note.vercel.app/#/docs](https://lumi-note.vercel.app/#/docs).

## Part of lumi

This is a component of the [lumi monorepo](https://github.com/ViniZap4/lumi).