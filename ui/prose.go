package ui

import (
	"fmt"
	"hash/fnv"
	"regexp"
	"strings"
	"sync"

	chromastyles "github.com/alecthomas/chroma/v2/styles"
	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/glamour/ansi"
	glamourstyles "github.com/charmbracelet/glamour/styles"
	"github.com/charmbracelet/lipgloss"
	"github.com/vinizap/lumi/tui-client/theme"
)

// Prose rendering for the preview column: the same reading spirit as
// the web/Apple prose stylesheet, terminal-native. Glamour does the
// CommonMark/GFM work (headings, lists, task lists, tables, code with
// chroma highlighting); lumi adds what goldmark's default set lacks:
//
//   - callouts (`> [!note] Title` …) drawn as coloured rounded boxes;
//   - footnotes (`[^1]` refs → superscripts, definitions collected
//     into a "notas" block at the end);
//   - a theme-derived style (heading/link/code colours from the active
//     lumi theme, ☐/☑ task markers, no `#` prefixes on headings).

// proseCache memoises renders by (theme, width, content) and keeps one
// glamour renderer per (theme, width). Shared by Model copies.
type proseCache struct {
	mu        sync.Mutex
	renderers map[string]*glamour.TermRenderer
	out       map[uint64]string
	order     []uint64
}

const proseCacheCap = 64

func newProseCache() *proseCache {
	return &proseCache{renderers: map[string]*glamour.TermRenderer{}, out: map[uint64]string{}}
}

// render returns the prose rendering of md at width columns.
func (c *proseCache) render(md string, width int) string {
	if width < 8 {
		width = 8
	}
	if c == nil {
		return renderProse(nil, md, width)
	}
	h := fnv.New64a()
	fmt.Fprintf(h, "%s\x00%d\x00%s", theme.Current.Name, width, md)
	key := h.Sum64()
	c.mu.Lock()
	defer c.mu.Unlock()
	if s, ok := c.out[key]; ok {
		return s
	}
	s := renderProse(c, md, width)
	c.out[key] = s
	c.order = append(c.order, key)
	if len(c.order) > proseCacheCap {
		delete(c.out, c.order[0])
		c.order = c.order[1:]
	}
	return s
}

func (c *proseCache) renderer(width int) *glamour.TermRenderer {
	key := fmt.Sprintf("%s/%d", theme.Current.Name, width)
	if c != nil {
		if r, ok := c.renderers[key]; ok {
			return r
		}
	}
	r, err := glamour.NewTermRenderer(glamour.WithStyles(proseStyle()), glamour.WithWordWrap(width))
	if err != nil {
		return nil
	}
	if c != nil {
		c.renderers[key] = r
	}
	return r
}

func sp(s string) *string { return &s }
func bp(b bool) *bool     { return &b }
func up(u uint) *uint     { return &u }

// chromaStyleFor maps a lumi theme to the closest chroma style.
func chromaStyleFor(t theme.Theme) string {
	candidates := map[string]string{
		"tokyo-night":      "tokyonight-night",
		"tokyo-day":        "tokyonight-day",
		"catppuccin-mocha": "catppuccin-mocha",
		"catppuccin-latte": "catppuccin-latte",
		"dracula":          "dracula",
	}
	if name, ok := candidates[t.Name]; ok {
		if _, found := chromastyles.Registry[name]; found {
			return name
		}
	}
	if t.IsDark {
		return "github-dark"
	}
	return "github"
}

// proseStyle derives a glamour style from the active lumi theme.
func proseStyle() ansi.StyleConfig {
	t := theme.Current
	cfg := glamourstyles.DarkStyleConfig
	if !t.IsDark {
		cfg = glamourstyles.LightStyleConfig
	}
	col := func(c lipgloss.Color) *string { return sp(string(c)) }

	cfg.Document = ansi.StyleBlock{StylePrimitive: ansi.StylePrimitive{Color: col(t.Text)}, Margin: up(0)}
	cfg.Paragraph = ansi.StyleBlock{}
	cfg.Heading = ansi.StyleBlock{StylePrimitive: ansi.StylePrimitive{BlockSuffix: "\n", Color: col(t.Primary), Bold: bp(true)}}
	cfg.H1 = ansi.StyleBlock{StylePrimitive: ansi.StylePrimitive{Color: col(t.Primary), Bold: bp(true)}}
	cfg.H2 = ansi.StyleBlock{StylePrimitive: ansi.StylePrimitive{Color: col(t.Secondary), Bold: bp(true)}}
	cfg.H3 = ansi.StyleBlock{StylePrimitive: ansi.StylePrimitive{Color: col(t.Accent), Bold: bp(true)}}
	cfg.H4 = ansi.StyleBlock{StylePrimitive: ansi.StylePrimitive{Color: col(t.Text), Bold: bp(true)}}
	cfg.H5 = ansi.StyleBlock{StylePrimitive: ansi.StylePrimitive{Color: col(t.TextDim), Bold: bp(true)}}
	cfg.H6 = ansi.StyleBlock{StylePrimitive: ansi.StylePrimitive{Color: col(t.TextDim), Italic: bp(true)}}
	cfg.Link = ansi.StylePrimitive{Color: col(t.Accent), Underline: bp(true)}
	cfg.LinkText = ansi.StylePrimitive{Color: col(t.Accent), Bold: bp(true)}
	cfg.Strong = ansi.StylePrimitive{Bold: bp(true)}
	cfg.Emph = ansi.StylePrimitive{Italic: bp(true)}
	cfg.Strikethrough = ansi.StylePrimitive{CrossedOut: bp(true), Color: col(t.TextDim)}
	cfg.Code = ansi.StyleBlock{StylePrimitive: ansi.StylePrimitive{Color: col(t.Accent), BackgroundColor: col(t.SelectedBg), Prefix: " ", Suffix: " "}}
	cfg.CodeBlock = ansi.StyleCodeBlock{
		StyleBlock: ansi.StyleBlock{StylePrimitive: ansi.StylePrimitive{Color: col(t.Text)}, Margin: up(1)},
		Theme:      chromaStyleFor(t),
	}
	cfg.BlockQuote = ansi.StyleBlock{
		StylePrimitive: ansi.StylePrimitive{Color: col(t.TextDim), Italic: bp(true)},
		Indent:         up(1),
		IndentToken:    sp("│ "),
	}
	cfg.HorizontalRule = ansi.StylePrimitive{Color: col(t.Separator), Format: "\n────────\n"}
	cfg.Item = ansi.StylePrimitive{BlockPrefix: "• "}
	cfg.Enumeration = ansi.StylePrimitive{BlockPrefix: ". "}
	cfg.Task = ansi.StyleTask{Ticked: "☑ ", Unticked: "☐ "}
	cfg.Table.StyleBlock = ansi.StyleBlock{StylePrimitive: ansi.StylePrimitive{Color: col(t.Text)}}
	return cfg
}

// --- preprocessing ---

var (
	calloutHead = regexp.MustCompile(`^\s{0,3}>\s*\[!([A-Za-z]+)\][+-]?\s*(.*)$`)
	footDef     = regexp.MustCompile(`^\[\^([^\]\s]+)\]:\s?(.*)$`)
	footRef     = regexp.MustCompile(`\[\^([^\]\s]+)\]`)
	fenceLine   = regexp.MustCompile("^\\s{0,3}(```|~~~)")
)

type proseSeg struct {
	callout string // "" = plain markdown
	title   string
	body    string
}

// splitProse cuts md into plain and callout segments and pulls the
// footnote definitions out (in order of first definition).
func splitProse(md string) (segs []proseSeg, notes []string, ids map[string]int) {
	ids = map[string]int{}
	lines := strings.Split(md, "\n")
	var plain []string
	flush := func() {
		if len(plain) > 0 {
			segs = append(segs, proseSeg{body: strings.Join(plain, "\n")})
			plain = nil
		}
	}
	inFence := false
	for i := 0; i < len(lines); i++ {
		line := lines[i]
		if fenceLine.MatchString(line) {
			inFence = !inFence
			plain = append(plain, line)
			continue
		}
		if inFence {
			plain = append(plain, line)
			continue
		}
		if m := footDef.FindStringSubmatch(line); m != nil {
			text := m[2]
			for i+1 < len(lines) && (strings.HasPrefix(lines[i+1], "    ") || strings.HasPrefix(lines[i+1], "\t")) {
				i++
				text += " " + strings.TrimSpace(lines[i])
			}
			if _, ok := ids[m[1]]; !ok {
				ids[m[1]] = len(ids) + 1
			}
			for len(notes) < ids[m[1]] {
				notes = append(notes, "")
			}
			notes[ids[m[1]]-1] = text
			continue
		}
		if m := calloutHead.FindStringSubmatch(line); m != nil {
			flush()
			var body []string
			for i+1 < len(lines) && strings.HasPrefix(strings.TrimLeft(lines[i+1], " "), ">") {
				i++
				l := strings.TrimLeft(lines[i], " ")
				l = strings.TrimPrefix(l, ">")
				l = strings.TrimPrefix(l, " ")
				body = append(body, l)
			}
			segs = append(segs, proseSeg{callout: strings.ToLower(m[1]), title: strings.TrimSpace(m[2]), body: strings.Join(body, "\n")})
			continue
		}
		plain = append(plain, line)
	}
	flush()
	return segs, notes, ids
}

var superDigits = []rune("⁰¹²³⁴⁵⁶⁷⁸⁹")

func superscript(n int) string {
	var b strings.Builder
	for _, r := range fmt.Sprint(n) {
		b.WriteRune(superDigits[r-'0'])
	}
	return b.String()
}

// replaceFootRefs turns `[^id]` into a superscript number (numbering
// ids that are referenced before or without a definition, too).
func replaceFootRefs(s string, ids map[string]int) string {
	if !strings.Contains(s, "[^") {
		return s
	}
	var out strings.Builder
	inFence := false
	for i, line := range strings.Split(s, "\n") {
		if i > 0 {
			out.WriteByte('\n')
		}
		if fenceLine.MatchString(line) {
			inFence = !inFence
		}
		if inFence {
			out.WriteString(line)
			continue
		}
		out.WriteString(footRef.ReplaceAllStringFunc(line, func(ref string) string {
			id := footRef.FindStringSubmatch(ref)[1]
			n, ok := ids[id]
			if !ok {
				n = len(ids) + 1
				ids[id] = n
			}
			return superscript(n)
		}))
	}
	return out.String()
}

// calloutKind maps a callout type to (icon, label, colour).
func calloutKind(kind string) (string, string, lipgloss.Color) {
	t := theme.Current
	switch kind {
	case "tip", "hint", "important", "success", "check", "done":
		return "✦", kind, t.Accent
	case "warning", "caution", "attention":
		return "▲", kind, t.Warning
	case "danger", "error", "bug", "failure", "fail", "missing":
		return "✖", kind, t.Error
	case "question", "help", "faq":
		return "?", kind, t.Secondary
	case "quote", "cite", "example", "abstract", "summary", "tldr":
		return "❝", kind, t.TextDim
	case "todo":
		return "☐", kind, t.Info
	default: // note, info
		return "ℹ", kind, t.Info
	}
}

func trimBlank(s string) string {
	lines := strings.Split(s, "\n")
	for len(lines) > 0 && strings.TrimSpace(stripAnsi(lines[0])) == "" {
		lines = lines[1:]
	}
	for len(lines) > 0 && strings.TrimSpace(stripAnsi(lines[len(lines)-1])) == "" {
		lines = lines[:len(lines)-1]
	}
	return strings.Join(lines, "\n")
}

func glamourRender(c *proseCache, md string, width int) string {
	r := c.renderer(width)
	if r == nil {
		return md
	}
	out, err := r.Render(md)
	if err != nil {
		return md
	}
	return trimBlank(out)
}

// renderProse renders md for a column of the given width.
func renderProse(c *proseCache, md string, width int) string {
	segs, notes, ids := splitProse(md)
	var parts []string
	for _, sg := range segs {
		body := replaceFootRefs(sg.body, ids)
		if sg.callout == "" {
			if strings.TrimSpace(body) == "" {
				continue
			}
			if out := glamourRender(c, body, width); out != "" {
				parts = append(parts, out)
			}
			continue
		}
		icon, label, color := calloutKind(sg.callout)
		title := sg.title
		if title == "" {
			title = strings.ToUpper(label[:1]) + label[1:]
		}
		head := lipgloss.NewStyle().Foreground(color).Bold(true).Render(icon + " " + title)
		inner := head
		if strings.TrimSpace(body) != "" {
			inner += "\n" + glamourRender(c, body, width-4)
		}
		box := lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(color).
			Padding(0, 1).
			Width(width - 2).
			Render(inner)
		parts = append(parts, box)
	}
	if len(notes) > 0 {
		var b strings.Builder
		b.WriteString("---\n\n")
		for i, n := range notes {
			if n == "" {
				continue
			}
			fmt.Fprintf(&b, "%s %s  \n", superscript(i+1), replaceFootRefs(n, ids))
		}
		parts = append(parts, glamourRender(c, b.String(), width))
	}
	return strings.Join(parts, "\n\n")
}

// inline renders one line of markdown (kanban card text) through the
// same prose system and returns it as plain, single-spaced text.
func (c *proseCache) inline(md string) string {
	out := stripAnsi(c.render(md, 400))
	return strings.Join(strings.Fields(out), " ")
}
