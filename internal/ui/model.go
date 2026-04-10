package ui

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/ion/ionix-file-manager/internal/clipboard"
	appfs "github.com/ion/ionix-file-manager/internal/fs"
	"github.com/ion/ionix-file-manager/internal/kitty"
)

const sidebarWidth = 22

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

	items = append(items, menuEntry{icon: "󰜺", label: "Cancel", action: menuCancel})
	return items
}

type contextMenuModel struct {
	open   bool
	cursor int
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
	Quit                  key.Binding
}{
	Up:           key.NewBinding(key.WithKeys("up", "k")),
	Down:         key.NewBinding(key.WithKeys("down", "j")),
	Left:         key.NewBinding(key.WithKeys("left", "h", "backspace")),
	Right:        key.NewBinding(key.WithKeys("right", "l", "enter")),
	GoHome:       key.NewBinding(key.WithKeys("~")),
	ToggleHidden: key.NewBinding(key.WithKeys("H")),
	SwitchPane:   key.NewBinding(key.WithKeys("tab")),
	OpenMenu:     key.NewBinding(key.WithKeys(".")),
	Copy:         key.NewBinding(key.WithKeys("ctrl+c")),
	Paste:        key.NewBinding(key.WithKeys("ctrl+v")),
	Search:       key.NewBinding(key.WithKeys("f")),
	Quit:         key.NewBinding(key.WithKeys("q")),
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

	clipboard   fileClipboard
	contextMenu contextMenuModel
	search      searchModel
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

func New(startDir string) Model {
	m := Model{
		cwd:          startDir,
		showHidden:   false,
		kittySupport: kitty.IsSupported(),
		focus:        focusList,
		bookmarks:    buildBookmarks(),
	}
	m.entries, m.err = m.loadEntries()
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
		return m, nil

	case tea.KeyMsg:
		// Context menu captures all input while open.
		if m.contextMenu.open {
			m = m.updateContextMenu(msg)
			return m, nil
		}

		// Search captures most input while active.
		if m.search.active {
			m = m.updateSearch(msg)
			return m, nil
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

		case key.Matches(msg, keyMap.Search):
			m.search.active = true
			m.search.query = ""
			m.cursor = 0
			m.offset = 0
			m.statusMsg = ""

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
					m = m.updateList(msg)
				}
			}
		}
	}

	return m, nil
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

	case menuCancel:
		// nothing
	}

	return m
}

func (m Model) updateSidebar(msg tea.KeyMsg) Model {
	switch {
	case key.Matches(msg, keyMap.Up):
		if m.sidebarCursor > 0 {
			m.sidebarCursor--
		}
	case key.Matches(msg, keyMap.Down):
		if m.sidebarCursor < len(m.bookmarks)-1 {
			m.sidebarCursor++
		}
	case key.Matches(msg, keyMap.Right):
		if len(m.bookmarks) > 0 {
			m.cwd = m.bookmarks[m.sidebarCursor].path
			m.cursor = 0
			m.offset = 0
			m.entries, m.err = m.loadEntries()
			m.focus = focusList
		}
	}
	return m
}

func (m Model) updateSearch(msg tea.KeyMsg) Model {
	listH := m.listHeight()
	visible := m.visibleEntries()

	switch msg.Type {
	case tea.KeyEsc:
		// Cancel: close search, restore position
		m.search.active = false
		m.search.query = ""
		m.cursor = 0
		m.offset = 0

	case tea.KeyEnter:
		// Confirm: close search, leave cursor on the selected item in the full list
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

	return m
}

func (m Model) updateList(msg tea.KeyMsg) Model {
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
			m.cwd = parent
			m.cursor = 0
			m.offset = 0
			m.search = searchModel{}
			m.entries, m.err = m.loadEntries()
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
	}
	return m
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

	return m.renderNormal()
}

func (m Model) renderNormal() string {
	listH := m.listHeight()
	listW := m.width - sidebarWidth - 1

	titleLine := StyleTitle.Render(" ionix ") +
		StyleDim.Render(" › ") +
		StyleNormal.Render(m.cwd)

	body := lipgloss.JoinHorizontal(lipgloss.Top,
		m.renderSidebar(listH),
		m.renderFileList(listH, listW),
	)

	statusLine := StyleStatusBar.Width(m.width).Render(m.buildStatus())

	if m.search.active {
		searchBar := m.renderSearchBar()
		return titleLine + "\n" + body + "\n" + searchBar + "\n" + statusLine
	}
	return titleLine + "\n" + body + "\n" + statusLine
}

func (m Model) renderWithOverlay() string {
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

	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, content)
}

func (m Model) renderSidebar(height int) string {
	innerW := sidebarWidth - 4

	var rows []string
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
