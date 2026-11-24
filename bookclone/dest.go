package bookclone

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/vinizap/lumi/tui-client/books"
)

// DefaultBooksDir is where new book folders go unless Settings say
// otherwise.
const DefaultBooksDir = "~/lumi"

// Destination errors (pt-BR, shown inline under the prompt).
var (
	ErrDestEmpty     = errors.New("informe uma pasta")
	ErrDestRelative  = errors.New("use um caminho absoluto ou começando com ~/")
	ErrDestTraversal = errors.New("o caminho não pode conter ..")
	ErrDestNotDir    = errors.New("já existe um arquivo com esse nome")
	ErrDestNotEmpty  = errors.New("a pasta já existe e não está vazia")
	ErrDestMissing   = errors.New("a pasta não existe")
)

// InsideBookError: the destination lies inside (or is) another book.
type InsideBookError struct{ Book string }

func (e *InsideBookError) Error() string {
	return fmt.Sprintf("fica dentro do livro %q — escolha outra pasta", e.Book)
}

// expand turns the user's input into a clean absolute path: `~` expanded,
// relative paths and `..` segments refused.
func expand(input string) (string, error) {
	p := strings.TrimSpace(input)
	if p == "" {
		return "", ErrDestEmpty
	}
	for _, seg := range strings.FieldsFunc(p, func(r rune) bool { return r == '/' || r == filepath.Separator }) {
		if seg == ".." {
			return "", ErrDestTraversal
		}
	}
	p = books.ExpandHome(p)
	if !filepath.IsAbs(p) {
		return "", ErrDestRelative
	}
	return filepath.Clean(p), nil
}

// resolve follows symlinks on the deepest existing ancestor, so a
// symlinked path cannot dodge the "inside another book" check.
func resolve(p string) string {
	rest := ""
	cur := p
	for {
		if r, err := filepath.EvalSymlinks(cur); err == nil {
			if rest == "" {
				return r
			}
			return filepath.Join(r, rest)
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return p
		}
		if rest == "" {
			rest = filepath.Base(cur)
		} else {
			rest = filepath.Join(filepath.Base(cur), rest)
		}
		cur = parent
	}
}

// insideBook returns the book whose folder contains (or is) abs.
func insideBook(abs string, list []books.Book, skipID string) *books.Book {
	target := resolve(abs)
	for i := range list {
		b := list[i]
		if b.Path == "" || b.ID == skipID {
			continue
		}
		root := resolve(filepath.Clean(b.AbsPath()))
		if within(root, target) {
			return &list[i]
		}
	}
	return nil
}

// ValidateCloneDest checks a clone destination: absolute (or ~/), no
// `..`, empty or not yet existing, and not inside any registered book.
// Returns the clean absolute path.
func ValidateCloneDest(input string, list []books.Book) (string, error) {
	abs, err := expand(input)
	if err != nil {
		return "", err
	}
	if fi, err := os.Stat(abs); err == nil {
		if !fi.IsDir() {
			return "", ErrDestNotDir
		}
		entries, err := os.ReadDir(abs)
		if err != nil {
			return "", err
		}
		if len(entries) > 0 {
			return "", ErrDestNotEmpty
		}
	}
	if b := insideBook(abs, list, ""); b != nil {
		return "", &InsideBookError{Book: b.Name}
	}
	return abs, nil
}

// ValidateLinkDest checks a folder to bind a book to: it must exist, be a
// directory, and not be (or be inside, or contain) another book.
func ValidateLinkDest(input string, list []books.Book, bookID string) (string, error) {
	abs, err := expand(input)
	if err != nil {
		return "", err
	}
	fi, err := os.Stat(abs)
	if err != nil {
		return "", ErrDestMissing
	}
	if !fi.IsDir() {
		return "", ErrDestNotDir
	}
	if b := insideBook(abs, list, bookID); b != nil {
		return "", &InsideBookError{Book: b.Name}
	}
	target := resolve(abs)
	for _, b := range list {
		if b.Path == "" || b.ID == bookID {
			continue
		}
		if within(target, resolve(b.AbsPath())) {
			return "", fmt.Errorf("contém o livro %q — escolha outra pasta", b.Name)
		}
	}
	return abs, nil
}

// Slugify makes a folder name from a book name: lowercase letters,
// digits and dashes ("Meu Livro!" → "meu-livro"); "livro" when nothing
// is left.
func Slugify(name string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(name) {
		r = fold(r)
		switch {
		case r < 128 && (unicode.IsLetter(r) || unicode.IsDigit(r)):
			b.WriteRune(r)
			dash = false
		case b.Len() > 0 && !dash:
			b.WriteByte('-')
			dash = true
		}
	}
	s := strings.Trim(b.String(), "-")
	if s == "" {
		return "livro"
	}
	return s
}

// fold strips the common Latin accents (pt-BR names).
func fold(r rune) rune {
	switch r {
	case 'á', 'à', 'â', 'ã', 'ä':
		return 'a'
	case 'é', 'è', 'ê', 'ë':
		return 'e'
	case 'í', 'ì', 'î', 'ï':
		return 'i'
	case 'ó', 'ò', 'ô', 'õ', 'ö':
		return 'o'
	case 'ú', 'ù', 'û', 'ü':
		return 'u'
	case 'ç':
		return 'c'
	case 'ñ':
		return 'n'
	}
	return r
}

// DefaultDest proposes `<booksDir>/<slug>` (as typed, with `~`), adding
// -2, -3… until it would pass ValidateCloneDest. booksDir "" = ~/lumi.
func DefaultDest(booksDir, name string, list []books.Book) string {
	if strings.TrimSpace(booksDir) == "" {
		booksDir = DefaultBooksDir
	}
	base := strings.TrimRight(booksDir, "/") + "/" + Slugify(name)
	cand := base
	for i := 2; i < 100; i++ {
		if _, err := ValidateCloneDest(cand, list); err == nil {
			return cand
		}
		cand = fmt.Sprintf("%s-%d", base, i)
	}
	return base
}
