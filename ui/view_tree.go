package ui

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/vinizap/lumi/tui-client/filesystem"
	"github.com/vinizap/lumi/tui-client/theme"
)

// renderTree paints the three-column file browser. The widths adapt to
// terminal size so the preview column gets the most room for content,
// the active list is comfortable, and the parent breadcrumb is slim.
//
// Width allocation (when m.width >= 80):
//
//	parent  ≈ 18% (slim breadcrumb)
//	center  ≈ 35% (active list)
//	preview ≈ 47% (note metadata + excerpt)
//
// Below 80 columns the parent column collapses to give the active list
// and preview equal share. Below 50 columns the preview is hidden too;
// the user gets a single-column file list that still works.
//
// The preview column can also be hidden explicitly with `zp` (yazi
// parity); the freed width goes to the active list.
func (m Model) renderTree() string {
	leftWidth, centerWidth, rightWidth, sepWidth := treeColumnWidths(m.width, m.previewHidden)

	var s strings.Builder

	// Header bar: lumi · <vault-name> · <path> · N items
	s.WriteString(m.renderTreeHeader())
	s.WriteByte('\n')

	// Thin separator under the header.
	sepLine := lipgloss.NewStyle().
		Foreground(theme.Current.Separator).
		Render(strings.Repeat("─", m.width))
	s.WriteString(sepLine)
	s.WriteByte('\n')

	// Three columns. Reserve 3 lines (header + separator + cmd bar).
	colHeight := m.height - 3
	if colHeight < 1 {
		colHeight = 1
	}

	sep := lipgloss.NewStyle().
		Foreground(theme.Current.Separator).
		Render(" │ ")
	_ = sepWidth // separator visual width baked into the layout maths

	if leftWidth == 0 && rightWidth == 0 {
		// Single-column mode (very narrow terminals).
		s.WriteString(m.renderCenterCol(centerWidth, colHeight))
		return s.String()
	}

	if leftWidth == 0 {
		// Two-column mode (medium-narrow terminals): list + preview.
		columns := lipgloss.JoinHorizontal(
			lipgloss.Top,
			m.renderCenterCol(centerWidth, colHeight),
			sep,
			m.renderPreviewCol(rightWidth, colHeight),
		)
		s.WriteString(columns)
		return s.String()
	}

	if rightWidth == 0 {
		// Preview hidden (`zp`): parent breadcrumb + active list only.
		columns := lipgloss.JoinHorizontal(
			lipgloss.Top,
			m.renderParentCol(leftWidth, colHeight),
			sep,
			m.renderCenterCol(centerWidth, colHeight),
		)
		s.WriteString(columns)
		return s.String()
	}

	columns := lipgloss.JoinHorizontal(
		lipgloss.Top,
		m.renderParentCol(leftWidth, colHeight),
		sep,
		m.renderCenterCol(centerWidth, colHeight),
		sep,
		m.renderPreviewCol(rightWidth, colHeight),
	)
	s.WriteString(columns)
	return s.String()
}

// treeColumnWidths returns (left, center, right, sepEach) for a given
// terminal width. The function is the single source of truth for the
// responsive layout breakpoints; callers stay declarative.
//
// hidePreview forces right = 0 (the `zp` toggle); the reclaimed width
// goes to the active list so long titles stop truncating.
func treeColumnWidths(total int, hidePreview bool) (left, center, right, sep int) {
	const sepEach = 3 // " │ "
	switch {
	case total < 50:
		// Single column: just the center list.
		return 0, total, 0, 0
	case total < 80:
		if hidePreview {
			// Preview toggled off: single full-width list.
			return 0, total, 0, 0
		}
		// Two columns: list + preview, even split.
		right = (total - sepEach) / 2
		center = total - sepEach - right
		return 0, center, right, sepEach
	case hidePreview:
		// Preview toggled off: slim parent breadcrumb + wide list.
		left = total * 18 / 100
		if left < 14 {
			left = 14
		}
		center = total - left - sepEach
		return left, center, 0, sepEach
	default:
		// Three columns. Bias preview wider so notes have room to show.
		left = total * 18 / 100
		if left < 14 {
			left = 14
		}
		right = total * 42 / 100
		if right < 30 {
			right = 30
		}
		center = total - left - right - 2*sepEach
		// Make sure the active list never collapses below readable.
		if center < 24 {
			center = 24
			right = total - left - center - 2*sepEach
			if right < 0 {
				right = 0
			}
		}
		return left, center, right, sepEach
	}
}

// renderTreeHeader paints the top bar:
//
//	lumi · <vault-name> · <relative-path> · 22 items
//
// where:
//
//   - vault-name is the basename of the workspace root (so users can
//     tell which vault they're in even when several look similar).
//   - relative-path collapses the workspace prefix to "~"; only shown
//     when the user has navigated below the root.
//   - the item count reflects the active list, not the whole tree, so
//     the user can confirm pagination expectations at a glance.
//
// At narrow widths the segments are dropped right-to-left (count first,
// then path) so the workspace name is the last thing to disappear.
func (m Model) renderTreeHeader() string {
	logo := lipgloss.NewStyle().
		Bold(true).
		Foreground(primaryColor).
		Render("lumi")

	vaultName := filepath.Base(m.rootDir)
	if vaultName == "" || vaultName == "." || vaultName == "/" {
		vaultName = "(root)"
	}
	vault := lipgloss.NewStyle().
		Foreground(accentColor).
		Bold(true).
		Render(vaultName)

	pathRel := strings.TrimPrefix(m.currentDir, m.rootDir)
	pathRel = strings.TrimPrefix(pathRel, string(filepath.Separator))

	itemCount := fmt.Sprintf("%d items", len(m.items))
	count := lipgloss.NewStyle().
		Foreground(mutedColor).
		Render(itemCount)

	dot := lipgloss.NewStyle().
		Foreground(theme.Current.Separator).
		Render(" · ")

	// Build the segments; drop right-to-left as width shrinks.
	parts := []string{logo, vault}
	if pathRel != "" {
		path := lipgloss.NewStyle().
			Foreground(theme.Current.TextDim).
			Render("~/" + pathRel)
		parts = append(parts, path)
	}
	parts = append(parts, count)

	join := func(p []string) string {
		return strings.Join(p, dot)
	}

	header := join(parts)
	if lipgloss.Width(header) <= m.width-2 {
		return " " + header
	}
	// Drop the count, then the path, until it fits.
	for len(parts) > 2 {
		parts = parts[:len(parts)-1]
		header = join(parts)
		if lipgloss.Width(header) <= m.width-2 {
			return " " + header
		}
	}
	return " " + truncate(header, m.width-2)
}

func (m Model) displayPath() string {
	pathDisplay := strings.TrimPrefix(m.currentDir, m.rootDir)
	if pathDisplay == "" {
		return "~"
	}
	return "~" + pathDisplay
}

// renderParentCol paints the slim breadcrumb-style column on the left
// showing the parent directory's contents. The current dir is marked
// with a `▸` and accent colour so the user can see where they are in
// the parent at a glance.
//
// All names are truncated (not wrapped) at the column width — this
// avoids the mid-word wrap the previous lipgloss-Width approach
// produced on long folder names like "Major Conferences & Venues
// (publish-or-perish guide)".
func (m Model) renderParentCol(width, height int) string {
	if m.currentDir == m.rootDir || width < 4 {
		// Nothing useful to show.
		return lipgloss.NewStyle().Width(width).Height(height).Render("")
	}

	var s strings.Builder

	parentDir := filepath.Dir(m.currentDir)
	parentItems, _ := filesystem.ListFolders(parentDir)
	parentNotes, _ := filesystem.ListNotes(parentDir)

	maxItems := height - 1
	if maxItems < 1 {
		maxItems = 1
	}
	count := 0
	innerWidth := width - 1 // 1-col left padding

	for _, f := range parentItems {
		if count >= maxItems {
			break
		}
		isCurrent := f.Path == m.currentDir
		prefix := "  "
		if isCurrent {
			prefix = "▸ "
		}
		name := truncate(f.Name+"/", innerWidth-2)
		line := prefix + name
		style := lipgloss.NewStyle().Foreground(secondaryColor)
		if isCurrent {
			style = style.Bold(true).Foreground(accentColor)
		}
		s.WriteString(style.Render(" " + line))
		s.WriteByte('\n')
		count++
	}

	for _, n := range parentNotes {
		if count >= maxItems {
			break
		}
		name := truncate(n.Title, innerWidth-2)
		s.WriteString(lipgloss.NewStyle().
			Foreground(theme.Current.TextDim).
			Render("   " + name))
		s.WriteByte('\n')
		count++
	}

	return lipgloss.NewStyle().
		Width(width).
		Height(height).
		Render(s.String())
}

// renderCenterCol paints the active list. Folders get a `▸` arrow,
// notes get a subtle bullet so the eye can scan the two sets without
// reading every name. The selected row inverts the background.
//
// A small "k/N" position indicator appears at the bottom when the list
// is taller than the viewport — without it, it's easy to miss that
// there are more items below.
func (m Model) renderCenterCol(width, height int) string {
	var s strings.Builder

	// Reserve one row at the bottom for the position indicator (only
	// rendered when the list overflows the viewport).
	overflow := len(m.items) > height-1
	rowsForList := height - 1
	if overflow {
		rowsForList--
	}
	if rowsForList < 1 {
		rowsForList = 1
	}

	start := 0
	if m.cursor >= rowsForList {
		start = m.cursor - rowsForList + 1
	}

	innerWidth := width - 1 // 1-col left padding
	if innerWidth < 4 {
		innerWidth = 4
	}

	end := start + rowsForList
	if end > len(m.items) {
		end = len(m.items)
	}

	for i := start; i < end; i++ {
		item := m.items[i]
		s.WriteString(m.renderCenterRow(item, innerWidth, i == m.cursor))
		s.WriteByte('\n')
	}

	if len(m.items) == 0 {
		s.WriteString(DimItemStyle.Render("  (empty)"))
		s.WriteByte('\n')
	}

	if overflow {
		pos := fmt.Sprintf("%d / %d", m.cursor+1, len(m.items))
		marker := lipgloss.NewStyle().
			Foreground(mutedColor).
			Italic(true).
			Render(" " + pos)
		s.WriteString(marker)
	}

	return lipgloss.NewStyle().
		Width(width).
		Height(height).
		Render(s.String())
}

// renderCenterRow returns a single styled row for the active list. The
// folder/note distinction is carried by both the prefix character and
// the colour so it survives in monochrome terminals.
func (m Model) renderCenterRow(item Item, innerWidth int, selected bool) string {
	const prefixWidth = 4 // " > " or "   ▸/·" + trailing space
	textWidth := innerWidth - prefixWidth
	if textWidth < 4 {
		textWidth = 4
	}

	name := item.Name
	if item.IsFolder {
		name = name + "/"
	}
	name = truncate(name, textWidth)

	var glyph, prefix string
	switch {
	case selected:
		glyph = ">"
	case item.IsFolder:
		glyph = "▸"
	default:
		glyph = "·"
	}
	prefix = " " + glyph + " "

	row := prefix + name

	switch {
	case selected:
		// Inverted highlight (accent bg + theme-bg fg). The
		// foreground=accent + background=selectedBg pattern was
		// unreadable on catppuccin / tokyo-night where the two sit
		// in the same palette region. See styles.go comment.
		return lipgloss.NewStyle().
			Foreground(theme.Current.Background).
			Background(accentColor).
			Bold(true).
			Width(innerWidth + 1).
			Render(row)
	case item.IsFolder:
		return lipgloss.NewStyle().
			Foreground(secondaryColor).
			Width(innerWidth + 1).
			Render(row)
	default:
		return lipgloss.NewStyle().
			Foreground(theme.Current.Text).
			Width(innerWidth + 1).
			Render(row)
	}
}

// renderPreviewCol paints the right column. For folders, it shows the
// child listing. For notes, it shows title + date + tags + a wrapped
// excerpt of the body so the user can see what's inside before opening
// the note. The previous version truncated each line individually, which
// produced a column full of "..."; the new version word-wraps at the
// column width and clamps to the available height.
func (m Model) renderPreviewCol(width, height int) string {
	if width < 10 {
		// Too narrow to be useful — render nothing.
		return lipgloss.NewStyle().Width(width).Height(height).Render("")
	}
	if m.cursor >= len(m.items) {
		return lipgloss.NewStyle().Width(width).Height(height).Render("")
	}

	item := m.items[m.cursor]
	innerWidth := width - 2

	if item.IsFolder {
		return m.renderFolderPreview(item, width, height, innerWidth)
	}
	if item.Note != nil {
		return m.renderNotePreview(item, width, height, innerWidth)
	}
	return lipgloss.NewStyle().Width(width).Height(height).Render("")
}

func (m Model) renderFolderPreview(item Item, width, height, innerWidth int) string {
	var s strings.Builder

	// Header
	header := truncate("▸ "+item.Name+"/", innerWidth)
	s.WriteString(lipgloss.NewStyle().
		Foreground(secondaryColor).
		Bold(true).
		Render(" " + header))
	s.WriteString("\n\n")

	subFolders, _ := filesystem.ListFolders(item.Path)
	folderNotes, _ := filesystem.ListNotes(item.Path)
	total := len(subFolders) + len(folderNotes)

	if total == 0 {
		s.WriteString(DimItemStyle.Render("  (empty)"))
		return lipgloss.NewStyle().Width(width).Height(height).Render(s.String())
	}

	noun := "items"
	if total == 1 {
		noun = "item"
	}
	s.WriteString(lipgloss.NewStyle().
		Foreground(mutedColor).
		Render(fmt.Sprintf(" %d %s", total, noun)))
	s.WriteString("\n\n")

	maxPreview := height - 5
	if maxPreview < 1 {
		maxPreview = 1
	}
	count := 0
	for _, f := range subFolders {
		if count >= maxPreview {
			break
		}
		name := truncate("▸ "+f.Name+"/", innerWidth-2)
		s.WriteString(lipgloss.NewStyle().
			Foreground(secondaryColor).
			Render("  " + name))
		s.WriteByte('\n')
		count++
	}
	for _, n := range folderNotes {
		if count >= maxPreview {
			break
		}
		name := truncate("· "+n.Title, innerWidth-2)
		s.WriteString(lipgloss.NewStyle().
			Foreground(theme.Current.TextDim).
			Render("  " + name))
		s.WriteByte('\n')
		count++
	}
	if total > maxPreview {
		s.WriteString(lipgloss.NewStyle().
			Foreground(mutedColor).
			Italic(true).
			Render(fmt.Sprintf("   … %d more", total-maxPreview)))
		s.WriteByte('\n')
	}
	return lipgloss.NewStyle().Width(width).Height(height).Render(s.String())
}

func (m Model) renderNotePreview(item Item, width, height, innerWidth int) string {
	var s strings.Builder

	// Title — bold + accent.
	title := truncate(item.Note.Title, innerWidth)
	s.WriteString(lipgloss.NewStyle().
		Foreground(primaryColor).
		Bold(true).
		Render(" " + title))
	s.WriteByte('\n')

	// Updated-at line.
	meta := lipgloss.NewStyle().
		Foreground(mutedColor).
		Render(" " + item.Note.UpdatedAt.Format("Jan 2, 2006"))
	s.WriteString(meta)
	s.WriteByte('\n')

	// Thin rule.
	if innerWidth >= 4 {
		s.WriteString(lipgloss.NewStyle().
			Foreground(theme.Current.Separator).
			Render(" " + strings.Repeat("─", innerWidth-1)))
	}
	s.WriteByte('\n')

	// Tags.
	if len(item.Note.Tags) > 0 {
		var tagParts []string
		for _, tag := range item.Note.Tags {
			tagParts = append(tagParts, lipgloss.NewStyle().
				Foreground(theme.Current.Accent).
				Render("#"+tag))
		}
		tagLine := truncate(strings.Join(tagParts, " "), innerWidth)
		s.WriteString(" " + tagLine)
		s.WriteString("\n\n")
	} else {
		s.WriteByte('\n')
	}

	// Body excerpt: collapse all paragraphs into one stream of words and
	// word-wrap to the column width. This avoids the previous "every
	// non-empty line truncated to width" behaviour which produced a
	// column full of partial sentences and "..." marks.
	rows := height - 6
	if rows < 1 {
		rows = 1
	}
	body := bodyExcerpt(item.Note.Content)
	wrapped := wrapWords(body, innerWidth)
	if len(wrapped) > rows {
		wrapped = wrapped[:rows]
	}
	for _, line := range wrapped {
		s.WriteString(lipgloss.NewStyle().
			Foreground(theme.Current.TextDim).
			Render(" " + line))
		s.WriteByte('\n')
	}

	return lipgloss.NewStyle().Width(width).Height(height).Render(s.String())
}

// bodyExcerpt produces a single space-collapsed string from the note
// body, suitable for word-wrapping into a preview pane. Headings,
// list-item markers, blockquote markers, and code-fence backticks are
// stripped because they're noise in a 4-line excerpt; the goal is to
// surface what the note is about, not to reproduce its formatting.
func bodyExcerpt(content string) string {
	var b strings.Builder
	inFence := false
	for _, raw := range strings.Split(content, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "```") {
			inFence = !inFence
			continue
		}
		if inFence {
			continue
		}
		// Strip common markdown noise so the excerpt reads like prose.
		line = stripMarkdownPrefix(line)
		if line == "" {
			continue
		}
		if b.Len() > 0 {
			b.WriteByte(' ')
		}
		b.WriteString(line)
		// 600 chars is enough material to fill any reasonable preview;
		// stop early to keep the word-wrap fast on long notes.
		if b.Len() > 600 {
			break
		}
	}
	return b.String()
}

// stripMarkdownPrefix removes leading markdown markers from a single
// line. Conservative — only strips well-known prefixes; never edits
// the middle of the line.
func stripMarkdownPrefix(line string) string {
	// Heading
	for strings.HasPrefix(line, "#") {
		line = strings.TrimPrefix(line, "#")
	}
	// Blockquote
	for strings.HasPrefix(line, ">") {
		line = strings.TrimPrefix(line, ">")
	}
	// List bullets
	switch {
	case strings.HasPrefix(line, "- "):
		line = strings.TrimPrefix(line, "- ")
	case strings.HasPrefix(line, "* "):
		line = strings.TrimPrefix(line, "* ")
	case strings.HasPrefix(line, "+ "):
		line = strings.TrimPrefix(line, "+ ")
	}
	return strings.TrimSpace(line)
}
