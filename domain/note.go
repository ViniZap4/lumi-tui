// tui-client/domain/note.go
package domain

import "time"

type Note struct {
	ID        string    `yaml:"id"`
	Title     string    `yaml:"title"`
	CreatedAt time.Time `yaml:"created_at"`
	UpdatedAt time.Time `yaml:"updated_at"`
	Tags      []string  `yaml:"tags"`
	Path      string    `yaml:"-"`
	Content   string    `yaml:"-"`

	// HadFrontmatter records whether the on-disk file already had a YAML
	// frontmatter block at the time of the last read. Writers consult this
	// to decide whether to (re)emit frontmatter (sticky for lumi-managed
	// notes) or preserve the file as plain markdown (non-invasive for
	// notes the user authored elsewhere). yaml:"-" so the flag itself
	// never round-trips into a frontmatter value.
	HadFrontmatter bool `yaml:"-"`

	// RawFrontmatter holds the original YAML bytes when the file's
	// frontmatter couldn't be parsed structurally. Set by ReadNote on
	// parse failure; honoured by WriteNote, which emits these bytes
	// verbatim instead of re-marshalling — that way a corrupted
	// frontmatter block (custom fields, malformed YAML, comments)
	// survives a round-trip through lumi instead of being silently
	// dropped on the next save. Empty when the frontmatter parsed
	// cleanly.
	RawFrontmatter []byte `yaml:"-"`
}

type Folder struct {
	Name     string
	Path     string
	Parent   *Folder
	Children []*Folder
}
