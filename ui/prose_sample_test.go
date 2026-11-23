package ui

const proseSample = `---
id: guide
title: Guide
tags: [docs]
---

# Field guide

Some *prose* with **bold**, ` + "`code`" + ` and a [link](https://example.com).[^1]

## Tasks

- [ ] write the spec
- [x] ship the TUI
- plain bullet

> [!warning] Careful
> Lineage changes are **never** merged.

> [!note]
> A note callout.

| col | value |
|-----|-------|
| a   | 1     |

` + "```go\nfunc main() { println(\"hi\") }\n```" + `

[^1]: The footnote text.
`
