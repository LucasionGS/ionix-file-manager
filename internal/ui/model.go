package ui

import (
	"bytes"
	"fmt"
	"image"
	"image/png"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/LucasionGS/ionix-file-manager/internal/clipboard"
	appconfig "github.com/LucasionGS/ionix-file-manager/internal/config"
	appfs "github.com/LucasionGS/ionix-file-manager/internal/fs"
	"github.com/LucasionGS/ionix-file-manager/internal/kitty"
	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

const sidebarWidth = 22
const detailsWidth = 30
const previewCellRows = 11 // rows reserved in the details panel for image preview

// ---------------------------------------------------------------------------
// Focus
// ---------------------------------------------------------------------------

type focus int

const (
	focusList focus = iota
	focusSidebar
)

// focusShortcut maps a key directly to a focus target.
// Add entries here to register new global focus shortcuts.
type focusShortcut struct {
	binding key.Binding
	target  focus
}

var focusShortcuts = []focusShortcut{
	{binding: key.NewBinding(key.WithKeys("w")), target: focusSidebar},
	{binding: key.NewBinding(key.WithKeys("e")), target: focusList},
}

// ---------------------------------------------------------------------------
// Clipboard
// ---------------------------------------------------------------------------

type clipOp int

const (
	clipNone clipOp = iota
	clipCopy
	clipCut
)

type fileClipboard struct {
	op   clipOp
	path string
	name string
}

// ---------------------------------------------------------------------------
// Context menu
// ---------------------------------------------------------------------------

type menuAction int

const (
	menuCopy menuAction = iota
	menuCut
	menuPaste
	menuCopyPath
	menuCopyImage
	menuFavoriteToggle
	menuCancel
)

type menuEntry struct {
	icon   string
	label  string
	action menuAction
}

// menuEntries defines the context menu. Add new items here to extend it.
// buildMenu returns only the context menu entries that apply to the current selection.
// Add new items here; the disabled-item pattern is replaced by simply omitting entries.
func (m *Model) buildMenu() []menuEntry {
	hasSelection := len(m.entries) > 0
	selected := func() appfs.Entry {
		if hasSelection {
			return m.entries[m.cursor]
		}
		return appfs.Entry{}
	}

	items := []menuEntry{}

	if hasSelection {
		items = append(items,
			menuEntry{icon: "󰆏", label: "Copy", action: menuCopy},
			menuEntry{icon: "󰆐", label: "Cut", action: menuCut},
		)
	}

	if m.clipboard.op != clipNone {
		items = append(items, menuEntry{icon: "󰆒", label: "Paste", action: menuPaste})
	}

	if hasSelection {
		items = append(items, menuEntry{icon: "󰅎", label: "Copy path", action: menuCopyPath})
	}

	if e := selected(); hasSelection && !e.IsDir && appfs.IsImage(e.Name) {
		items = append(items, menuEntry{icon: "󰋩", label: "Copy image", action: menuCopyImage})
	}

	if e := selected(); hasSelection && e.IsDir {
		if m.isFavorite(e.Path) {
			items = append(items, menuEntry{icon: "󰀻", label: "Remove favorite", action: menuFavoriteToggle})
		} else {
			items = append(items, menuEntry{icon: "󰀼", label: "Add to favorites", action: menuFavoriteToggle})
		}
	}

	items = append(items, menuEntry{icon: "󰜺", label: "Cancel", action: menuCancel})
	return items
}

type contextMenuModel struct {
	open   bool
	cursor int
}

// ---------------------------------------------------------------------------
// Delete confirmation modal
// ---------------------------------------------------------------------------

type deleteModal struct {
	open   bool
	target appfs.Entry // entry to be deleted
}

// ---------------------------------------------------------------------------
// Search
// ---------------------------------------------------------------------------

type searchModel struct {
	active bool
	query  string
}

// filterAndSort returns entries matching query, ranked by match quality.
// Priority: exact match → prefix match → contains match.
// Dirs before files within each tier.
func filterAndSort(entries []appfs.Entry, query string) []appfs.Entry {
	q := strings.ToLower(query)

	type scored struct {
		entry appfs.Entry
		score int // lower = better
	}

	var results []scored
	for _, e := range entries {
		name := strings.ToLower(e.Name)
		switch {
		case name == q:
			results = append(results, scored{e, 0})
		case strings.HasPrefix(name, q):
			results = append(results, scored{e, 1})
		case strings.Contains(name, q):
			results = append(results, scored{e, 2})
		}
	}

	sort.SliceStable(results, func(i, j int) bool {
		if results[i].score != results[j].score {
			return results[i].score < results[j].score
		}
		if results[i].entry.IsDir != results[j].entry.IsDir {
			return results[i].entry.IsDir
		}
		return strings.ToLower(results[i].entry.Name) < strings.ToLower(results[j].entry.Name)
	})

	out := make([]appfs.Entry, len(results))
	for i, r := range results {
		out[i] = r.entry
	}
	return out
}

// ---------------------------------------------------------------------------
// Editor
// ---------------------------------------------------------------------------

type editorClosedMsg struct{ err error }

// defaultEditor returns the user's preferred editor by checking $VISUAL,
// $EDITOR, then falling back to common editors found in PATH.
func defaultEditor() string {
	for _, env := range []string{"VISUAL", "EDITOR"} {
		if v := os.Getenv(env); v != "" {
			return v
		}
	}
	for _, name := range []string{"nano", "vim", "vi"} {
		if _, err := exec.LookPath(name); err == nil {
			return name
		}
	}
	return ""
}

// openInEditorCmd suspends the TUI and opens path in editor.
// editor may contain arguments (e.g. "vim -u NONE").
func openInEditorCmd(editor, path string) tea.Cmd {
	parts := strings.Fields(editor)
	parts = append(parts, path)
	c := exec.Command(parts[0], parts[1:]...)
	return tea.ExecProcess(c, func(err error) tea.Msg {
		return editorClosedMsg{err: err}
	})
}

// ---------------------------------------------------------------------------
// Image preview
// ---------------------------------------------------------------------------

type previewLoadedMsg struct {
	path    string
	encoded string // base64-encoded image, pre-computed in the background
	imgW    int
	imgH    int
}

// previewMaxPx is the longest edge (in pixels) we scale previews down to.
// At typical 8×16px cells a 26-col×11-row panel is ~416×352px, so 512px
// keeps quality while producing a PNG of only a few KB to write per render.
const previewMaxPx = 512

func loadPreviewCmd(path string) tea.Cmd {
	return func() tea.Msg {
		data, err := os.ReadFile(path)
		if err != nil {
			return previewLoadedMsg{path: path}
		}

		// Decode to image.Image — handles PNG, JPEG, GIF (decoders registered in main).
		img, _, err := image.Decode(bytes.NewReader(data))
		if err != nil {
			return previewLoadedMsg{path: path}
		}

		// Scale down to at most previewMaxPx on the longest edge.
		// This keeps the terminal write tiny (~KB) on every render.
		img = scaleDown(img, previewMaxPx, previewMaxPx)

		// Re-encode as PNG — kitty's f=100 is PNG-specific.
		var buf bytes.Buffer
		if err := png.Encode(&buf, img); err != nil {
			return previewLoadedMsg{path: path}
		}

		b := img.Bounds()
		return previewLoadedMsg{
			path:    path,
			encoded: kitty.Encode(buf.Bytes()),
			imgW:    b.Dx(),
			imgH:    b.Dy(),
		}
	}
}

// ---------------------------------------------------------------------------
// Image modal
// ---------------------------------------------------------------------------

type imageModalCacheEntry struct {
	encoded string
	imgW    int
	imgH    int
}

type imageModalState struct {
	open    bool
	path    string
	encoded string // base64-encoded PNG, "" while loading
	imgW    int
	imgH    int
	cache   map[string]imageModalCacheEntry
}

type imageModalLoadedMsg struct {
	path    string
	encoded string
	imgW    int
	imgH    int
}

// imageModalMaxPx is the longest edge (in pixels) we scale modal images down to.
// Larger than previewMaxPx so the full-screen modal looks crisp.
const imageModalMaxPx = 2048

func loadImageModalCmd(path string) tea.Cmd {
	return func() tea.Msg {
		data, err := os.ReadFile(path)
		if err != nil {
			return imageModalLoadedMsg{path: path}
		}
		img, _, err := image.Decode(bytes.NewReader(data))
		if err != nil {
			return imageModalLoadedMsg{path: path}
		}
		img = scaleDown(img, imageModalMaxPx, imageModalMaxPx)
		var buf bytes.Buffer
		if err := png.Encode(&buf, img); err != nil {
			return imageModalLoadedMsg{path: path}
		}
		b := img.Bounds()
		return imageModalLoadedMsg{
			path:    path,
			encoded: kitty.Encode(buf.Bytes()),
			imgW:    b.Dx(),
			imgH:    b.Dy(),
		}
	}
}

// openImageModal initialises the modal for path, re-using any existing cache,
// and kicks off a load + neighbour preload.
func (m *Model) openImageModal(path string) tea.Cmd {
	cache := m.imageModal.cache
	if cache == nil {
		cache = make(map[string]imageModalCacheEntry)
	}
	if ce, ok := cache[path]; ok {
		m.imageModal = imageModalState{
			open:    true,
			path:    path,
			encoded: ce.encoded,
			imgW:    ce.imgW,
			imgH:    ce.imgH,
			cache:   cache,
		}
		// Still preload neighbours.
		visible := m.visibleEntries()
		var imgIndices []int
		for i, e := range visible {
			if !e.IsDir && appfs.IsImage(e.Name) {
				imgIndices = append(imgIndices, i)
			}
		}
		for ci, idx := range imgIndices {
			if visible[idx].Path == path {
				return m.preloadNeighbours(imgIndices, ci, visible)
			}
		}
		return nil
	}
	m.imageModal = imageModalState{open: true, path: path, cache: cache}
	return loadImageModalCmd(path)
}

// imageModalStep moves the modal to the next (+1) or previous (-1) image in
// the visible entry list. Returns a load command, or nil if no neighbour exists.
func (m *Model) imageModalStep(delta int) tea.Cmd {
	visible := m.visibleEntries()
	// Collect image-only indices in visible order.
	var imgIndices []int
	for i, e := range visible {
		if !e.IsDir && appfs.IsImage(e.Name) {
			imgIndices = append(imgIndices, i)
		}
	}
	if len(imgIndices) == 0 {
		return nil
	}
	// Find the current image.
	cur := -1
	for i, idx := range imgIndices {
		if visible[idx].Path == m.imageModal.path {
			cur = i
			break
		}
	}
	if cur == -1 {
		return nil
	}
	next := cur + delta
	if next < 0 || next >= len(imgIndices) {
		return nil
	}
	entry := visible[imgIndices[next]]
	cache := m.imageModal.cache
	// Reuse cached data immediately if available.
	if ce, ok := cache[entry.Path]; ok {
		m.imageModal = imageModalState{
			open:    true,
			path:    entry.Path,
			encoded: ce.encoded,
			imgW:    ce.imgW,
			imgH:    ce.imgH,
			cache:   cache,
		}
		// Pre-load neighbours in the background.
		return m.preloadNeighbours(imgIndices, next, visible)
	}
	m.imageModal = imageModalState{open: true, path: entry.Path, cache: cache}
	return tea.Batch(loadImageModalCmd(entry.Path), m.preloadNeighbours(imgIndices, next, visible))
}

// preloadNeighbours issues background load commands for the immediate neighbours
// of curIdx in imgIndices that are not yet cached.
func (m *Model) preloadNeighbours(imgIndices []int, curIdx int, visible []appfs.Entry) tea.Cmd {
	var cmds []tea.Cmd
	for _, delta := range []int{-1, 1} {
		ni := curIdx + delta
		if ni < 0 || ni >= len(imgIndices) {
			continue
		}
		p := visible[imgIndices[ni]].Path
		if _, cached := m.imageModal.cache[p]; !cached {
			cmds = append(cmds, loadImageModalCmd(p))
		}
	}
	if len(cmds) == 0 {
		return nil
	}
	return tea.Batch(cmds...)
}

// scaleDown resizes src so neither dimension exceeds maxW×maxH, preserving
// aspect ratio. Returns src unchanged if it already fits.
func scaleDown(src image.Image, maxW, maxH int) image.Image {
	b := src.Bounds()
	srcW, srcH := b.Dx(), b.Dy()
	if srcW <= maxW && srcH <= maxH {
		return src
	}
	scale := math.Min(float64(maxW)/float64(srcW), float64(maxH)/float64(srcH))
	dstW := int(math.Round(float64(srcW) * scale))
	dstH := int(math.Round(float64(srcH) * scale))

	dst := image.NewRGBA(image.Rect(0, 0, dstW, dstH))
	for y := range dstH {
		for x := range dstW {
			dst.Set(x, y, src.At(b.Min.X+int(float64(x)/scale), b.Min.Y+int(float64(y)/scale)))
		}
	}
	return dst
}

// ---------------------------------------------------------------------------
// Sidebar bookmarks
// ---------------------------------------------------------------------------

type bookmark struct {
	label string
	icon  string
	path  string
}

// ---------------------------------------------------------------------------
// Key bindings
// ---------------------------------------------------------------------------

var keyMap = struct {
	Up, Down, Left, Right key.Binding
	GoHome                key.Binding
	ToggleHidden          key.Binding
	SwitchPane            key.Binding
	OpenMenu              key.Binding
	Copy                  key.Binding
	Paste                 key.Binding
	Search                key.Binding
	ToggleDetails         key.Binding
	Delete                key.Binding
	Quit                  key.Binding
	ToggleFavorite        key.Binding
}{
	Up:             key.NewBinding(key.WithKeys("up", "k")),
	Down:           key.NewBinding(key.WithKeys("down", "j")),
	Left:           key.NewBinding(key.WithKeys("left", "h", "backspace")),
	Right:          key.NewBinding(key.WithKeys("right", "l", "enter")),
	GoHome:         key.NewBinding(key.WithKeys("~")),
	ToggleHidden:   key.NewBinding(key.WithKeys("H")),
	SwitchPane:     key.NewBinding(key.WithKeys("tab")),
	OpenMenu:       key.NewBinding(key.WithKeys(".")),
	Copy:           key.NewBinding(key.WithKeys("ctrl+c")),
	Paste:          key.NewBinding(key.WithKeys("ctrl+v")),
	Search:         key.NewBinding(key.WithKeys("f")),
	ToggleDetails:  key.NewBinding(key.WithKeys("d")),
	Delete:         key.NewBinding(key.WithKeys("delete", "D")),
	Quit:           key.NewBinding(key.WithKeys("q")),
	ToggleFavorite: key.NewBinding(key.WithKeys("b")),
}

// ---------------------------------------------------------------------------
// Model
// ---------------------------------------------------------------------------

type Model struct {
	cwd          string
	entries      []appfs.Entry
	cursor       int
	offset       int
	width        int
	height       int
	showHidden   bool
	kittySupport bool
	err          error
	statusMsg    string

	focus         focus
	sidebarCursor int
	bookmarks     []bookmark
	favorites     []string // persisted favorite folder paths

	clipboard      fileClipboard
	contextMenu    contextMenuModel
	deleteConfirm  deleteModal
	imageModal     imageModalState
	search         searchModel
	showDetails    bool
	previewPath    string
	previewEncoded string // base64-encoded, ready for kitty.Place
	previewW       int
	previewH       int
	previewCache   map[string]imageModalCacheEntry // reuses imageModalCacheEntry: encoded+dims
	pendingSelect  string                          // filename to select on first WindowSizeMsg
}

func buildBookmarks() []bookmark {
	home, _ := os.UserHomeDir()

	candidates := []bookmark{
		{label: "Home", icon: "󰋜", path: home},
		{label: "Desktop", icon: "󰧨", path: filepath.Join(home, "Desktop")},
		{label: "Documents", icon: "󰈙", path: filepath.Join(home, "Documents")},
		{label: "Downloads", icon: "󰉍", path: filepath.Join(home, "Downloads")},
		{label: "Pictures", icon: "󰉏", path: filepath.Join(home, "Pictures")},
		{label: "Music", icon: "󰎄", path: filepath.Join(home, "Music")},
		{label: "Videos", icon: "󰨜", path: filepath.Join(home, "Videos")},
		{label: "Root", icon: "󱂵", path: "/"},
	}

	result := []bookmark{}
	for _, b := range candidates {
		if b.path == "" {
			continue
		}
		info, err := os.Stat(b.path)
		if err == nil && info.IsDir() {
			result = append(result, b)
		}
	}
	return result
}

// isFavorite reports whether path is in the favorites list.
func (m Model) isFavorite(path string) bool {
	for _, f := range m.favorites {
		if f == path {
			return true
		}
	}
	return false
}

// toggleFavorite adds or removes path from favorites and persists the change.
func (m *Model) toggleFavorite(path string) {
	if m.isFavorite(path) {
		newFavs := m.favorites[:0]
		for _, f := range m.favorites {
			if f != path {
				newFavs = append(newFavs, f)
			}
		}
		m.favorites = newFavs
	} else {
		m.favorites = append(m.favorites, path)
	}
	cfg, _ := appconfig.Load()
	cfg.Favorites = m.favorites
	_ = appconfig.Save(cfg)
}

func New(startDir, selectName string) Model {
	cfg, _ := appconfig.Load()
	ApplyColors(cfg.Colors)
	m := Model{
		cwd:          filepath.Clean(startDir),
		showHidden:   cfg.ShowHidden,
		showDetails:  cfg.ShowDetails,
		kittySupport: kitty.IsSupported(),
		focus:        focusList,
		bookmarks:    buildBookmarks(),
		favorites:    cfg.Favorites,
	}
	m.entries, m.err = m.loadEntries()
	if selectName != "" {
		for i, e := range m.entries {
			if e.Name == selectName {
				m.cursor = i
				m.pendingSelect = selectName
				break
			}
		}
	}
	return m
}

func (m Model) loadEntries() ([]appfs.Entry, error) {
	all, err := appfs.List(m.cwd)
	if err != nil {
		return nil, err
	}
	if m.showHidden {
		return all, nil
	}
	filtered := all[:0]
	for _, e := range all {
		if !appfs.IsHidden(e.Name) {
			filtered = append(filtered, e)
		}
	}
	return filtered, nil
}

// fileListWidth returns the column width allocated to the file list pane.
func (m Model) fileListWidth() int {
	w := m.width - sidebarWidth
	if m.showDetails {
		w -= detailsWidth
	}
	return w
}

// maybeLoadPreview returns a Cmd to load the selected image when the details
// panel is open and the selection has changed. Returns nil when not needed.
// As a side-effect it clears stale preview state when the selection is no longer an image.
func (m *Model) maybeLoadPreview() tea.Cmd {
	if !m.showDetails || !kitty.IsSupported() {
		return nil
	}
	visible := m.visibleEntries()
	if len(visible) == 0 || m.cursor >= len(visible) {
		return nil
	}
	e := visible[m.cursor]
	if e.IsDir || !appfs.IsImage(e.Name) {
		// Not an image — clear any leftover preview.
		m.previewPath = ""
		m.previewEncoded = ""
		return nil
	}
	if e.Path == m.previewPath {
		return nil // already displayed
	}
	// Serve from cache instantly.
	if ce, ok := m.previewCache[e.Path]; ok {
		return func() tea.Msg {
			return previewLoadedMsg{
				path:    e.Path,
				encoded: ce.encoded,
				imgW:    ce.imgW,
				imgH:    ce.imgH,
			}
		}
	}
	return loadPreviewCmd(e.Path)
}

// visibleEntries returns the entries to display: filtered+sorted when searching,
// or all loaded entries otherwise.
func (m Model) visibleEntries() []appfs.Entry {
	if m.search.active && m.search.query != "" {
		return filterAndSort(m.entries, m.search.query)
	}
	return m.entries
}

// ---------------------------------------------------------------------------
// Bubbletea interface
// ---------------------------------------------------------------------------

func (m Model) Init() tea.Cmd { return nil }

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		if m.pendingSelect != "" {
			listH := m.listHeight()
			if m.cursor >= listH {
				m.offset = m.cursor - listH/2
			}
			m.pendingSelect = ""
		}
		return m, m.maybeLoadPreview()

	case previewLoadedMsg:
		if msg.encoded != "" {
			if m.previewCache == nil {
				m.previewCache = make(map[string]imageModalCacheEntry)
			}
			m.previewCache[msg.path] = imageModalCacheEntry{
				encoded: msg.encoded,
				imgW:    msg.imgW,
				imgH:    msg.imgH,
			}
			m.previewPath = msg.path
			m.previewEncoded = msg.encoded
			m.previewW = msg.imgW
			m.previewH = msg.imgH
		}
		return m, nil

	case imageModalLoadedMsg:
		if m.imageModal.open && msg.encoded != "" {
			if m.imageModal.cache == nil {
				m.imageModal.cache = make(map[string]imageModalCacheEntry)
			}
			m.imageModal.cache[msg.path] = imageModalCacheEntry{
				encoded: msg.encoded,
				imgW:    msg.imgW,
				imgH:    msg.imgH,
			}
			if msg.path == m.imageModal.path {
				m.imageModal.encoded = msg.encoded
				m.imageModal.imgW = msg.imgW
				m.imageModal.imgH = msg.imgH
			}
		}
		return m, nil

	case editorClosedMsg:
		if msg.err != nil {
			m.statusMsg = fmt.Sprintf("editor error: %v", msg.err)
		}
		m.entries, _ = m.loadEntries()
		return m, m.maybeLoadPreview()

	case tea.KeyMsg:
		// Image modal captures all input while open.
		if m.imageModal.open {
			switch {
			case msg.String() == "q" || msg.String() == "Q" || msg.Type == tea.KeyEsc:
				m.imageModal = imageModalState{}
				return m, nil
			case key.Matches(msg, keyMap.Left):
				if cmd := m.imageModalStep(-1); cmd != nil {
					return m, cmd
				}
				return m, nil
			case key.Matches(msg, keyMap.Right):
				if cmd := m.imageModalStep(1); cmd != nil {
					return m, cmd
				}
				return m, nil
			}
			return m, nil
		}

		// Context menu captures all input while open.
		if m.contextMenu.open {
			m = m.updateContextMenu(msg)
			return m, nil
		}

		// Delete confirmation modal captures all input while open.
		if m.deleteConfirm.open {
			m = m.updateDeleteModal(msg)
			return m, m.maybeLoadPreview()
		}

		// Search captures most input while active.
		if m.search.active {
			var searchCmd tea.Cmd
			m, searchCmd = m.updateSearch(msg)
			if searchCmd != nil {
				return m, searchCmd
			}
			return m, m.maybeLoadPreview()
		}

		switch {
		case key.Matches(msg, keyMap.Quit):
			return m, tea.Quit

		case key.Matches(msg, keyMap.Copy):
			if len(m.entries) > 0 {
				e := m.entries[m.cursor]
				m.clipboard = fileClipboard{op: clipCopy, path: e.Path, name: e.Name}
				m.statusMsg = fmt.Sprintf("copied  %s", e.Name)
			}

		case key.Matches(msg, keyMap.Paste):
			if m.clipboard.op != clipNone {
				dst := filepath.Join(m.cwd, m.clipboard.name)
				var err error
				if m.clipboard.op == clipCopy {
					err = appfs.CopyEntry(m.clipboard.path, dst)
				} else {
					err = appfs.MoveEntry(m.clipboard.path, dst)
					if err == nil {
						m.clipboard = fileClipboard{}
					}
				}
				if err != nil {
					m.statusMsg = fmt.Sprintf("error: %v", err)
				} else {
					m.statusMsg = fmt.Sprintf("pasted  %s", m.clipboard.name)
					m.entries, _ = m.loadEntries()
				}
			}

		case key.Matches(msg, keyMap.ToggleDetails):
			m.showDetails = !m.showDetails
			if !m.showDetails {
				m.previewPath = ""
				m.previewEncoded = ""
			}
			_ = appconfig.Save(appconfig.Config{
				ShowDetails: m.showDetails,
				ShowHidden:  m.showHidden,
			})

		case key.Matches(msg, keyMap.Search):
			m.search.active = true
			m.search.query = ""
			m.cursor = 0
			m.offset = 0
			m.statusMsg = ""

		case key.Matches(msg, keyMap.Delete):
			visible := m.visibleEntries()
			if m.focus == focusList && len(visible) > 0 {
				m.deleteConfirm = deleteModal{open: true, target: visible[m.cursor]}
				m.statusMsg = ""
			}

		case key.Matches(msg, keyMap.OpenMenu):
			m.contextMenu.open = true
			m.contextMenu.cursor = 0
			m.statusMsg = ""

		case key.Matches(msg, keyMap.SwitchPane):
			if m.focus == focusList {
				m.focus = focusSidebar
			} else {
				m.focus = focusList
			}

		default:
			// Global focus shortcuts checked before per-pane dispatch.
			handled := false
			for _, sc := range focusShortcuts {
				if key.Matches(msg, sc.binding) {
					m.focus = sc.target
					handled = true
					break
				}
			}
			if !handled {
				if m.focus == focusSidebar {
					m = m.updateSidebar(msg)
				} else {
					var listCmd tea.Cmd
					m, listCmd = m.updateList(msg)
					if listCmd != nil {
						return m, listCmd
					}
				}
			}
		}
	}

	return m, m.maybeLoadPreview()
}

func (m Model) updateContextMenu(msg tea.KeyMsg) Model {
	switch {
	case key.Matches(msg, keyMap.Up):
		if m.contextMenu.cursor > 0 {
			m.contextMenu.cursor--
		}

	case key.Matches(msg, keyMap.Down):
		if m.contextMenu.cursor < len(m.buildMenu())-1 {
			m.contextMenu.cursor++
		}

	case key.Matches(msg, keyMap.Right):
		items := m.buildMenu()
		m = m.execMenuAction(items[m.contextMenu.cursor])

	case key.Matches(msg, keyMap.Quit),
		key.Matches(msg, keyMap.Left),
		key.Matches(msg, keyMap.OpenMenu):
		m.contextMenu.open = false
	}
	return m
}

func (m Model) execMenuAction(item menuEntry) Model {
	m.contextMenu.open = false

	switch item.action {
	case menuCopy:
		e := m.entries[m.cursor]
		m.clipboard = fileClipboard{op: clipCopy, path: e.Path, name: e.Name}
		m.statusMsg = fmt.Sprintf("copied  %s", e.Name)

	case menuCut:
		e := m.entries[m.cursor]
		m.clipboard = fileClipboard{op: clipCut, path: e.Path, name: e.Name}
		m.statusMsg = fmt.Sprintf("cut  %s", e.Name)

	case menuPaste:
		dst := filepath.Join(m.cwd, m.clipboard.name)
		var err error
		if m.clipboard.op == clipCopy {
			err = appfs.CopyEntry(m.clipboard.path, dst)
		} else {
			err = appfs.MoveEntry(m.clipboard.path, dst)
			if err == nil {
				m.clipboard = fileClipboard{}
			}
		}
		if err != nil {
			m.statusMsg = fmt.Sprintf("error: %v", err)
		} else {
			m.statusMsg = fmt.Sprintf("pasted  %s", filepath.Base(dst))
			m.entries, _ = m.loadEntries()
		}

	case menuCopyPath:
		e := m.entries[m.cursor]
		if err := clipboard.Write(e.Path); err != nil {
			m.statusMsg = fmt.Sprintf("clipboard error: %v", err)
		} else {
			m.statusMsg = fmt.Sprintf("path copied  %s", e.Path)
		}

	case menuCopyImage:
		e := m.entries[m.cursor]
		mime, _ := appfs.ImageMIME(e.Name)
		if err := clipboard.WriteImage(e.Path, mime); err != nil {
			m.statusMsg = fmt.Sprintf("clipboard error: %v", err)
		} else {
			m.statusMsg = fmt.Sprintf("image copied  %s", e.Name)
		}

	case menuFavoriteToggle:
		e := m.entries[m.cursor]
		if e.IsDir {
			wasFav := m.isFavorite(e.Path)
			m.toggleFavorite(e.Path)
			if wasFav {
				m.statusMsg = fmt.Sprintf("removed from favorites  %s", e.Name)
			} else {
				m.statusMsg = fmt.Sprintf("added to favorites  %s", e.Name)
			}
		}

	case menuCancel:
		// nothing
	}

	return m
}

func (m Model) updateSidebar(msg tea.KeyMsg) Model {
	total := len(m.bookmarks) + len(m.favorites)
	switch {
	case key.Matches(msg, keyMap.Up):
		if m.sidebarCursor > 0 {
			m.sidebarCursor--
		}
	case key.Matches(msg, keyMap.Down):
		if m.sidebarCursor < total-1 {
			m.sidebarCursor++
		}
	case key.Matches(msg, keyMap.Right):
		if total > 0 {
			var targetPath string
			if m.sidebarCursor < len(m.bookmarks) {
				targetPath = m.bookmarks[m.sidebarCursor].path
			} else {
				targetPath = m.favorites[m.sidebarCursor-len(m.bookmarks)]
			}
			m.cwd = targetPath
			m.cursor = 0
			m.offset = 0
			m.entries, m.err = m.loadEntries()
			m.focus = focusList
		}
	}
	return m
}

func (m Model) updateDeleteModal(msg tea.KeyMsg) Model {
	switch msg.String() {
	case "y", "Y":
		target := m.deleteConfirm.target
		m.deleteConfirm = deleteModal{}
		if err := appfs.DeleteEntry(target.Path); err != nil {
			m.statusMsg = fmt.Sprintf("delete error: %v", err)
		} else {
			m.statusMsg = fmt.Sprintf("deleted  %s", target.Name)
			m.entries, _ = m.loadEntries()
			if m.cursor >= len(m.entries) && m.cursor > 0 {
				m.cursor = len(m.entries) - 1
			}
			m.previewPath = ""
			m.previewEncoded = ""
		}
	default:
		// Any other key (n, esc, q, etc.) cancels.
		m.deleteConfirm = deleteModal{}
	}
	return m
}

func (m Model) updateSearch(msg tea.KeyMsg) (Model, tea.Cmd) {
	listH := m.listHeight()
	visible := m.visibleEntries()

	switch msg.Type {
	case tea.KeyEsc:
		// Close search and land the cursor on the selected item in the full list.
		if len(visible) > 0 && m.cursor < len(visible) {
			selectedPath := visible[m.cursor].Path
			m.search.active = false
			m.search.query = ""
			for i, e := range m.entries {
				if e.Path == selectedPath {
					m.cursor = i
					if m.cursor >= m.offset+listH {
						m.offset = m.cursor - listH/2
					}
					break
				}
			}
		} else {
			m.search.active = false
			m.search.query = ""
		}

	case tea.KeyEnter:
		// Execute the action on the selected entry immediately.
		if len(visible) == 0 || m.cursor >= len(visible) {
			m.search.active = false
			m.search.query = ""
			break
		}
		entry := visible[m.cursor]
		m.search.active = false
		m.search.query = ""

		for i, e := range m.entries {
			if e.Path == entry.Path {
				m.cursor = i
				if m.cursor >= m.offset+listH {
					m.offset = m.cursor - listH/2
				}
				break
			}
		}

		if entry.IsDir {
			m.cwd = entry.Path
			m.cursor = 0
			m.offset = 0
			m.entries, m.err = m.loadEntries()
			if m.err != nil {
				m.statusMsg = fmt.Sprintf("error: %v", m.err)
				m.err = nil
				m.cwd = filepath.Dir(m.cwd)
				m.entries, _ = m.loadEntries()
			}
		} else if appfs.IsImage(entry.Name) {
			if !kitty.IsSupported() {
				m.statusMsg = "image preview requires kitty terminal"
			} else {
				return m, m.openImageModal(entry.Path)
			}
		} else if appfs.IsText(entry.Path) {
			editor := defaultEditor()
			if editor != "" {
				return m, openInEditorCmd(editor, entry.Path)
			}
			m.statusMsg = "no editor found (set $VISUAL or $EDITOR)"
		}

	case tea.KeyBackspace:
		if len(m.search.query) > 0 {
			runes := []rune(m.search.query)
			m.search.query = string(runes[:len(runes)-1])
			m.cursor = 0
			m.offset = 0
		}

	case tea.KeyUp:
		if m.cursor > 0 {
			m.cursor--
			if m.cursor < m.offset {
				m.offset = m.cursor
			}
		}

	case tea.KeyDown:
		if m.cursor < len(visible)-1 {
			m.cursor++
			if m.cursor >= m.offset+listH {
				m.offset = m.cursor - listH + 1
			}
		}

	case tea.KeyRunes:
		m.search.query += string(msg.Runes)
		m.cursor = 0
		m.offset = 0

	case tea.KeySpace:
		m.search.query += " "
		m.cursor = 0
		m.offset = 0
	}

	return m, nil
}

func (m Model) updateList(msg tea.KeyMsg) (Model, tea.Cmd) {
	listH := m.listHeight()
	visible := m.visibleEntries()

	switch {
	case key.Matches(msg, keyMap.Up):
		if m.cursor > 0 {
			m.cursor--
			if m.cursor < m.offset {
				m.offset = m.cursor
			}
		}

	case key.Matches(msg, keyMap.Down):
		if m.cursor < len(visible)-1 {
			m.cursor++
			if m.cursor >= m.offset+listH {
				m.offset = m.cursor - listH + 1
			}
		}

	case key.Matches(msg, keyMap.Left):
		parent := filepath.Dir(m.cwd)
		if parent != m.cwd {
			prevDir := m.cwd
			m.cwd = parent
			m.cursor = 0
			m.offset = 0
			m.search = searchModel{}
			m.previewPath = ""
			m.previewEncoded = ""
			m.entries, m.err = m.loadEntries()
			// Re-select the folder we just came out of.
			prevName := filepath.Base(prevDir)
			listH := m.listHeight()
			for i, e := range m.entries {
				if e.Name == prevName {
					m.cursor = i
					if m.cursor >= listH {
						m.offset = m.cursor - listH/2
					}
					break
				}
			}
		}

	case key.Matches(msg, keyMap.Right):
		if len(visible) == 0 {
			break
		}
		entry := visible[m.cursor]
		if entry.IsDir {
			m.cwd = entry.Path
			m.cursor = 0
			m.offset = 0
			m.entries, m.err = m.loadEntries()
			if m.err != nil {
				m.statusMsg = fmt.Sprintf("error: %v", m.err)
				m.err = nil
				m.cwd = filepath.Dir(m.cwd)
				m.entries, _ = m.loadEntries()
			}
		} else if appfs.IsImage(entry.Name) {
			if !kitty.IsSupported() {
				m.statusMsg = "image preview requires kitty terminal"
			} else {
				return m, m.openImageModal(entry.Path)
			}
		} else if appfs.IsText(entry.Path) {
			editor := defaultEditor()
			if editor != "" {
				return m, openInEditorCmd(editor, entry.Path)
			}
			m.statusMsg = "no editor found (set $VISUAL or $EDITOR)"
		}

	case key.Matches(msg, keyMap.GoHome):
		home, err := os.UserHomeDir()
		if err == nil {
			m.cwd = home
			m.cursor = 0
			m.offset = 0
			m.entries, m.err = m.loadEntries()
		}

	case key.Matches(msg, keyMap.ToggleHidden):
		m.showHidden = !m.showHidden
		m.cursor = 0
		m.offset = 0
		m.entries, m.err = m.loadEntries()
		_ = appconfig.Save(appconfig.Config{
			ShowDetails: m.showDetails,
			ShowHidden:  m.showHidden,
		})

	case key.Matches(msg, keyMap.ToggleFavorite):
		if len(visible) > 0 {
			e := visible[m.cursor]
			if e.IsDir {
				wasFav := m.isFavorite(e.Path)
				m.toggleFavorite(e.Path)
				if wasFav {
					m.statusMsg = fmt.Sprintf("removed from favorites  %s", e.Name)
				} else {
					m.statusMsg = fmt.Sprintf("added to favorites  %s", e.Name)
				}
			}
		}
	}
	return m, nil
}

// ---------------------------------------------------------------------------
// View
// ---------------------------------------------------------------------------

func (m Model) listHeight() int {
	// title(1) + newline(1) + top-border(1) + bottom-border(1) + status(1) = 5
	h := m.height - 5
	if m.search.active {
		h-- // search bar
	}
	return h
}

func (m Model) View() string {
	if m.width == 0 {
		return ""
	}

	if m.contextMenu.open {
		return m.renderWithOverlay()
	}

	if m.deleteConfirm.open {
		return m.renderDeleteModal()
	}

	if m.imageModal.open {
		return m.renderImageModal()
	}

	view := m.renderNormal()

	if kitty.IsSupported() {
		// Always clear stale images; redraw if we have preview data.
		view += kitty.ClearAll()
		if m.showDetails && m.previewEncoded != "" {
			view += m.renderKittyPreview()
		}
	}

	return view
}

func (m Model) renderImageModal() string {
	// Modal occupies ~85% of the terminal.
	modalW := m.width * 17 / 20
	modalH := m.height * 17 / 20
	if modalW < 20 {
		modalW = 20
	}
	if modalH < 8 {
		modalH = 8
	}

	// StylePaneActive adds border(1 each side) + padding(1 each side left/right).
	// innerW/innerH are the content dimensions passed to lipgloss.
	innerW := modalW - 4 // 2 borders + 2 padding (left/right)
	innerH := modalH - 2 // 2 borders (top/bottom), no vertical padding

	imgAreaCols := innerW
	imgAreaRows := innerH - 1 // reserve the last row for the hint

	// Top row: image path.
	pathLine := StyleDim.Render(m.imageModal.path)

	imgAreaRows-- // one row used by path at top

	var lines []string
	lines = append(lines, pathLine)
	if m.imageModal.encoded == "" {
		lines = append(lines, StyleDim.Render("  loading…"))
		for len(lines) < imgAreaRows+1 {
			lines = append(lines, "")
		}
	} else {
		for len(lines) < imgAreaRows+1 {
			lines = append(lines, "")
		}
	}
	lines = append(lines, StyleDim.Render("  ←/→ cycle    q  close"))

	box := StylePaneActive.Width(innerW).Height(innerH).Render(strings.Join(lines, "\n"))
	out := lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, box)

	// Kitty image is placed on top of the box using absolute terminal coordinates.
	// col/row are 1-based. Offset 3 = border(1)+padding(1)+1-based(1) for col;
	// Offset 2 = border(1)+1-based(1) for row.
	out += kitty.ClearAll()
	if m.imageModal.encoded != "" {
		c, r := calcPreviewSize(m.imageModal.imgW, m.imageModal.imgH, imgAreaCols, imgAreaRows)
		// Center the image within the modal's content area.
		// +1 extra row offset to skip the path line at the top.
		col := (m.width-modalW)/2 + 3 + (imgAreaCols-c)/2
		row := (m.height-modalH)/2 + 2 + 1 + (imgAreaRows-r)/2
		out += kitty.Place(m.imageModal.encoded, col, row, c, r, 2)
	}
	return out
}

func (m Model) renderNormal() string {
	listH := m.listHeight()
	listW := m.fileListWidth()

	hn, _ := os.Hostname()

	titleLine := StyleTitle.Render(" "+hn+" ") +
		StyleDim.Render(" › ") +
		StyleNormal.Render(m.cwd)

	cols := []string{m.renderSidebar(listH), m.renderFileList(listH, listW)}
	if m.showDetails {
		cols = append(cols, m.renderDetails(listH))
	}
	body := lipgloss.JoinHorizontal(lipgloss.Top, cols...)

	statusLine := StyleStatusBar.Width(m.width).Render(m.buildStatus())

	if m.search.active {
		return titleLine + "\n" + body + "\n" + m.renderSearchBar() + "\n" + statusLine
	}
	return titleLine + "\n" + body + "\n" + statusLine
}

func (m Model) renderDeleteModal() string {
	clear := ""
	if kitty.IsSupported() {
		clear = kitty.ClearAll()
	}
	target := m.deleteConfirm.target
	kind := "file"
	if target.IsDir {
		kind = "directory"
	}

	const modalW = 46
	nameStyle := StyleSelected.Bold(true)
	name := nameStyle.Render(target.Name)
	if target.IsDir {
		name = StyleDir.Bold(true).Render(target.Name + "/")
	}

	warning := StyleDim.Render(fmt.Sprintf("Delete this %s permanently?", kind))
	nameLine := name
	confirm := StyleNormal.Render("  ") + StyleCursor.Render(" y ") +
		StyleNormal.Render(" confirm   ") +
		StyleDim.Render("any other key") + StyleNormal.Render(" cancel")

	content := strings.Join([]string{"", warning, nameLine, "", confirm, ""}, "\n")
	box := StylePaneActive.Width(modalW).Render(content)
	return clear + lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, box)
}

func (m Model) renderWithOverlay() string {
	clear := ""
	if kitty.IsSupported() {
		clear = kitty.ClearAll()
	}
	const menuW = 26

	var rows []string
	rows = append(rows, "")

	for i, item := range m.buildMenu() {
		label := fmt.Sprintf("  %s  %s", item.icon, item.label)
		var style lipgloss.Style
		if i == m.contextMenu.cursor {
			style = StyleCursor.Width(menuW - 4)
		} else {
			style = StyleNormal.Width(menuW - 4)
		}
		rows = append(rows, style.Render(label))
	}

	rows = append(rows, "")

	menuBox := StylePaneActive.Width(menuW).Render(strings.Join(rows, "\n"))

	// Hint line shown below the menu box.
	hint := StyleDim.Render("  j/k navigate  enter select  esc close")
	content := lipgloss.JoinVertical(lipgloss.Center, menuBox, hint)

	return clear + lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, content)
}

func (m Model) renderSidebar(height int) string {
	innerW := sidebarWidth - 4

	var rows []string

	// --- Bookmarks ---
	rows = append(rows, StyleSidebarLabel.Render("BOOKMARKS"))
	for i, b := range m.bookmarks {
		label := fmt.Sprintf("%s %s", b.icon, b.label)
		var style lipgloss.Style
		if i == m.sidebarCursor {
			if m.focus == focusSidebar {
				style = StyleSidebarCursor
			} else {
				style = StyleSidebarCursorInactive
			}
		} else {
			style = StyleSidebarItem
		}
		rows = append(rows, style.Width(innerW).Render(label))
	}

	// --- Favorites ---
	if len(m.favorites) > 0 {
		rows = append(rows, StyleSidebarLabel.Render("FAVORITES"))
		for i, fav := range m.favorites {
			sidebarIdx := len(m.bookmarks) + i
			label := fmt.Sprintf("󰀼 %s", filepath.Base(fav))
			var style lipgloss.Style
			if sidebarIdx == m.sidebarCursor {
				if m.focus == focusSidebar {
					style = StyleSidebarCursor
				} else {
					style = StyleSidebarCursorInactive
				}
			} else {
				style = StyleSidebarItem
			}
			rows = append(rows, style.Width(innerW).Render(label))
		}
	}

	for len(rows) < height {
		rows = append(rows, "")
	}

	paneStyle := StylePane
	if m.focus == focusSidebar {
		paneStyle = StylePaneActive
	}
	return paneStyle.Width(sidebarWidth - 2).Height(height).Render(strings.Join(rows, "\n"))
}

func (m Model) renderFileList(height, width int) string {
	innerW := width - 4
	visible := m.visibleEntries()

	var rows []string
	for i := m.offset; i < len(visible) && i < m.offset+height; i++ {
		e := visible[i]
		name := e.Name
		if e.IsDir {
			name += "/"
		}

		var style lipgloss.Style
		switch {
		case i == m.cursor:
			style = StyleCursor
		case e.IsDir:
			style = StyleDir
		case appfs.IsHidden(e.Name):
			style = StyleHidden
		default:
			style = StyleNormal
		}

		var rendered string
		if i == m.cursor {
			rendered = style.Width(innerW).Render(name)
		} else {
			rendered = style.MaxWidth(innerW).Render(name)
		}
		rows = append(rows, rendered)
	}

	for len(rows) < height {
		rows = append(rows, "")
	}

	paneStyle := StylePane
	if m.focus == focusList {
		paneStyle = StylePaneActive
	}
	return paneStyle.Width(width - 2).Height(height).Render(strings.Join(rows, "\n"))
}

// calcPreviewSize returns the cell (cols, rows) that preserve the image's
// aspect ratio while fitting within (maxCols, maxRows).
// cellAspect = cellWidth/cellHeight ≈ 0.5 for most terminal fonts.
func calcPreviewSize(imgW, imgH, maxCols, maxRows int) (cols, rows int) {
	if imgW <= 0 || imgH <= 0 {
		return maxCols, maxRows
	}
	const cellAspect = 0.5
	// Express the image aspect ratio in cell-unit space.
	// One "cell unit" wide = cellAspect pixel-units tall.
	imageAspectInCells := float64(imgW) / (float64(imgH) * cellAspect)
	availAspect := float64(maxCols) / float64(maxRows)

	if imageAspectInCells >= availAspect {
		// Wider than the area → constrained by columns.
		cols = maxCols
		rows = int(math.Round(float64(maxCols) / imageAspectInCells))
		if rows < 1 {
			rows = 1
		}
	} else {
		// Taller than the area → constrained by rows.
		rows = maxRows
		cols = int(math.Round(float64(maxRows) * imageAspectInCells))
		if cols < 1 {
			cols = 1
		}
	}
	return
}

func (m Model) renderKittyPreview() string {
	listW := m.fileListWidth()
	// 1-based column: past sidebar + file-list panes, then past left border+padding of details pane.
	col := sidebarWidth + listW + 3
	row := 3 // title(1) + pane top border(1) + first content row(1)
	maxCols := detailsWidth - 4
	c, r := calcPreviewSize(m.previewW, m.previewH, maxCols, previewCellRows)
	return kitty.Place(m.previewEncoded, col, row, c, r, 1)
}

func (m Model) renderDetails(height int) string {
	innerW := detailsWidth - 4
	var rows []string

	visible := m.visibleEntries()
	if len(visible) == 0 || m.cursor >= len(visible) {
		for len(rows) < height {
			rows = append(rows, "")
		}
		return StylePane.Width(detailsWidth - 2).Height(height).Render(strings.Join(rows, "\n"))
	}

	e := visible[m.cursor]

	// Reserve space for image preview at the top of the panel.
	isImageFile := !e.IsDir && appfs.IsImage(e.Name) && kitty.IsSupported()
	if isImageFile {
		if m.previewEncoded == "" {
			rows = append(rows, StyleDim.Render("loading…"))
		}
		for len(rows) < previewCellRows+1 {
			rows = append(rows, "")
		}
	}

	label := func(s string) string {
		return StyleDetailsLabel.Render(s)
	}
	val := func(s string) string {
		return StyleDetailsValue.MaxWidth(innerW).Render(s)
	}

	// Name
	rows = append(rows, label("NAME"))
	name := e.Name
	if e.IsDir {
		name = StyleDetailsValueDir.MaxWidth(innerW).Render(name + "/")
	} else {
		name = val(name)
	}
	rows = append(rows, name)
	rows = append(rows, "")

	// Type
	rows = append(rows, label("TYPE"))
	if e.IsDir {
		rows = append(rows, val("directory"))
	} else {
		ext := strings.ToLower(filepath.Ext(e.Name))
		if ext == "" {
			ext = "file"
		}
		rows = append(rows, val(ext+" file"))
	}
	rows = append(rows, "")

	// Size
	if e.Info != nil {
		rows = append(rows, label("SIZE"))
		if e.IsDir {
			rows = append(rows, val("—"))
		} else {
			rows = append(rows, val(formatSize(e.Info.Size())))
		}
		rows = append(rows, "")

		// Modified
		rows = append(rows, label("MODIFIED"))
		rows = append(rows, val(e.Info.ModTime().Format("2006-01-02  15:04")))
		rows = append(rows, "")

		// Permissions
		rows = append(rows, label("PERMISSIONS"))
		rows = append(rows, val(e.Info.Mode().String()))
	}

	for len(rows) < height {
		rows = append(rows, "")
	}

	return StylePane.Width(detailsWidth - 2).Height(height).Render(strings.Join(rows, "\n"))
}

func formatSize(b int64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(b)/float64(div), "KMGTPE"[exp])
}

func (m Model) renderSearchBar() string {
	visible := m.visibleEntries()
	count := fmt.Sprintf("  %d results", len(visible))
	query := StyleNormal.Render(m.search.query) + StyleCursor.Render(" ")
	bar := StyleTitle.Render(" 󰍉 ") + StyleDim.Render("/ ") + query + StyleDim.Render(count)
	return StyleStatusBar.Width(m.width).Render(bar)
}

func (m Model) buildStatus() string {
	if m.err != nil {
		return fmt.Sprintf("error: %v", m.err)
	}
	if m.statusMsg != "" {
		return " " + m.statusMsg
	}

	total := len(m.entries)
	pos := 0
	if total > 0 {
		pos = m.cursor + 1
	}

	clip := ""
	if m.clipboard.op == clipCopy {
		clip = fmt.Sprintf("  [copy: %s]", m.clipboard.name)
	} else if m.clipboard.op == clipCut {
		clip = fmt.Sprintf("  [cut: %s]", m.clipboard.name)
	}

	paneKeys := "tab/"

	focusShortcutsMap := make(map[focus]key.Binding)
	for _, sc := range focusShortcuts {
		focusShortcutsMap[sc.target] = sc.binding
	}

	if m.focus == focusSidebar {
		paneKeys += focusShortcutsMap[focusList].Keys()[0]
	} else {
		paneKeys += focusShortcutsMap[focusSidebar].Keys()[0]
	}

	parts := []string{
		fmt.Sprintf("%d/%d", pos, total),
		". menu",
		"tab/w/e panes",
		"H hidden",
		"q quit",
	}

	return " " + strings.Join(parts, "  ") + clip
}
