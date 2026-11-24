package ui

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/vinizap/lumi/tui-client/domain"
	"github.com/vinizap/lumi/tui-client/filesystem"
)

// noteLink is a link found in a note's markdown.
type noteLink struct {
	line       int    // 0-based source line
	start, end int    // rune columns [start, end)
	text       string // visible text
	target     string // note path/id or URL
	external   bool
}

var (
	reMdLink   = regexp.MustCompile(`!?\[([^\]]*)\]\(([^)\s]+)(?:\s+"[^"]*")?\)`)
	reWikiLink = regexp.MustCompile(`\[\[([^\]|#]+)(?:#[^\]|]*)?(?:\|([^\]]+))?\]\]`)
	reBareURL  = regexp.MustCompile(`https?://[^\s<>()\[\]]+[^\s<>()\[\].,;:!?'"]`)
)

func isExternal(t string) bool {
	l := strings.ToLower(t)
	return strings.HasPrefix(l, "http://") || strings.HasPrefix(l, "https://") || strings.HasPrefix(l, "mailto:")
}

// findLinks returns the links of body in reading order (images and
// fenced code are skipped).
func findLinks(body string) []noteLink {
	var out []noteLink
	inFence := false
	for i, line := range strings.Split(body, "\n") {
		t := strings.TrimSpace(line)
		if strings.HasPrefix(t, "```") || strings.HasPrefix(t, "~~~") {
			inFence = !inFence
			continue
		}
		if inFence {
			continue
		}
		taken := make([]bool, len(line)+1)
		add := func(bs, be int, text, target string) {
			for k := bs; k < be; k++ {
				if taken[k] {
					return
				}
			}
			for k := bs; k < be; k++ {
				taken[k] = true
			}
			out = append(out, noteLink{line: i, start: runeCol(line, bs), end: runeCol(line, be), text: text,
				target: target, external: isExternal(target)})
		}
		for _, m := range reWikiLink.FindAllStringSubmatchIndex(line, -1) {
			target := line[m[2]:m[3]]
			text := target
			if m[4] >= 0 {
				text = line[m[4]:m[5]]
			}
			add(m[0], m[1], text, strings.TrimSpace(target))
		}
		for _, m := range reMdLink.FindAllStringSubmatchIndex(line, -1) {
			if line[m[0]] == '!' {
				for k := m[0]; k < m[1]; k++ {
					taken[k] = true
				}
				continue
			}
			add(m[0], m[1], line[m[2]:m[3]], line[m[4]:m[5]])
		}
		for _, m := range reBareURL.FindAllStringIndex(line, -1) {
			add(m[0], m[1], line[m[0]:m[1]], line[m[0]:m[1]])
		}
	}
	// stable reading order
	for a := 1; a < len(out); a++ {
		for b := a; b > 0 && (out[b].line < out[b-1].line || out[b].line == out[b-1].line && out[b].start < out[b-1].start); b-- {
			out[b], out[b-1] = out[b-1], out[b]
		}
	}
	return out
}

func runeCol(s string, byteOff int) int { return len([]rune(s[:byteOff])) }

// linkAt returns the link covering (line, col).
func linkAt(links []noteLink, line, col int) (noteLink, bool) {
	for _, l := range links {
		if l.line == line && col >= l.start && col < l.end {
			return l, true
		}
	}
	return noteLink{}, false
}

// resolveLink finds the note a link points to, relative to from (the
// note holding the link), inside the book.
func (m *Model) resolveLink(from, target string) *domain.Note {
	target = strings.TrimSpace(target)
	if i := strings.IndexAny(target, "#?"); i > 0 {
		target = target[:i]
	}
	if dec, err := unescapePath(target); err == nil {
		target = dec
	}
	if from != "" && filepath.IsLocal(filepath.FromSlash(target)) {
		cand := filepath.Join(filepath.Dir(from), filepath.FromSlash(target))
		for _, c := range []string{cand, cand + ".md"} {
			if !strings.HasSuffix(c, ".md") && !strings.HasSuffix(c, ".canvas") {
				continue
			}
			if pathIsInsideVault(m.rootDir, c) {
				if n, err := filesystem.ReadNote(c); err == nil {
					return n
				}
			}
		}
	}
	saved := m.fullNote
	defer func() { m.fullNote = saved }()
	m.fullNote = nil
	return m.findNoteByLink(target)
}

func unescapePath(s string) (string, error) {
	return strings.NewReplacer("%20", " ").Replace(s), nil
}

// openURLFn opens an external URL in the browser (tests replace it so
// nothing is ever launched).
var openURLFn = func(url string) { openBrowser(url) }

// noteMeta is the hover-card subtitle: folder and updated time.
func (m Model) noteMeta(n *domain.Note) string {
	rel, _ := filepath.Rel(m.rootDir, filepath.Dir(n.Path))
	folder := "~/" + filepath.ToSlash(rel)
	if rel == "." {
		folder = "~"
	}
	t := n.UpdatedAt
	if t.IsZero() {
		if fi, err := os.Stat(n.Path); err == nil {
			t = fi.ModTime()
		}
	}
	if t.IsZero() {
		return folder
	}
	return folder + " · " + t.Format("02/01/2006 15:04")
}

var _ = time.Now
