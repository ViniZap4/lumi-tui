package ui

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/vinizap/lumi/tui-client/domain"
	"github.com/vinizap/lumi/tui-client/filesystem"
)

// Explorer file operations as pure plans (yazi grammar; the same rules
// as the web client's fileops.ts and the Apple explorer):
//
//	a name        → note "name" in the current folder
//	a a/b/name    → note in a/b under the current folder (made on the fly)
//	a dir/        → a folder
//	r name        → retitle the note
//	r dir/name    → move (relative to the note's folder; leading `/` is
//	                the book root, `..` climbs, clamped at the root) + retitle
//	r dir/        → move, keep the title
//	r file.md     → set the file name explicitly
//
// Paths are book-relative, slash-separated and validated before any
// filesystem call: nothing can resolve outside the book root, and no
// segment may start with a dot (so `.lumi/` is unreachable).

const maxPathBytes = 512

type planKind int

const (
	planError planKind = iota
	planNone
	planFolder
	planNote
)

type createPlan struct {
	kind   planKind
	path   string // folder: its path
	title  string // note: its title
	folder string // note: its folder ("" = root)
	err    string
}

type renamePlan struct {
	kind  planKind
	title string // new title ("" = unchanged)
	path  string // new file path ("" = unchanged)
	err   string
}

type folderRenamePlan struct {
	kind     planKind
	from, to string
	err      string
}

// resolvePath resolves input against base (both book-relative, "" =
// root): `/x` starts at the root, `..` climbs (clamped), `.`/empty
// segments vanish.
func resolvePath(base, input string) string {
	var parts []string
	if !strings.HasPrefix(input, "/") {
		for _, p := range strings.Split(base, "/") {
			if p != "" {
				parts = append(parts, p)
			}
		}
	}
	for _, seg := range strings.Split(strings.ReplaceAll(input, `\`, "/"), "/") {
		switch seg {
		case "", ".":
		case "..":
			if len(parts) > 0 {
				parts = parts[:len(parts)-1]
			}
		default:
			parts = append(parts, seg)
		}
	}
	return strings.Join(parts, "/")
}

// pathError is the server's canonical-form check ("" = valid).
func pathError(p string) string {
	if p == "" {
		return "caminho vazio"
	}
	if len(p) > maxPathBytes {
		return "caminho longo demais"
	}
	for _, r := range p {
		if r == '\\' || r == 0x7f || unicode.IsControl(r) {
			return "caracteres de controle e barras invertidas não são permitidos"
		}
	}
	for _, seg := range strings.Split(p, "/") {
		if strings.HasPrefix(seg, ".") {
			return fmt.Sprintf("“%s”: nomes não podem começar com ponto", seg)
		}
		if strings.TrimSpace(seg) != seg {
			return fmt.Sprintf("“%s”: nomes não podem começar ou terminar com espaço", seg)
		}
	}
	return ""
}

func dirOf(p string) string {
	if i := strings.LastIndex(p, "/"); i >= 0 {
		return p[:i]
	}
	return ""
}

func baseOf(p string) string {
	if i := strings.LastIndex(p, "/"); i >= 0 {
		return p[i+1:]
	}
	return p
}

func isMD(name string) bool { return strings.HasSuffix(strings.ToLower(name), ".md") }

func stemOf(p string) string {
	b := baseOf(p)
	if isMD(b) {
		return b[:len(b)-3]
	}
	return b
}

func planCreate(dir, raw string) createPlan {
	input := strings.TrimSpace(raw)
	if input == "" || input == "/" {
		return createPlan{err: "digite um nome (termine com / para pasta)"}
	}
	isFolder := strings.HasSuffix(input, "/")
	rel := resolvePath(dir, input)
	if rel == "" {
		return createPlan{err: "isso é a raiz do livro"}
	}
	if e := pathError(rel); e != "" {
		return createPlan{err: e}
	}
	if isFolder {
		return createPlan{kind: planFolder, path: rel}
	}
	title := strings.TrimSpace(stemOf(rel))
	if title == "" {
		return createPlan{err: "a nota precisa de um nome"}
	}
	return createPlan{kind: planNote, title: title, folder: dirOf(rel)}
}

func planRename(notePath, title, raw string) renamePlan {
	input := strings.TrimSpace(raw)
	if input == "" {
		return renamePlan{err: "digite o novo nome"}
	}
	current := notePath
	dir := dirOf(current)
	if !strings.Contains(input, "/") {
		if isMD(input) {
			p := input
			if dir != "" {
				p = dir + "/" + input
			}
			if e := pathError(p); e != "" {
				return renamePlan{err: e}
			}
			if p == current {
				return renamePlan{kind: planNone}
			}
			return renamePlan{kind: planNote, path: p}
		}
		if input == title {
			return renamePlan{kind: planNone}
		}
		return renamePlan{kind: planNote, title: input}
	}
	isFolder := strings.HasSuffix(input, "/")
	rel := resolvePath(dir, input)
	folder, last := dirOf(rel), baseOf(rel)
	if isFolder {
		folder, last = rel, ""
	}
	file := baseOf(current)
	if isMD(last) {
		file = last
	}
	p := file
	if folder != "" {
		p = folder + "/" + file
	}
	if e := pathError(p); e != "" {
		return renamePlan{err: e}
	}
	plan := renamePlan{kind: planNote}
	if p != current {
		plan.path = p
	}
	if last != "" && !isMD(last) && last != title {
		plan.title = last
	}
	if plan.path == "" && plan.title == "" {
		return renamePlan{kind: planNone}
	}
	return plan
}

func planFolderRename(folderPath, raw string) folderRenamePlan {
	input := strings.TrimRight(strings.TrimSpace(raw), "/")
	if input == "" {
		return folderRenamePlan{err: "digite o novo nome da pasta"}
	}
	to := resolvePath(dirOf(folderPath), input)
	if to == "" {
		return folderRenamePlan{err: "isso é a raiz do livro"}
	}
	if e := pathError(to); e != "" {
		return folderRenamePlan{err: e}
	}
	if to == folderPath {
		return folderRenamePlan{kind: planNone}
	}
	if strings.HasPrefix(to, folderPath+"/") {
		return folderRenamePlan{err: "uma pasta não pode ir para dentro dela mesma"}
	}
	return folderRenamePlan{kind: planFolder, from: folderPath, to: to}
}

// copyTitle is a title for a pasted copy that does not collide.
func copyTitle(title string, taken map[string]bool) string {
	base := title + " copy"
	if !taken[base] {
		return base
	}
	for i := 2; i < 1000; i++ {
		t := fmt.Sprintf("%s %d", base, i)
		if !taken[t] {
			return t
		}
	}
	return base + " +"
}

// --- executors (filesystem side) ---

// relOf is p relative to the book root, slash-separated ("" = root).
func (m Model) relOf(p string) string {
	r, err := filepath.Rel(m.rootDir, p)
	if err != nil || r == "." {
		return ""
	}
	return filepath.ToSlash(r)
}

// absOf joins a validated book-relative path onto the root.
func (m Model) absOf(rel string) string {
	if rel == "" {
		return m.rootDir
	}
	return filepath.Join(m.rootDir, filepath.FromSlash(path.Clean(rel)))
}

// applyCreate runs a create plan. It returns the folder the new item
// lives in and the path to select.
func (m Model) applyCreate(p createPlan) (dir, sel string, err error) {
	switch p.kind {
	case planFolder:
		abs := m.absOf(p.path)
		if _, err := os.Stat(abs); err == nil {
			return "", "", fmt.Errorf("%s já existe", p.path)
		}
		if err := os.MkdirAll(abs, 0o755); err != nil {
			return "", "", err
		}
		return filepath.Dir(abs), abs, nil
	case planNote:
		dir := m.absOf(p.folder)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return "", "", err
		}
		n, err := filesystem.CreateNote(dir, p.title)
		if err != nil {
			return "", "", err
		}
		return dir, n.Path, nil
	}
	return "", "", fmt.Errorf("%s", p.err)
}

// applyRename runs a note rename/move plan and returns the new path.
func (m Model) applyRename(n *domain.Note, p renamePlan) (string, error) {
	if p.kind != planNote {
		return n.Path, nil
	}
	if p.path != "" {
		dest := m.absOf(p.path)
		if _, err := os.Stat(dest); err == nil {
			return "", fmt.Errorf("%s já existe", p.path)
		}
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			return "", err
		}
		if err := os.Rename(n.Path, dest); err != nil {
			return "", err
		}
		n.Path = dest
	}
	if p.title != "" {
		// Frontmatter notes keep the file and change `title:`; plain
		// markdown notes are renamed on disk (the title is the name).
		if err := filesystem.RenameNote(n, p.title); err != nil {
			return "", err
		}
	}
	return n.Path, nil
}

func (m Model) applyFolderRename(p folderRenamePlan) (string, error) {
	if p.kind != planFolder {
		return m.absOf(p.from), nil
	}
	from, to := m.absOf(p.from), m.absOf(p.to)
	if _, err := os.Stat(to); err == nil {
		return "", fmt.Errorf("%s já existe", p.to)
	}
	if err := os.MkdirAll(filepath.Dir(to), 0o755); err != nil {
		return "", err
	}
	if err := os.Rename(from, to); err != nil {
		return "", err
	}
	return to, nil
}

// pasteNote writes a copy of src into dir with a non-colliding title.
// The copy never inherits the source's id (it is a new note).
func pasteNote(src *domain.Note, dir string) (*domain.Note, error) {
	taken := map[string]bool{}
	if notes, err := filesystem.ListNotes(dir); err == nil {
		for _, n := range notes {
			taken[n.Title] = true
		}
	}
	title := copyTitle(src.Title, taken)
	n, err := filesystem.CreateNote(dir, title)
	if err != nil {
		return nil, err
	}
	n.Content = src.Content
	n.Tags = append([]string(nil), src.Tags...)
	if err := filesystem.WriteNote(n); err != nil {
		return nil, err
	}
	return n, nil
}
