package ui

import (
	"path/filepath"

	"github.com/charmbracelet/glamour"
	"github.com/vinizap/lumi/tui-client/config"
	"github.com/vinizap/lumi/tui-client/domain"
	"github.com/vinizap/lumi/tui-client/feed"
	"github.com/vinizap/lumi/tui-client/filesystem"
	"github.com/vinizap/lumi/tui-client/theme"
)

// ViewMode represents the current screen.
type ViewMode int

// There is no full-screen explorer: the floating explorer overlay
// (floatExp) is the only navigation and opens over either view
// (SPEC-PARTE2 §2).
const (
	ViewHome     ViewMode = iota // empty book home (no note open)
	ViewFullNote                 // the notes workspace (panes)
	ViewConfig                   // in-app settings
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
	rootDir    string
	currentDir string
	items      []Item
	cursor     int
	width      int
	height     int
	viewMode   ViewMode
	renderer   *glamour.TermRenderer

	// fullNote is the focused pane's note (link resolution).
	fullNote *domain.Note

	// Transient status message (e.g. "3 lines yanked"), cleared on next keypress
	statusMsg string

	// Modals
	showNav    bool
	showSearch bool
	showInput  bool

	// Move picker modal state (own cursor, dir, items)
	navCursor int
	navDir    string
	navItems  []Item

	// Yazi-style navigation history (explorer). histBack holds the
	// locations the user came from; histFwd holds locations they went
	// "back" out of and can return to with `L`. Any fresh navigation
	// clears histFwd — same contract as a browser. Both stacks are
	// capped at navHistoryCap entries (see history.go).
	histBack []navEntry
	histFwd  []navEntry

	// treePrefix holds a pending multi-key prefix in the explorer
	// ("g" or "z"). Empty means no prefix is pending. The next key
	// either completes a chord (gg / gh / zp) or cancels the prefix
	// and dispatches normally.
	treePrefix string

	// previewHidden toggles the preview column off in the explorer
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
	searchCursor  int
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
	showBookDetails bool
	lastMirrorGen   uint64
	// books level of the floating explorer (accounts → books).
	bk *booksLevel
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
	// floatExp shows the floating explorer (the only navigation).
	floatExp bool
	// mo drives the springs (motion.go); shared by Model copies.
	mo *motion
	// recent lists the book's latest notes on the empty home;
	// homeCursor is the selected row.
	recent     []Item
	homeCursor int
	// overlayBeforeConfig: settings were opened from the explorer,
	// which comes back when they close.
	overlayBeforeConfig bool
	// lastRemembered is the note last written to last-notes.json.
	lastRemembered string
	// hover is the K link card.
	hover *hoverCard
	// lastShape is the last DECSCUSR sent.
	lastShape string
	// accounts screen and sign-in form.
	acct *accountsScreen
	// ctxMenu is the explorer's right-click menu (float_mouse.go).
	ctxMenu *ctxMenu
}

// NewModelWithInitialNote builds the model for the book at rootDir.
// When notePath is set (`lumi <file.md>`, an absolute path) that note
// opens; otherwise the book's last opened note does, or, with none,
// the empty home with the floating explorer open.
func NewModelWithInitialNote(rootDir, notePath string) Model {
	cfg := config.Load()

	// Resolve the theme based on config
	theme.Resolve(cfg.ThemeMode, cfg.DarkTheme, cfg.LightTheme)
	ApplyTheme()

	renderer, _ := glamour.NewTermRenderer(
		glamour.WithStylePath(glamourStyle()),
		glamour.WithWordWrap(100),
	)

	m := Model{
		cfg:           cfg,
		rootDir:       rootDir,
		currentDir:    rootDir,
		navDir:        rootDir,
		items:         []Item{},
		viewMode:      ViewHome,
		renderer:      renderer,
		searchType:    cfg.SearchType,
		pane:          PaneList,
		previewHidden: !cfg.ShowPreview,
		showHidden:    cfg.ShowHidden,
		legendOn:      cfg.ShowLegend,
		paneNav:       cfg.PaneNav,
		mouseOn:       cfg.Mouse,
		prose:         newProseCache(),
		mo:            newMotion(cfg.ReduceMotion),
	}
	if notePath != "" {
		if note, err := filesystem.ReadNote(notePath); err == nil && note != nil {
			m.currentDir = filepath.Dir(notePath)
			if !pathIsInsideVault(rootDir, m.currentDir) {
				m.currentDir = rootDir
			}
			m.pendingSelect = notePath
			m.openNoteIn(note, "replace")
			return m
		}
	}
	m.openBook()
	return m
}
