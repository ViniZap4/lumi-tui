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
}

type Folder struct {
	Name     string
	Path     string
	Parent   *Folder
	Children []*Folder
}
