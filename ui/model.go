package ui

import (
	"github.com/charmbracelet/glamour"
	"github.com/vinizap/lumi/tui-client/config"
	"github.com/vinizap/lumi/tui-client/domain"
	"github.com/vinizap/lumi/tui-client/feed"
	"github.com/vinizap/lumi/tui-client/theme"
)

// ViewMode represents the current screen.
type ViewMode int

const (
	ViewHome     ViewMode = iota // animated splash
	ViewTree                     // file browser (3-column)
	ViewFullNote                 // reading a note
	ViewConfig                   // in-app settings
)

// VisualModeType represents the type of visual selection.
type VisualModeType int

const (
	VisualNone VisualModeType = iota
	VisualChar                // v - character-wise selection
	VisualLine                // V - line-wise selection
)

// ConfigItemKind represents the kind of config item.
type ConfigItemKind int

const (
	ConfigHeader ConfigItemKind = iota // section label, not selectable
	ConfigCycle                        // h/l cycles options
	ConfigAction                       // enter triggers action
	ConfigInput                        // enter opens input modal to edit value
)

// ConfigItem represents a single row in the config view.
type ConfigItem struct {
	Label   string
	Kind    ConfigItemKind
	Key     string // config field key
	Value   string
	Options []string // for Cycle kind
}

// Item represents a folder or note in listings.
type Item struct {
	Name     string
	IsFolder bool
	Path     string
	Note     *domain.Note
}

// Model is the main Bubbletea model for the TUI.
type Model struct {
	// Core state
	rootDir      string
	currentDir   string
	folderConfig *config.FolderConfig
	items        []Item
	cursor       int
	width        int
	height       int
	viewMode     ViewMode
	renderer     *glamour.TermRenderer

	// Home animation (diagonal left-to-right wipe)
	animCol  int  // how many rune columns have been revealed
	animDone bool // animation finished

	// Note view state
	fullNote      *domain.Note
	contentLines  []string
	renderedView  string
	renderedLines []string
	lineCursor    int
	colCursor     int
	desiredCol    int

	// Visual mode
	visualMode     VisualModeType
	visualStart    int
	visualEnd      int
	visualStartCol int
	visualEndCol   int

	// Transient status message (e.g. "3 lines yanked"), cleared on next keypress
	statusMsg string

	// Yank highlight flash (brief visual feedback after yanking)
	yankHighlight bool
	yankMode      VisualModeType
	yankStartLine int
	yankEndLine   int
	yankStartCol  int
	yankEndCol    int

	// Modals
	showNav    bool
	showSearch bool
	showInput  bool

	// Navigation modal state (own cursor, dir, items)
	navCursor int
	navDir    string
	navItems  []Item

	// Yazi-style navigation history (tree view). histBack holds the
	// locations the user came from; histFwd holds locations they went
	// "back" out of and can return to with `L`. Any fresh navigation
	// clears histFwd — same contract as a browser. Both stacks are
	// capped at navHistoryCap entries (see history.go).
	histBack []navEntry
	histFwd  []navEntry

	// treePrefix holds a pending multi-key prefix in the tree view
	// ("g" or "z"). Empty means no prefix is pending. The next key
	// either completes a chord (gg / gh / zp) or cancels the prefix
	// and dispatches normally.
	treePrefix string

	// previewHidden toggles the preview column off in the tree view
	// (`zp`). Session-only by design — not persisted to config.
	previewHidden bool

	// Split view
	splitMode string
	splitNote *domain.Note

	// Link insertion mode (L key opens nav to pick a note for link)
	linkInsertMode bool

	// Search state
	searchQuery   string
	searchType    string
	searchResults []Item
	inFileSearch  bool
	// searchGen is bumped on every dispatchSearch call so stale
	// results from earlier rapid keystrokes are discarded in Update.
	// Without this guard, a slow walk for query "lum" could overwrite
	// the fresh results for query "lumi" if it returned later.
	searchGen int

	// Input modal
	inputMode  string
	inputValue string

	// Confirm modal (delete confirmation)
	showConfirm         bool
	confirmMsg          string
	pendingDeleteNote   *domain.Note
	pendingDeleteFolder string

	// Pending move operation
	pendingMoveNote   *domain.Note
	pendingMoveFolder string

	// Books (SPEC-LIVROS): registry + per-account feed. Nil when the
	// model runs without a manager (tests, single-file mode).
	bookMgr         *feed.Manager
	activeBookID    string
	showBooks       bool
	bookCursor      int
	showBookDetails bool
	lastMirrorGen   uint64
	// switchTo is set when the user picked another book: the program
	// quits and main restarts the model on that folder.
	switchTo string

	// Toast notifications
	toastMsg   string
	toastLevel ToastLevel
	toastID    int

	// Config view state
	configCursor int
	configItems  []ConfigItem
	configCfg    *config.Config
	previousView ViewMode

	// --- Explorer v2 (web/Apple parity) ---

	// pane is the focused explorer column (Ctrl-h/l).
	pane Pane
	// parentCursor is the selection in the parent column when it has
	// focus; previewScroll the first visible preview line.
	parentCursor  int
	previewScroll int
	// count is a pending numeric prefix ("5" in 5j).
	count string
	// showHidden lists dot files and folders (`.`).
	showHidden bool
	// legendOn / paneNav / mouseOn mirror the settings.
	legendOn bool
	paneNav  bool
	mouseOn  bool
	// filter narrows the current folder in place (`/`). allItems is the
	// unfiltered listing; filterTyping is true while the prompt is open.
	filter       string
	filterTyping bool
	allItems     []Item
	// yanked is the note `y` copied; `p` pastes a copy.
	yanked *Item
	// Palette (`:` / space), help (`?`).
	showPalette   bool
	paletteQuery  string
	paletteCursor int
	paletteNotes  []paletteEntry
	showHelp      bool
	helpScroll    int
	// lastClick drives double-click detection.
	lastClick clickInfo
	// pendingSelect is a path to put the cursor on after the next
	// listing load (create, paste, palette jump).
	pendingSelect string
	// prose caches rendered markdown previews (shared across copies).
	prose *proseCache

	// --- Part 2: notes workspace (splits, built-in editor, views) ---

	// ws is the pane layout; nil until a note is opened. Shared by
	// Model copies (pointer).
	ws *workspace
	// cfg is the loaded settings (reloaded when settings change).
	cfg *config.Config
	// winPending: Ctrl-w was pressed, the next key is a window command.
	winPending bool
	// notePrefix is a pending g/z/leader chord in reader/board/canvas.
	notePrefix string
	// floatExp shows the floating explorer over the notes.
	floatExp bool
	// overlayFrame drives the overlay open animation (0 = settled).
	overlayFrame int
	// hover is the K link card.
	hover *hoverCard
	// lastShape is the last DECSCUSR sent.
	lastShape string
	// accounts screen and sign-in form.
	acct *accountsScreen

	// initialNotePath holds an absolute path the user passed at the
	// command line (`lumi some/note.md`). When set, the first
	// itemsLoadedMsg handler auto-opens the matching note and clears
	// the field. Empty string means "no auto-open".
	initialNotePath string
}

// NewModel creates and returns a new Model.
func NewModel(rootDir string) Model {
	cfg := config.Load()

	// Resolve the theme based on config
	theme.Resolve(cfg.ThemeMode, cfg.DarkTheme, cfg.LightTheme)
	ApplyTheme()

	folderCfg := config.LoadFolderConfig(rootDir)

	renderer, _ := glamour.NewTermRenderer(
		glamour.WithStylePath(glamourStyle()),
		glamour.WithWordWrap(100),
	)

	return Model{
		cfg:           cfg,
		rootDir:       rootDir,
		currentDir:    rootDir,
		navDir:        rootDir,
		items:         []Item{},
		viewMode:      ViewHome,
		renderer:      renderer,
		searchType:    cfg.SearchType,
		folderConfig:  folderCfg,
		pane:          PaneList,
		previewHidden: !cfg.ShowPreview,
		showHidden:    cfg.ShowHidden,
		legendOn:      cfg.ShowLegend,
		paneNav:       cfg.PaneNav,
		mouseOn:       cfg.Mouse,
		prose:         newProseCache(),
	}
}

// NewSimpleModel is an alias for backward compatibility with main.go.
func NewSimpleModel(rootDir string) Model {
	return NewModel(rootDir)
}

// NewModelWithInitialNote builds a Model that lands directly on the
// file-browser view, skipping the home splash. When notePath is set
// (an absolute file path) it also queues an auto-open of that file as
// soon as the initial item listing arrives.
//
// Use this whenever the user explicitly told lumi where to start —
// `lumi <dir>`, `lumi <file.md>`, or via $LUMI_NOTES_DIR. The splash
// only makes sense for `lumi` with no hint at all (see NewSimpleModel),
// because past that point the user wants to see their notes, not an
// animation.
func NewModelWithInitialNote(rootDir, notePath string) Model {
	m := NewModel(rootDir)
	m.viewMode = ViewTree
	m.animDone = true
	if notePath != "" {
		m.initialNotePath = notePath
	}
	return m
}
