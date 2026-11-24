# lumi-tui

> **🚧 Work in progress** — lumi is under active development. Features may change, break, or be incomplete.

Terminal UI client for [lumi](https://github.com/ViniZap4/lumi) — a local-first, markdown-based note-taking system.

Built with [Go](https://golang.org) and [Bubbletea](https://github.com/charmbracelet/bubbletea).

## Features

- The floating explorer is the only navigation: a centred overlay with
  yazi Miller columns (parent | list | preview), or a full-height sheet on
  terminals under 100×30. Open it with `-`, `<leader>e`, `:explore`, the
  palette or the `≡ notes` button (mouse); `⏎` opens here, `⌃v`/`⌃s` in a
  split, `Esc`/`q` close. Inside: the same keymap as the web and Apple
  clients (`?` shows it): `j/k gg G ⌃d ⌃u` + counts, `h/l`, `H/L` history,
  `~` root, `/` filter + `n/N`, `a` new (`a/b/name`, `dir/`), `r`
  rename/move, `d` delete, `y/p` copy, `m` move, `.` hidden, `zp` preview,
  `R` refresh, `e/i` edit, `E` the other editor, `⌃h/⌃l` column focus
- Books in the same overlay: `gb` (or `h` at the book root) shows accounts →
  books with their sync state; `[ ]` and `g1–9` switch
- Opening a book shows its last opened note (per book, kept in
  `~/.config/lumi/state/last-notes.json`, never in the book folder);
  otherwise a calm home (book, sync state, recent notes) with the explorer
  open
- Motion that feels alive (harmonica springs): the overlay grows from the
  centre, rows stagger in, new panes slide open, toasts slide, the mode pill
  morphs, the sync dot breathes while syncing and pulses on a remote change.
  Frames tick only while something moves; Settings → "Reduce motion" or
  `NO_MOTION=1` / `REDUCE_MOTION=1` turn it off
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
- Views: prose reader with a read-only vim cursor (`h j k l w b e 0 $ gg G`,
  counts, `/ n N`, `v V ⌃v` selection, `y`/`yy` to the clipboard; `⏎/gf`
  follow the link under the cursor, `⌃w f/F` in a split, `gx` browser,
  `Tab` next link, `K` link card, `⏎`/`x` toggle a task; click a link to
  follow it), source, source+preview (`⌃l` reads the preview with the same
  cursor), kanban boards and JSON Canvas with Reading | Edit | Source
  (`i` edits a board, `Esc` back to Reading); `zv` cycles
- Panes: `⌃w v/s/q/o/=/hjkl/w/p/f/F/z`, `:vs`/`:sp`/`:zoom`, layout kept
  per book; `q` closes a pane (the last one goes to the home); `⌃w z` or
  the header's ⤢ zooms a pane tmux-style
- Floating explorer: footer legend (clickable, item count), kind glyphs
  (▤ note, ▥ board, ◈ canvas; `LUMI_ASCII=1` for ASCII), canvas outline
  and mini-board previews, inline `open · ⇥ right · ⇩ below` actions and a
  right-click menu
- Rendering: frames go out as DEC 2026 synchronized updates with compact
  SGR, so scrolling never flickers
- Accounts and servers inside the TUI (`ga`): sign in/out, remove,
  web address per server
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