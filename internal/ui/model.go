package ui

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/gif"
	"image/png"
	"math"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/LucasionGS/ionix-file-manager/internal/clipboard"
	appconfig "github.com/LucasionGS/ionix-file-manager/internal/config"
	appfs "github.com/LucasionGS/ionix-file-manager/internal/fs"
	appgit "github.com/LucasionGS/ionix-file-manager/internal/git"
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
	focusSplit
)

// focusShortcut maps a key directly to a focus target.
// Add entries here to register new global focus shortcuts.
type focusShortcut struct {
	binding key.Binding
	target  focus
}

var focusShortcuts = []focusShortcut{
	{binding: key.NewBinding(key.WithKeys("w")), target: focusSidebar},
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

type clipItem struct {
	path string
	name string
}

type fileClipboard struct {
	op    clipOp
	items []clipItem
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
	menuExtract
	menuRename
	menuCancel
)

type menuEntry struct {
	icon   string
	label  string
	action menuAction
}

// menuEntries defines the context menu. Add new items here to extend it.
// buildMenu returns only the context menu entries that apply to the current selection.

// archiveExtractCmd returns a ready-to-run *exec.Cmd that extracts path into
// its parent directory, or (nil, false) when the file is not a recognised
// archive or the required tool is not installed.
func archiveExtractCmd(path string) (*exec.Cmd, bool) {
	name := strings.ToLower(filepath.Base(path))
	dir := filepath.Dir(path)

	buildTar := func() (*exec.Cmd, bool) {
		t, err := exec.LookPath("tar")
		if err != nil {
			return nil, false
		}
		cmd := exec.Command(t, "xf", path)
		cmd.Dir = dir
		return cmd, true
	}

	switch {
	case strings.HasSuffix(name, ".tar"),
		strings.HasSuffix(name, ".tar.gz"),
		strings.HasSuffix(name, ".tgz"),
		strings.HasSuffix(name, ".tar.bz2"),
		strings.HasSuffix(name, ".tbz2"),
		strings.HasSuffix(name, ".tar.xz"),
		strings.HasSuffix(name, ".txz"):
		return buildTar()

	case strings.HasSuffix(name, ".zip"):
		t, err := exec.LookPath("unzip")
		if err != nil {
			return nil, false
		}
		cmd := exec.Command(t, path, "-d", dir)
		cmd.Dir = dir
		return cmd, true

	case strings.HasSuffix(name, ".gz"):
		t, err := exec.LookPath("gzip")
		if err != nil {
			return nil, false
		}
		cmd := exec.Command(t, "-dk", path)
		cmd.Dir = dir
		return cmd, true

	case strings.HasSuffix(name, ".bz2"):
		t, err := exec.LookPath("bzip2")
		if err != nil {
			return nil, false
		}
		cmd := exec.Command(t, "-dk", path)
		cmd.Dir = dir
		return cmd, true

	case strings.HasSuffix(name, ".xz"):
		t, err := exec.LookPath("xz")
		if err != nil {
			return nil, false
		}
		cmd := exec.Command(t, "-dk", path)
		cmd.Dir = dir
		return cmd, true
	}
	return nil, false
}

// Add new items here; the disabled-item pattern is replaced by simply omitting entries.
func (m *Model) buildMenu() []menuEntry {
	active := m.activeVisible()
	cursor := m.activeCursor()
	hasSelection := len(active) > 0
	selected := func() appfs.Entry {
		if hasSelection {
			return active[cursor]
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

	if m.clipboard.op != clipNone && len(m.clipboard.items) > 0 {
		items = append(items, menuEntry{icon: "󰆒", label: "Paste", action: menuPaste})
	}

	if hasSelection {
		items = append(items, menuEntry{icon: "󰅎", label: "Copy path", action: menuCopyPath})
	}

	if e := selected(); hasSelection && !e.IsDir && appfs.IsImage(e.Name) {
		items = append(items, menuEntry{icon: "󰋩", label: "Copy image", action: menuCopyImage})
	}

	if e := selected(); hasSelection && !e.IsDir {
		if _, ok := archiveExtractCmd(e.Path); ok {
			items = append(items, menuEntry{icon: "󰛫", label: "Extract", action: menuExtract})
		}
	}

	if e := selected(); hasSelection && e.IsDir {
		if m.isFavorite(e.Path) {
			items = append(items, menuEntry{icon: "󰀻", label: "Remove favorite", action: menuFavoriteToggle})
		} else {
			items = append(items, menuEntry{icon: "󰀼", label: "Add to favorites", action: menuFavoriteToggle})
		}
	}

	if hasSelection {
		items = append(items, menuEntry{icon: "󰐅", label: "Rename", action: menuRename})
	}

	items = append(items, menuEntry{icon: "󰜺", label: "Cancel", action: menuCancel})
	return items
}

type contextMenuModel struct {
	open   bool
	cursor int
}

// ---------------------------------------------------------------------------
// Rename modal
// ---------------------------------------------------------------------------

type renameModal struct {
	open   bool
	target appfs.Entry
	query  string
	err    string
}

// ---------------------------------------------------------------------------
// Delete confirmation modal
// ---------------------------------------------------------------------------

type deleteModal struct {
	open         bool
	target       appfs.Entry   // primary entry (for display)
	multiTargets []appfs.Entry // all targets when multiple selected
}

// ---------------------------------------------------------------------------
// Go-to path modal
// ---------------------------------------------------------------------------

type goToModal struct {
	open  bool
	query string
}

// ---------------------------------------------------------------------------
// Macro modal
// ---------------------------------------------------------------------------

type macroModal struct {
	open   bool
	cursor int
	macros []appconfig.Macro

	// input phase: shown when the selected macro's Command contains $INPUT
	inputMode  bool
	inputQuery string
	inputMacro appconfig.Macro
}

// ---------------------------------------------------------------------------
// Macro manager modal
// ---------------------------------------------------------------------------

// macroManagerModal drives the full CRUD UI for macros.
// editing=false → list view; editing=true → edit/create form.
type macroManagerModal struct {
	open   bool
	cursor int
	macros []appconfig.Macro

	// form state
	editing        bool
	isNew          bool
	editIdx        int
	fieldCursor    int // 0=Name 1=Command 2=Filter 3=Background
	editName       string
	editCommand    string
	editFilter     string // comma-separated extensions, e.g. ".png, .jpg"
	editBackground bool
	err            string
}

// editField appends s to the currently focused text field.
func (mm *macroManagerModal) editField(s string) {
	switch mm.fieldCursor {
	case 0:
		mm.editName += s
	case 1:
		mm.editCommand += s
	case 2:
		mm.editFilter += s
	}
}

// deleteFieldChar removes the last rune from the currently focused text field.
func (mm *macroManagerModal) deleteFieldChar() {
	var target *string
	switch mm.fieldCursor {
	case 0:
		target = &mm.editName
	case 1:
		target = &mm.editCommand
	case 2:
		target = &mm.editFilter
	default:
		return
	}
	runes := []rune(*target)
	if len(runes) > 0 {
		*target = string(runes[:len(runes)-1])
	}
}

// ---------------------------------------------------------------------------
// New item modal
// ---------------------------------------------------------------------------

type newItemKind int

const (
	newItemDir newItemKind = iota
	newItemFile
)

type newItemModal struct {
	open  bool
	kind  newItemKind
	query string
	err   string // parse/validation error shown inline
}

// ---------------------------------------------------------------------------
// Command palette
// ---------------------------------------------------------------------------

type paletteCmd struct {
	icon  string
	label string
	run   func(m Model) (Model, tea.Cmd)
}

type paletteModel struct {
	open   bool
	query  string
	cursor int
}

// allPaletteCommands is the full registry of palette actions.
var allPaletteCommands = []paletteCmd{
	{"󰋜", "Go home", func(m Model) (Model, tea.Cmd) {
		home, err := os.UserHomeDir()
		if err == nil {
			if m.focus == focusSplit {
				m.cwd2 = home
				m.cursor2 = 0
				m.offset2 = 0
				m.reloadSplit()
			} else {
				m.cwd = home
				m.cursor = 0
				m.offset = 0
				m.reloadMain()
			}
		}
		return m, m.maybeLoadPreview()
	}},
	{"󰆏", "Copy", func(m Model) (Model, tea.Cmd) {
		av := m.activeVisible()
		if len(av) > 0 {
			sel := m.activeSelectedPaths()
			var items []clipItem
			for _, e := range av {
				if sel[e.Path] {
					items = append(items, clipItem{path: e.Path, name: e.Name})
				}
			}
			if len(items) == 0 {
				e := av[m.activeCursor()]
				items = []clipItem{{path: e.Path, name: e.Name}}
			}
			m.clipboard = fileClipboard{op: clipCopy, items: items}
			if len(items) == 1 {
				m.statusMsg = fmt.Sprintf("copied  %s", items[0].name)
			} else {
				m.statusMsg = fmt.Sprintf("copied  %d items", len(items))
			}
		}
		return m, nil
	}},
	{"󰆐", "Cut", func(m Model) (Model, tea.Cmd) {
		av := m.activeVisible()
		if len(av) > 0 {
			sel := m.activeSelectedPaths()
			var items []clipItem
			for _, e := range av {
				if sel[e.Path] {
					items = append(items, clipItem{path: e.Path, name: e.Name})
				}
			}
			if len(items) == 0 {
				e := av[m.activeCursor()]
				items = []clipItem{{path: e.Path, name: e.Name}}
			}
			m.clipboard = fileClipboard{op: clipCut, items: items}
			if len(items) == 1 {
				m.statusMsg = fmt.Sprintf("cut  %s", items[0].name)
			} else {
				m.statusMsg = fmt.Sprintf("cut  %d items", len(items))
			}
		}
		return m, nil
	}},
	{"󰆒", "Paste", func(m Model) (Model, tea.Cmd) {
		if m.clipboard.op != clipNone && len(m.clipboard.items) > 0 {
			var lastErr error
			pasteCount := len(m.clipboard.items)
			var lastName string
			for _, item := range m.clipboard.items {
				dst := filepath.Join(m.activeCwd(), item.name)
				var err error
				if m.clipboard.op == clipCopy {
					err = appfs.CopyEntry(item.path, dst)
				} else {
					err = appfs.MoveEntry(item.path, dst)
				}
				if err != nil {
					lastErr = err
				} else {
					lastName = item.name
				}
			}
			if m.clipboard.op == clipCut {
				m.clipboard = fileClipboard{}
			}
			if lastErr != nil {
				m.statusMsg = fmt.Sprintf("error: %v", lastErr)
			} else if pasteCount == 1 {
				m.statusMsg = fmt.Sprintf("pasted  %s", lastName)
			} else {
				m.statusMsg = fmt.Sprintf("pasted  %d items", pasteCount)
			}
			if m.focus == focusSplit {
				m.reloadSplit()
			} else {
				m.reloadMainQuiet()
			}
		}
		return m, m.maybeLoadPreview()
	}},
	{"󰅎", "Delete", func(m Model) (Model, tea.Cmd) {
		visible := m.activeVisible()
		if m.focus != focusSidebar && len(visible) > 0 {
			sel := m.activeSelectedPaths()
			if len(sel) > 0 {
				var targets []appfs.Entry
				for _, e := range visible {
					if sel[e.Path] {
						targets = append(targets, e)
					}
				}
				if len(targets) > 0 {
					m.deleteConfirm = deleteModal{open: true, target: targets[0], multiTargets: targets}
				}
			} else {
				m.deleteConfirm = deleteModal{open: true, target: visible[m.activeCursor()]}
			}
			m.statusMsg = ""
		}
		return m, nil
	}},
	{"󰉋", "New folder", func(m Model) (Model, tea.Cmd) {
		if m.focus != focusSidebar {
			m.newItem = newItemModal{open: true, kind: newItemDir}
			m.statusMsg = ""
		}
		return m, nil
	}},
	{"󰈔", "New file", func(m Model) (Model, tea.Cmd) {
		if m.focus != focusSidebar {
			m.newItem = newItemModal{open: true, kind: newItemFile}
			m.statusMsg = ""
		}
		return m, nil
	}},
	{"󰍉", "Search", func(m Model) (Model, tea.Cmd) {
		if m.focus != focusSplit {
			m.search.active = true
			m.search.query = ""
			m.cursor = 0
			m.offset = 0
			m.statusMsg = ""
		}
		return m, nil
	}},
	{"󰋞", "Go to path", func(m Model) (Model, tea.Cmd) {
		m.goTo = goToModal{open: true, query: m.activeCwd()}
		return m, nil
	}},
	{"󱏻", "Toggle split pane", func(m Model) (Model, tea.Cmd) {
		if m.showSplit {
			m.showSplit = false
			if m.focus == focusSplit {
				m.focus = focusList
			}
		} else {
			m.showSplit = true
			m.cwd2 = m.cwd
			m.cursor2 = 0
			m.offset2 = 0
			m.reloadSplit()
			m.focus = focusSplit
		}
		return m, nil
	}},
	{"󰈙", "Toggle details panel", func(m Model) (Model, tea.Cmd) {
		m.showDetails = !m.showDetails
		if !m.showDetails {
			m.previewPath = ""
			m.previewEncoded = ""
		}
		m.saveUIPrefs()
		return m, m.maybeLoadPreview()
	}},
	{"󰈉", "Toggle hidden files", func(m Model) (Model, tea.Cmd) {
		m.showHidden = !m.showHidden
		m.cursor = 0
		m.offset = 0
		m.reloadMain()
		if m.showSplit {
			m.cursor2 = 0
			m.offset2 = 0
			m.reloadSplit()
		}
		m.saveUIPrefs()
		return m, m.maybeLoadPreview()
	}},
	{"󰀼", "Toggle favorite", func(m Model) (Model, tea.Cmd) {
		av := m.activeVisible()
		if len(av) > 0 {
			e := av[m.activeCursor()]
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
		return m, nil
	}},
	{"�", "Manage macros", func(m Model) (Model, tea.Cmd) {
		m.openMacroManagerModal()
		return m, nil
	}},
	{"�󰗼", "Quit", func(m Model) (Model, tea.Cmd) {
		stopAudio(&m.audioPlayer)
		return m, tea.Quit
	}},
}

func paletteFilter(query string) []paletteCmd {
	if query == "" {
		return allPaletteCommands
	}
	q := strings.ToLower(query)
	var out []paletteCmd
	for _, c := range allPaletteCommands {
		if strings.Contains(strings.ToLower(c.label), q) {
			out = append(out, c)
		}
	}
	return out
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

// macroClosedMsg is sent when a foreground macro process exits.
type macroClosedMsg struct{ err error }

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
	encoded   string          // base64 PNG (static or first frame)
	frames    []string        // base64 PNG per frame (animated GIF only)
	frameDurs []time.Duration // per-frame delays (animated GIF only)
	imgW      int
	imgH      int
}

type imageModalState struct {
	open    bool
	path    string
	encoded string // base64 PNG shown in render (updated per-frame for GIFs)
	imgW    int
	imgH    int
	cache   map[string]imageModalCacheEntry
	// GIF animation
	frames       []string
	frameDurs    []time.Duration
	currentFrame int
	isAnimated   bool
}

// gifTickMsg is sent by the animation ticker to advance one frame.
type gifTickMsg struct{ path string }

func gifTickCmd(path string, d time.Duration) tea.Cmd {
	return tea.Tick(d, func(_ time.Time) tea.Msg { return gifTickMsg{path: path} })
}

// ---------------------------------------------------------------------------
// Audio modal
// ---------------------------------------------------------------------------

type audioModal struct {
	open       bool
	path       string
	name       string  // display name
	dur        float64 // total duration in seconds (0 = unknown)
	elapsed    float64 // seconds played
	paused     bool
	proc       *exec.Cmd
	playerOK   bool   // a suitable player was found
	playerBin  string // "mpv" or "ffplay"
	ipcSocket  string // mpv IPC socket path
	generation int    // incremented on each new process; guards audioFinishedMsg
}

// audioTickMsg fires every second while audio is playing.
type audioTickMsg struct{ path string }

func audioTickCmd(path string) tea.Cmd {
	return tea.Tick(time.Second, func(_ time.Time) tea.Msg { return audioTickMsg{path: path} })
}

// audioDurMsg carries the probed duration for a file.
type audioDurMsg struct {
	path string
	dur  float64
}

// probeDurationCmd uses ffprobe to determine the duration of an audio file.
func probeDurationCmd(path string) tea.Cmd {
	return func() tea.Msg {
		ffprobe, err := exec.LookPath("ffprobe")
		if err != nil {
			return audioDurMsg{path: path, dur: 0}
		}
		out, err := exec.Command(ffprobe,
			"-v", "error",
			"-show_entries", "format=duration",
			"-of", "default=noprint_wrappers=1:nokey=1",
			path,
		).Output()
		if err != nil {
			return audioDurMsg{path: path, dur: 0}
		}
		var d float64
		fmt.Sscanf(strings.TrimSpace(string(out)), "%f", &d)
		return audioDurMsg{path: path, dur: d}
	}
}

// audioPlayer returns a ready-to-start *exec.Cmd for the best available player,
// or nil if none is found.  The process must be started with Start() (not Run()).
// buildAudioCmd creates a player command for path starting at startAt seconds.
// Returns (cmd, playerBin, ipcSocketPath); ipcSocketPath is set only for mpv.
func buildAudioCmd(path string, startAt float64) (*exec.Cmd, string, string) {
	if mpvBin, err := exec.LookPath("mpv"); err == nil {
		sock := filepath.Join(os.TempDir(), fmt.Sprintf("ifm-mpv-%d.sock", os.Getpid()))
		args := []string{
			"--no-video",
			"--quiet",
			"--really-quiet",
			fmt.Sprintf("--input-ipc-server=%s", sock),
		}
		if startAt > 0 {
			args = append(args, fmt.Sprintf("--start=%.3f", startAt))
		}
		args = append(args, path)
		return exec.Command(mpvBin, args...), "mpv", sock
	}
	if ffplayBin, err := exec.LookPath("ffplay"); err == nil {
		args := []string{"-nodisp", "-autoexit", "-loglevel", "quiet"}
		if startAt > 0 {
			args = append(args, "-ss", fmt.Sprintf("%.3f", startAt))
		}
		args = append(args, path)
		return exec.Command(ffplayBin, args...), "ffplay", ""
	}
	return nil, "", ""
}

// seekMPVCmd sends a relative seek command to an mpv IPC socket.
func seekMPVCmd(socketPath string, delta float64) tea.Cmd {
	return func() tea.Msg {
		conn, err := net.Dial("unix", socketPath)
		if err != nil {
			return nil
		}
		defer conn.Close()
		fmt.Fprintf(conn, `{"command":["seek",%g,"relative"]}`+"\n", delta)
		return nil
	}
}

// stopAudio kills the player process if one is running.
func stopAudio(a *audioModal) {
	if a == nil || a.proc == nil || a.proc.Process == nil {
		return
	}
	_ = a.proc.Process.Kill()
	_ = a.proc.Wait()
	a.proc = nil
}

// pauseAudio sends SIGSTOP to the player process.
func pauseAudio(a *audioModal) {
	if a == nil || a.proc == nil || a.proc.Process == nil {
		return
	}
	_ = a.proc.Process.Signal(sigStop)
}

// resumeAudio sends SIGCONT to the player process.
func resumeAudio(a *audioModal) {
	if a == nil || a.proc == nil || a.proc.Process == nil {
		return
	}
	_ = a.proc.Process.Signal(sigCont)
}

// audioPlayerFinishedCmd returns a command that waits for the player process
// to finish, then sends an audioFinishedMsg.
type audioFinishedMsg struct {
	path       string
	generation int
}

func waitAudioCmd(path string, cmd *exec.Cmd, gen int) tea.Cmd {
	return func() tea.Msg {
		_ = cmd.Wait()
		return audioFinishedMsg{path: path, generation: gen}
	}
}

// ---------------------------------------------------------------------------
// Image modal messages
// ---------------------------------------------------------------------------

type imageModalLoadedMsg struct {
	path      string
	encoded   string
	frames    []string
	frameDurs []time.Duration
	imgW      int
	imgH      int
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
		// Try animated GIF first.
		if strings.ToLower(filepath.Ext(path)) == ".gif" {
			if msg := loadGIFFrames(path, data); msg != nil {
				return *msg
			}
		}
		// Static image fallback.
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

// loadGIFFrames decodes a GIF and returns a fully-populated imageModalLoadedMsg,
// or nil if the GIF has only one frame (treat as static).
func loadGIFFrames(path string, data []byte) *imageModalLoadedMsg {
	g, err := gif.DecodeAll(bytes.NewReader(data))
	if err != nil || len(g.Image) <= 1 {
		return nil
	}
	cw, ch := g.Config.Width, g.Config.Height
	if cw == 0 {
		cw = g.Image[0].Bounds().Max.X
	}
	if ch == 0 {
		ch = g.Image[0].Bounds().Max.Y
	}

	// Composite frames onto a canvas respecting disposal methods.
	canvas := image.NewRGBA(image.Rect(0, 0, cw, ch))
	prevCanvas := image.NewRGBA(image.Rect(0, 0, cw, ch))

	// Fill canvas with the GIF background colour.
	if int(g.BackgroundIndex) < len(g.Image[0].Palette) {
		bg := g.Image[0].Palette[g.BackgroundIndex]
		draw.Draw(canvas, canvas.Bounds(), &image.Uniform{bg}, image.Point{}, draw.Src)
	}

	var frames []string
	var durs []time.Duration
	for i, frame := range g.Image {
		// Save canvas before drawing for DisposalPrevious.
		copy(prevCanvas.Pix, canvas.Pix)

		// Composite this frame.
		draw.Draw(canvas, frame.Rect, frame, frame.Rect.Min, draw.Over)

		// Encode the composited canvas as PNG.
		scaled := scaleDown(canvas, imageModalMaxPx, imageModalMaxPx)
		var buf bytes.Buffer
		if err := png.Encode(&buf, scaled); err != nil {
			continue
		}
		frames = append(frames, kitty.Encode(buf.Bytes()))

		delay := g.Delay[i]
		if delay <= 0 {
			delay = 10 // default 100ms
		}
		durs = append(durs, time.Duration(delay)*10*time.Millisecond)

		// Apply disposal for next frame.
		switch g.Disposal[i] {
		case gif.DisposalBackground:
			draw.Draw(canvas, frame.Rect, &image.Uniform{color.RGBA{0, 0, 0, 0}}, image.Point{}, draw.Src)
		case gif.DisposalPrevious:
			copy(canvas.Pix, prevCanvas.Pix)
			// DisposalNone / default: leave canvas as-is.
		}
	}

	if len(frames) == 0 {
		return nil
	}

	b := canvas.Bounds()
	return &imageModalLoadedMsg{
		path:      path,
		encoded:   frames[0],
		frames:    frames,
		frameDurs: durs,
		imgW:      b.Dx(),
		imgH:      b.Dy(),
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
		var tickCmd tea.Cmd
		if len(ce.frames) > 1 {
			m.imageModal.frames = ce.frames
			m.imageModal.frameDurs = ce.frameDurs
			m.imageModal.currentFrame = 0
			m.imageModal.isAnimated = true
			tickCmd = gifTickCmd(path, ce.frameDurs[0])
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
				return tea.Batch(tickCmd, m.preloadNeighbours(imgIndices, ci, visible))
			}
		}
		return tickCmd
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
		var tickCmd tea.Cmd
		if len(ce.frames) > 1 {
			m.imageModal.frames = ce.frames
			m.imageModal.frameDurs = ce.frameDurs
			m.imageModal.currentFrame = 0
			m.imageModal.isAnimated = true
			tickCmd = gifTickCmd(entry.Path, ce.frameDurs[0])
		}
		// Pre-load neighbours in the background.
		return tea.Batch(tickCmd, m.preloadNeighbours(imgIndices, next, visible))
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

// openAudioModal starts playback of path and opens the audio modal.
// If another track is already playing it is stopped first.
func (m *Model) openAudioModal(path string) tea.Cmd {
	// Stop any current playback.
	stopAudio(&m.audioPlayer)

	cmd, bin, sock := buildAudioCmd(path, 0)
	if cmd == nil {
		m.statusMsg = "audio playback requires mpv or ffplay"
		return nil
	}
	if err := cmd.Start(); err != nil {
		m.statusMsg = fmt.Sprintf("audio error: %v", err)
		return nil
	}
	const gen = 1
	m.audioPlayer = audioModal{
		open:       true,
		path:       path,
		name:       filepath.Base(path),
		proc:       cmd,
		playerOK:   true,
		playerBin:  bin,
		ipcSocket:  sock,
		generation: gen,
	}
	return tea.Batch(
		audioTickCmd(path),
		probeDurationCmd(path),
		waitAudioCmd(path, cmd, gen),
	)
}

// seekAudio seeks the current track by delta seconds (positive = forward).
func (m *Model) seekAudio(delta float64) tea.Cmd {
	a := &m.audioPlayer
	if !a.open {
		return nil
	}
	newElapsed := a.elapsed + delta
	if newElapsed < 0 {
		newElapsed = 0
	}
	if a.dur > 0 && newElapsed > a.dur {
		newElapsed = a.dur
	}
	a.elapsed = newElapsed

	// mpv: send IPC seek — no process restart needed.
	if a.playerBin == "mpv" && a.ipcSocket != "" && a.proc != nil {
		return seekMPVCmd(a.ipcSocket, delta)
	}

	// ffplay (no IPC): if paused, just update the counter for when it resumes.
	// If playing, kill and restart at the new position.
	if a.paused || a.proc == nil {
		return nil
	}
	if a.proc.Process != nil {
		_ = a.proc.Process.Kill()
		_ = a.proc.Wait()
		a.proc = nil
	}
	cmd, bin, sock := buildAudioCmd(a.path, newElapsed)
	if cmd == nil {
		return nil
	}
	if err := cmd.Start(); err != nil {
		return nil
	}
	a.generation++
	a.proc = cmd
	a.playerBin = bin
	a.ipcSocket = sock
	return tea.Batch(waitAudioCmd(a.path, cmd, a.generation), audioTickCmd(a.path))
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
	CyclePanes            key.Binding
	ToggleSplit           key.Binding
	GoTo                  key.Binding
	MarkSelect            key.Binding
	MarkSelectRange       key.Binding
	NewDir                key.Binding
	NewFile               key.Binding
	Rename                key.Binding
	Palette               key.Binding
	RunMacro              key.Binding
	ToggleGitPane         key.Binding
}{
	Up:              key.NewBinding(key.WithKeys("up", "k")),
	Down:            key.NewBinding(key.WithKeys("down", "j")),
	Left:            key.NewBinding(key.WithKeys("left", "h", "backspace")),
	Right:           key.NewBinding(key.WithKeys("right", "l", "enter")),
	GoHome:          key.NewBinding(key.WithKeys("~")),
	ToggleHidden:    key.NewBinding(key.WithKeys("H")),
	SwitchPane:      key.NewBinding(key.WithKeys("tab")),
	OpenMenu:        key.NewBinding(key.WithKeys(".")),
	Copy:            key.NewBinding(key.WithKeys("ctrl+c")),
	Paste:           key.NewBinding(key.WithKeys("ctrl+v")),
	Search:          key.NewBinding(key.WithKeys("f")),
	ToggleDetails:   key.NewBinding(key.WithKeys("d")),
	Delete:          key.NewBinding(key.WithKeys("delete", "D")),
	Quit:            key.NewBinding(key.WithKeys("q")),
	ToggleFavorite:  key.NewBinding(key.WithKeys("b")),
	CyclePanes:      key.NewBinding(key.WithKeys("e")),
	ToggleSplit:     key.NewBinding(key.WithKeys("s")),
	GoTo:            key.NewBinding(key.WithKeys(":")),
	MarkSelect:      key.NewBinding(key.WithKeys(" ")),
	MarkSelectRange: key.NewBinding(key.WithKeys("V")),
	NewDir:          key.NewBinding(key.WithKeys("n")),
	NewFile:         key.NewBinding(key.WithKeys("N")),
	Rename:          key.NewBinding(key.WithKeys("f2")),
	Palette:         key.NewBinding(key.WithKeys("f1")),
	RunMacro:        key.NewBinding(key.WithKeys(",")),
	ToggleGitPane:   key.NewBinding(key.WithKeys("G")),
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

	clipboard        fileClipboard
	contextMenu      contextMenuModel
	deleteConfirm    deleteModal
	goTo             goToModal
	newItem          newItemModal
	palette          paletteModel
	imageModal       imageModalState
	audioPlayer      audioModal
	renameModal      renameModal
	search           searchModel
	macroRunner      macroModal
	macroManager     macroManagerModal
	showDetails      bool
	previewPath      string
	previewEncoded   string // base64-encoded, ready for kitty.Place
	previewW         int
	previewH         int
	previewCache     map[string]imageModalCacheEntry // reuses imageModalCacheEntry: encoded+dims
	pendingSelect    string                          // filename to select on first WindowSizeMsg
	pendingOpenImage bool                            // open image modal on first WindowSizeMsg

	showSplit bool
	cwd2      string
	entries2  []appfs.Entry
	cursor2   int
	offset2   int

	selectedPaths     map[string]bool
	lastSelectedPath  string
	selected2Paths    map[string]bool
	lastSelected2Path string

	showGitPane bool
	gitRoot     string                       // cached repo root for main pane
	gitStatus   map[string]appgit.FileStatus // entry name → status for main pane CWD
	gitRoot2    string                       // cached repo root for split pane
	gitStatus2  map[string]appgit.FileStatus // entry name → status for split pane CWD
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

// saveUIPrefs persists the current UI toggle states to the config file,
// preserving Colors, Favorites, and any other fields.
func (m Model) saveUIPrefs() {
	cfg, _ := appconfig.Load()
	cfg.ShowDetails = m.showDetails
	cfg.ShowHidden = m.showHidden
	cfg.ShowGitPane = m.showGitPane
	_ = appconfig.Save(cfg)
}

func New(startDir, selectName string) Model {
	cfg, _ := appconfig.Load()
	ApplyColors(cfg.Colors)
	m := Model{
		cwd:          filepath.Clean(startDir),
		showHidden:   cfg.ShowHidden,
		showDetails:  cfg.ShowDetails,
		showGitPane:  cfg.ShowGitPane,
		kittySupport: kitty.IsSupported(),
		focus:        focusList,
		bookmarks:    buildBookmarks(),
		favorites:    cfg.Favorites,
	}
	m.entries, m.err = m.loadEntries()
	m.refreshGitStatus()
	if selectName != "" {
		for i, e := range m.entries {
			if e.Name == selectName {
				m.cursor = i
				m.pendingSelect = selectName
				if !e.IsDir && appfs.IsImage(e.Name) {
					m.pendingOpenImage = true
				}
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

// fileListWidth returns the column width allocated to each file list pane.
func (m Model) fileListWidth() int {
	w := m.width - sidebarWidth
	if m.showDetails || m.showGitPane {
		w -= detailsWidth
	}
	if m.showSplit {
		w /= 2
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
	visible := m.activeVisible()
	cursor := m.activeCursor()
	if len(visible) == 0 || cursor < 0 || cursor >= len(visible) {
		return nil
	}
	e := visible[cursor]
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

// activeVisible returns visible entries for the currently focused file pane.
func (m Model) activeVisible() []appfs.Entry {
	if m.focus == focusSplit {
		return m.entries2
	}
	return m.visibleEntries()
}

// activeCursor returns the cursor index for the currently focused file pane.
func (m Model) activeCursor() int {
	if m.focus == focusSplit {
		return m.cursor2
	}
	return m.cursor
}

// activeCwd returns the working directory of the currently focused file pane.
func (m Model) activeCwd() string {
	if m.focus == focusSplit {
		return m.cwd2
	}
	return m.cwd
}

func (m Model) activeSelectedPaths() map[string]bool {
	if m.focus == focusSplit {
		return m.selected2Paths
	}
	return m.selectedPaths
}

// loadEntries2 loads and filters directory entries for the second pane.
func (m Model) loadEntries2() ([]appfs.Entry, error) {
	all, err := appfs.List(m.cwd2)
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

// refreshGitStatus updates the cached git repo root and file statuses for the
// main pane. It reuses the cached root when navigating deeper into the same repo.
func (m *Model) refreshGitStatus() {
	m.gitRoot = appgit.DetectRepo(m.cwd, m.gitRoot)
	m.gitStatus = appgit.Status(m.cwd, m.gitRoot)
}

// refreshGitStatus2 updates the cached git repo root and file statuses for the
// split pane.
func (m *Model) refreshGitStatus2() {
	m.gitRoot2 = appgit.DetectRepo(m.cwd2, m.gitRoot2)
	m.gitStatus2 = appgit.Status(m.cwd2, m.gitRoot2)
}

// reloadMain loads entries for the main pane and refreshes its git status.
func (m *Model) reloadMain() {
	m.entries, m.err = m.loadEntries()
	m.refreshGitStatus()
}

// reloadMainQuiet loads entries for the main pane (ignoring errors) and refreshes git status.
func (m *Model) reloadMainQuiet() {
	m.entries, _ = m.loadEntries()
	m.refreshGitStatus()
}

// reloadSplit loads entries for the split pane and refreshes its git status.
func (m *Model) reloadSplit() {
	m.entries2, _ = m.loadEntries2()
	m.refreshGitStatus2()
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
		if m.pendingOpenImage && m.cursor >= 0 && m.cursor < len(m.entries) {
			m.pendingOpenImage = false
			e := m.entries[m.cursor]
			imgCmd := m.openImageModal(e.Path)
			return m, tea.Batch(m.maybeLoadPreview(), imgCmd)
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
				encoded:   msg.encoded,
				frames:    msg.frames,
				frameDurs: msg.frameDurs,
				imgW:      msg.imgW,
				imgH:      msg.imgH,
			}
			if msg.path == m.imageModal.path {
				m.imageModal.encoded = msg.encoded
				m.imageModal.imgW = msg.imgW
				m.imageModal.imgH = msg.imgH
				if len(msg.frames) > 1 {
					m.imageModal.frames = msg.frames
					m.imageModal.frameDurs = msg.frameDurs
					m.imageModal.currentFrame = 0
					m.imageModal.isAnimated = true
					return m, gifTickCmd(msg.path, msg.frameDurs[0])
				}
			}
		}
		return m, nil

	case gifTickMsg:
		if m.imageModal.open && m.imageModal.isAnimated && m.imageModal.path == msg.path {
			next := (m.imageModal.currentFrame + 1) % len(m.imageModal.frames)
			m.imageModal.currentFrame = next
			m.imageModal.encoded = m.imageModal.frames[next]
			return m, gifTickCmd(msg.path, m.imageModal.frameDurs[next])
		}
		return m, nil

	case audioDurMsg:
		if m.audioPlayer.open && m.audioPlayer.path == msg.path {
			m.audioPlayer.dur = msg.dur
		}
		return m, nil

	case audioTickMsg:
		if m.audioPlayer.open && m.audioPlayer.path == msg.path && !m.audioPlayer.paused {
			m.audioPlayer.elapsed++
			return m, audioTickCmd(msg.path)
		}
		return m, nil

	case audioFinishedMsg:
		if m.audioPlayer.open && m.audioPlayer.path == msg.path && m.audioPlayer.generation == msg.generation {
			m.audioPlayer.proc = nil
			// Leave modal open so user can see it finished; elapsed stays.
			m.audioPlayer.paused = true
		}
		return m, nil

	case editorClosedMsg:
		if msg.err != nil {
			m.statusMsg = fmt.Sprintf("editor error: %v", msg.err)
		}
		m.reloadMainQuiet()
		return m, m.maybeLoadPreview()

	case macroClosedMsg:
		if msg.err != nil {
			m.statusMsg = fmt.Sprintf("macro error: %v", msg.err)
		}
		m.reloadMainQuiet()
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

		// Audio modal captures all input while open.
		if m.audioPlayer.open {
			switch {
			case msg.String() == "q" || msg.String() == "Q" || msg.Type == tea.KeyEsc:
				stopAudio(&m.audioPlayer)
				m.audioPlayer = audioModal{}
			case msg.String() == " " || msg.String() == "p":
				if m.audioPlayer.paused {
					resumeAudio(&m.audioPlayer)
					m.audioPlayer.paused = false
					return m, audioTickCmd(m.audioPlayer.path)
				} else {
					pauseAudio(&m.audioPlayer)
					m.audioPlayer.paused = true
				}
			case key.Matches(msg, keyMap.Left):
				return m, m.seekAudio(-1)
			case key.Matches(msg, keyMap.Right):
				return m, m.seekAudio(1)
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

		// Go-to path modal captures all input while open.
		if m.goTo.open {
			var cmd tea.Cmd
			m, cmd = m.updateGoToModal(msg)
			return m, cmd
		}

		// New item modal captures all input while open.
		if m.newItem.open {
			var cmd tea.Cmd
			m, cmd = m.updateNewItemModal(msg)
			return m, cmd
		}

		// Rename modal captures all input while open.
		if m.renameModal.open {
			var cmd tea.Cmd
			m, cmd = m.updateRenameModal(msg)
			return m, cmd
		}

		// Command palette captures all input while open.
		if m.palette.open {
			var cmd tea.Cmd
			m, cmd = m.updatePalette(msg)
			return m, cmd
		}

		// Macro manager captures all input while open.
		if m.macroManager.open {
			var cmd tea.Cmd
			m, cmd = m.updateMacroManager(msg)
			return m, cmd
		}

		// Macro modal captures all input while open.
		if m.macroRunner.open {
			var cmd tea.Cmd
			m, cmd = m.updateMacroModal(msg)
			return m, cmd
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
			if len(m.selectedPaths) > 0 || len(m.selected2Paths) > 0 {
				m.selectedPaths = nil
				m.selected2Paths = nil
				m.lastSelectedPath = ""
				m.lastSelected2Path = ""
				return m, nil
			}
			stopAudio(&m.audioPlayer)
			return m, tea.Quit

		case key.Matches(msg, keyMap.Copy):
			av := m.activeVisible()
			if len(av) > 0 {
				sel := m.activeSelectedPaths()
				var items []clipItem
				if len(sel) > 0 {
					for _, e := range av {
						if sel[e.Path] {
							items = append(items, clipItem{path: e.Path, name: e.Name})
						}
					}
				}
				if len(items) == 0 {
					e := av[m.activeCursor()]
					items = []clipItem{{path: e.Path, name: e.Name}}
				}
				m.clipboard = fileClipboard{op: clipCopy, items: items}
				if len(items) == 1 {
					m.statusMsg = fmt.Sprintf("copied  %s", items[0].name)
				} else {
					m.statusMsg = fmt.Sprintf("copied  %d items", len(items))
				}
			}

		case key.Matches(msg, keyMap.Paste):
			if m.clipboard.op != clipNone && len(m.clipboard.items) > 0 {
				var lastErr error
				pasteCount := len(m.clipboard.items)
				var lastName string
				for _, item := range m.clipboard.items {
					dst := filepath.Join(m.activeCwd(), item.name)
					var err error
					if m.clipboard.op == clipCopy {
						err = appfs.CopyEntry(item.path, dst)
					} else {
						err = appfs.MoveEntry(item.path, dst)
					}
					if err != nil {
						lastErr = err
					} else {
						lastName = item.name
					}
				}
				if m.clipboard.op == clipCut {
					m.clipboard = fileClipboard{}
				}
				if lastErr != nil {
					m.statusMsg = fmt.Sprintf("error: %v", lastErr)
				} else if pasteCount == 1 {
					m.statusMsg = fmt.Sprintf("pasted  %s", lastName)
				} else {
					m.statusMsg = fmt.Sprintf("pasted  %d items", pasteCount)
				}
				if m.focus == focusSplit {
					m.reloadSplit()
				} else {
					m.reloadMainQuiet()
				}
			}

		case key.Matches(msg, keyMap.ToggleDetails):
			m.showDetails = !m.showDetails
			if !m.showDetails {
				m.previewPath = ""
				m.previewEncoded = ""
			}
			m.saveUIPrefs()

		case key.Matches(msg, keyMap.ToggleGitPane):
			m.showGitPane = !m.showGitPane
			m.saveUIPrefs()

		case key.Matches(msg, keyMap.Search):
			if m.focus != focusSplit {
				m.search.active = true
				m.search.query = ""
				m.cursor = 0
				m.offset = 0
				m.statusMsg = ""
			}

		case key.Matches(msg, keyMap.Delete):
			visible := m.activeVisible()
			if m.focus != focusSidebar && len(visible) > 0 {
				sel := m.activeSelectedPaths()
				if len(sel) > 0 {
					var targets []appfs.Entry
					for _, e := range visible {
						if sel[e.Path] {
							targets = append(targets, e)
						}
					}
					if len(targets) > 0 {
						m.deleteConfirm = deleteModal{open: true, target: targets[0], multiTargets: targets}
					}
				} else {
					m.deleteConfirm = deleteModal{open: true, target: visible[m.activeCursor()]}
				}
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

		case key.Matches(msg, keyMap.CyclePanes):
			if m.showSplit {
				if m.focus == focusSplit {
					m.focus = focusList
				} else {
					m.focus = focusSplit
				}
			} else {
				m.focus = focusList
			}

		case key.Matches(msg, keyMap.GoTo):
			if m.focus != focusSidebar {
				m.goTo = goToModal{open: true, query: m.activeCwd()}
			}

		case key.Matches(msg, keyMap.NewDir):
			if m.focus != focusSidebar {
				m.newItem = newItemModal{open: true, kind: newItemDir}
				m.statusMsg = ""
			}

		case key.Matches(msg, keyMap.NewFile):
			if m.focus != focusSidebar {
				m.newItem = newItemModal{open: true, kind: newItemFile}
				m.statusMsg = ""
			}

		case key.Matches(msg, keyMap.Rename):
			if m.focus != focusSidebar {
				active := m.activeVisible()
				cursor := m.activeCursor()
				if len(active) > 0 && cursor >= 0 && cursor < len(active) {
					e := active[cursor]
					m.renameModal = renameModal{open: true, target: e, query: e.Name}
					m.statusMsg = ""
				}
			}

		case key.Matches(msg, keyMap.Palette):
			m.palette = paletteModel{open: true}
			m.statusMsg = ""

		case key.Matches(msg, keyMap.RunMacro):
			if m.focus != focusSidebar {
				m.openMacroModal()
			}

		case key.Matches(msg, keyMap.ToggleSplit):
			if m.showSplit {
				m.showSplit = false
				if m.focus == focusSplit {
					m.focus = focusList
				}
			} else {
				m.showSplit = true
				m.cwd2 = m.cwd
				m.cursor2 = 0
				m.offset2 = 0
				m.reloadSplit()
				m.focus = focusSplit
			}

		case msg.Type == tea.KeyEsc:
			if len(m.selectedPaths) > 0 || len(m.selected2Paths) > 0 {
				m.selectedPaths = nil
				m.selected2Paths = nil
				m.lastSelectedPath = ""
				m.lastSelected2Path = ""
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

	active := m.activeVisible()
	cursor := m.activeCursor()

	switch item.action {
	case menuCopy:
		sel := m.activeSelectedPaths()
		var items []clipItem
		if len(sel) > 0 {
			for _, e := range active {
				if sel[e.Path] {
					items = append(items, clipItem{path: e.Path, name: e.Name})
				}
			}
		}
		if len(items) == 0 {
			e := active[cursor]
			items = []clipItem{{path: e.Path, name: e.Name}}
		}
		m.clipboard = fileClipboard{op: clipCopy, items: items}
		if len(items) == 1 {
			m.statusMsg = fmt.Sprintf("copied  %s", items[0].name)
		} else {
			m.statusMsg = fmt.Sprintf("copied  %d items", len(items))
		}

	case menuCut:
		sel := m.activeSelectedPaths()
		var items []clipItem
		if len(sel) > 0 {
			for _, e := range active {
				if sel[e.Path] {
					items = append(items, clipItem{path: e.Path, name: e.Name})
				}
			}
		}
		if len(items) == 0 {
			e := active[cursor]
			items = []clipItem{{path: e.Path, name: e.Name}}
		}
		m.clipboard = fileClipboard{op: clipCut, items: items}
		if len(items) == 1 {
			m.statusMsg = fmt.Sprintf("cut  %s", items[0].name)
		} else {
			m.statusMsg = fmt.Sprintf("cut  %d items", len(items))
		}

	case menuPaste:
		if m.clipboard.op != clipNone && len(m.clipboard.items) > 0 {
			var lastErr error
			pasteCount := len(m.clipboard.items)
			var lastName string
			for _, item := range m.clipboard.items {
				dst := filepath.Join(m.activeCwd(), item.name)
				var err error
				if m.clipboard.op == clipCopy {
					err = appfs.CopyEntry(item.path, dst)
				} else {
					err = appfs.MoveEntry(item.path, dst)
				}
				if err != nil {
					lastErr = err
				} else {
					lastName = item.name
				}
			}
			if m.clipboard.op == clipCut {
				m.clipboard = fileClipboard{}
			}
			if lastErr != nil {
				m.statusMsg = fmt.Sprintf("error: %v", lastErr)
			} else if pasteCount == 1 {
				m.statusMsg = fmt.Sprintf("pasted  %s", lastName)
			} else {
				m.statusMsg = fmt.Sprintf("pasted  %d items", pasteCount)
			}
			if m.focus == focusSplit {
				m.reloadSplit()
			} else {
				m.reloadMainQuiet()
			}
		}

	case menuCopyPath:
		e := active[cursor]
		if err := clipboard.Write(e.Path); err != nil {
			m.statusMsg = fmt.Sprintf("clipboard error: %v", err)
		} else {
			m.statusMsg = fmt.Sprintf("path copied  %s", e.Path)
		}

	case menuCopyImage:
		e := active[cursor]
		mime, _ := appfs.ImageMIME(e.Name)
		if err := clipboard.WriteImage(e.Path, mime); err != nil {
			m.statusMsg = fmt.Sprintf("clipboard error: %v", err)
		} else {
			m.statusMsg = fmt.Sprintf("image copied  %s", e.Name)
		}

	case menuFavoriteToggle:
		e := active[cursor]
		if e.IsDir {
			wasFav := m.isFavorite(e.Path)
			m.toggleFavorite(e.Path)
			if wasFav {
				m.statusMsg = fmt.Sprintf("removed from favorites  %s", e.Name)
			} else {
				m.statusMsg = fmt.Sprintf("added to favorites  %s", e.Name)
			}
		}

	case menuExtract:
		e := active[cursor]
		if cmd, ok := archiveExtractCmd(e.Path); ok {
			if out, err := cmd.CombinedOutput(); err != nil {
				m.statusMsg = fmt.Sprintf("extract error: %s", strings.TrimSpace(string(out)))
			} else {
				m.statusMsg = fmt.Sprintf("extracted  %s", e.Name)
				if m.focus == focusSplit {
					m.reloadSplit()
				} else {
					m.reloadMainQuiet()
				}
			}
		}

	case menuRename:
		e := active[cursor]
		m.renameModal = renameModal{open: true, target: e, query: e.Name}
		m.statusMsg = ""

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
			m.reloadMain()
			m.focus = focusList
		}
	}
	return m
}

func (m Model) updateDeleteModal(msg tea.KeyMsg) Model {
	switch msg.String() {
	case "y", "Y":
		targets := m.deleteConfirm.multiTargets
		if len(targets) == 0 {
			targets = []appfs.Entry{m.deleteConfirm.target}
		}
		m.deleteConfirm = deleteModal{}
		var deleteErr error
		deleted := 0
		for _, target := range targets {
			if err := appfs.DeleteEntry(target.Path); err != nil {
				deleteErr = err
			} else {
				deleted++
			}
		}
		// Clear selections that were just deleted.
		m.selectedPaths = nil
		m.selected2Paths = nil
		m.lastSelectedPath = ""
		m.lastSelected2Path = ""
		if deleteErr != nil {
			m.statusMsg = fmt.Sprintf("delete error: %v", deleteErr)
		} else if deleted == 1 {
			m.statusMsg = fmt.Sprintf("deleted  %s", targets[0].Name)
		} else {
			m.statusMsg = fmt.Sprintf("deleted  %d items", deleted)
		}
		m.reloadMainQuiet()
		if m.cursor >= len(m.entries) && m.cursor > 0 {
			m.cursor = len(m.entries) - 1
		}
		if m.showSplit {
			m.reloadSplit()
			if m.cursor2 >= len(m.entries2) && m.cursor2 > 0 {
				m.cursor2 = len(m.entries2) - 1
			}
		}
		m.previewPath = ""
		m.previewEncoded = ""
	default:
		// Any other key (n, esc, q, etc.) cancels.
		m.deleteConfirm = deleteModal{}
	}
	return m
}

func (m Model) updateRenameModal(msg tea.KeyMsg) (Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEnter:
		newName := strings.TrimSpace(m.renameModal.query)
		if newName == "" || newName == m.renameModal.target.Name {
			m.renameModal = renameModal{}
			break
		}
		old := m.renameModal.target.Path
		newPath := filepath.Join(filepath.Dir(old), newName)
		m.renameModal = renameModal{}
		if err := appfs.MoveEntry(old, newPath); err != nil {
			m.statusMsg = fmt.Sprintf("rename error: %v", err)
		} else {
			m.statusMsg = fmt.Sprintf("renamed  %s", newName)
			if m.focus == focusSplit {
				m.reloadSplit()
				for i, e := range m.entries2 {
					if e.Path == newPath {
						m.cursor2 = i
						break
					}
				}
			} else {
				m.reloadMainQuiet()
				for i, e := range m.entries {
					if e.Path == newPath {
						m.cursor = i
						break
					}
				}
			}
		}
		return m, m.maybeLoadPreview()

	case tea.KeyEsc:
		m.renameModal = renameModal{}

	case tea.KeyBackspace:
		runes := []rune(m.renameModal.query)
		if len(runes) > 0 {
			m.renameModal.query = string(runes[:len(runes)-1])
		}

	case tea.KeyRunes:
		m.renameModal.query += string(msg.Runes)

	case tea.KeySpace:
		m.renameModal.query += " "
	}
	return m, nil
}

func (m Model) renderRenameModal() string {
	const modalW = 54
	label := StyleDim.Render("Rename " + m.renameModal.target.Name + ":")
	input := StyleNormal.Render(m.renameModal.query) + StyleCursor.Render(" ")
	hint := StyleDim.Render("enter  confirm   esc  cancel")
	var lines []string
	lines = append(lines, "", label, input, "")
	if m.renameModal.err != "" {
		lines = append(lines, StyleSelected.Render("  "+m.renameModal.err))
	}
	lines = append(lines, hint, "")
	box := StylePaneActive.Width(modalW).Render(strings.Join(lines, "\n"))
	return m.overlayModal(box)
}

func (m Model) updateNewItemModal(msg tea.KeyMsg) (Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEnter:
		pattern := strings.TrimSpace(m.newItem.query)
		if pattern == "" {
			m.newItem = newItemModal{}
			break
		}
		kind := m.newItem.kind
		paths, parseErr := appfs.ExpandBraces(pattern)
		if parseErr != nil {
			m.newItem.err = parseErr.Error()
			return m, nil
		}
		m.newItem = newItemModal{}
		base := m.activeCwd()
		var firstCreated string
		created, errCount := 0, 0
		var lastErr error
		for _, p := range paths {
			target := filepath.Join(base, p)
			var err error
			if kind == newItemDir {
				err = appfs.MkdirEntry(target)
			} else {
				if dirErr := appfs.MkdirEntry(filepath.Dir(target)); dirErr != nil {
					err = dirErr
				} else {
					err = appfs.CreateFileEntry(target)
				}
			}
			if err != nil {
				errCount++
				lastErr = err
			} else {
				created++
				if firstCreated == "" {
					firstCreated = filepath.Base(p)
				}
			}
		}
		if created == 0 {
			m.statusMsg = fmt.Sprintf("error: %v", lastErr)
		} else {
			if m.focus == focusSplit {
				m.reloadSplit()
				if m.cursor2 < 0 || m.cursor2 >= len(m.entries2) {
					m.cursor2 = 0
				}
			} else {
				m.reloadMain()
				if m.cursor < 0 || m.cursor >= len(m.entries) {
					m.cursor = 0
				}
			}
			if firstCreated != "" {
				active := m.activeVisible()
				for i, e := range active {
					if e.Name == firstCreated {
						if m.focus == focusSplit {
							m.cursor2 = i
						} else {
							m.cursor = i
						}
						break
					}
				}
			}
			switch {
			case errCount > 0:
				m.statusMsg = fmt.Sprintf("created  %d   %d failed", created, errCount)
			case created == 1 && kind == newItemDir:
				m.statusMsg = fmt.Sprintf("created  %s/", firstCreated)
			case created == 1:
				m.statusMsg = fmt.Sprintf("created  %s", firstCreated)
			default:
				m.statusMsg = fmt.Sprintf("created  %d items", created)
			}
		}
		return m, m.maybeLoadPreview()

	case tea.KeyEsc:
		m.newItem = newItemModal{}

	case tea.KeyBackspace:
		runes := []rune(m.newItem.query)
		if len(runes) > 0 {
			m.newItem.query = string(runes[:len(runes)-1])
		}

	case tea.KeyRunes:
		m.newItem.query += string(msg.Runes)

	case tea.KeySpace:
		m.newItem.query += " "
	}
	return m, nil
}

func (m Model) updatePalette(msg tea.KeyMsg) (Model, tea.Cmd) {
	filtered := paletteFilter(m.palette.query)
	switch msg.Type {
	case tea.KeyEnter:
		if len(filtered) > 0 {
			cmd := filtered[m.palette.cursor]
			m.palette = paletteModel{}
			return cmd.run(m)
		}
		m.palette = paletteModel{}

	case tea.KeyEsc:
		m.palette = paletteModel{}

	case tea.KeyUp:
		if m.palette.cursor > 0 {
			m.palette.cursor--
		}

	case tea.KeyDown:
		if m.palette.cursor < len(filtered)-1 {
			m.palette.cursor++
		}

	case tea.KeyBackspace:
		runes := []rune(m.palette.query)
		if len(runes) > 0 {
			m.palette.query = string(runes[:len(runes)-1])
			// clamp cursor
			f := paletteFilter(m.palette.query)
			if m.palette.cursor >= len(f) {
				m.palette.cursor = max(0, len(f)-1)
			}
		}

	case tea.KeyRunes:
		m.palette.query += string(msg.Runes)
		m.palette.cursor = 0
	}
	return m, nil
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// overlayCenter composites the overlay string centered on top of bg.
// Both are assumed to be rendered terminal strings (may contain ANSI escapes).
// bg is split into lines; overlay lines replace the corresponding bg lines.
func overlayCenter(bg, overlay string, bgW, bgH int) string {
	bgLines := strings.Split(bg, "\n")
	overlayLines := strings.Split(overlay, "\n")

	oH := len(overlayLines)
	oW := lipgloss.Width(overlayLines[0])
	if oW == 0 && len(overlayLines) > 0 {
		// measure the widest line
		for _, l := range overlayLines {
			if w := lipgloss.Width(l); w > oW {
				oW = w
			}
		}
	}

	startRow := (bgH - oH) / 2
	startCol := (bgW - oW) / 2
	if startRow < 0 {
		startRow = 0
	}
	if startCol < 0 {
		startCol = 0
	}

	// Ensure bg has enough lines.
	for len(bgLines) < startRow+oH {
		bgLines = append(bgLines, "")
	}

	for i, ol := range overlayLines {
		row := startRow + i
		if row >= len(bgLines) {
			break
		}
		bgLines[row] = insertAt(bgLines[row], ol, startCol)
	}
	return strings.Join(bgLines, "\n")
}

// insertAt replaces the visible characters of dst starting at column col with src.
// It handles ANSI-escaped strings by working in terms of printable-cell width.
func insertAt(dst, src string, col int) string {
	// Convert to rune slice for indexing, strip ANSI for width accounting.
	// Strategy: walk dst runes tracking visible column; rebuild with src spliced in.
	srcW := lipgloss.Width(src)
	dstW := lipgloss.Width(dst)

	// If dst is shorter than needed, right-pad with spaces.
	if dstW < col {
		dst += strings.Repeat(" ", col-dstW)
	}

	// We work on the raw bytes of dst but need visible-cell positions.
	// Use a simple approach: rebuild left + src + right portions.
	left := visibleTrunc(dst, col)
	leftW := lipgloss.Width(left)
	// Account for any shortfall from wide chars landing on the boundary.
	if leftW < col {
		left += strings.Repeat(" ", col-leftW)
	}

	// Skip dstW chars that src will overwrite.
	right := visibleSkip(dst, col+srcW)

	return left + src + right
}

// visibleTrunc returns the prefix of s whose visible width is <= n.
func visibleTrunc(s string, n int) string {
	if n <= 0 {
		return ""
	}
	// Strip ANSI and count; but we want to preserve ANSI codes in output.
	// Simple approach: use lipgloss to truncate.
	return lipgloss.NewStyle().MaxWidth(n).Render(s)
}

// visibleSkip returns the suffix of s after skipping n visible columns.
func visibleSkip(s string, n int) string {
	plain := stripANSI(s)
	col := 0
	for i, r := range plain {
		if col >= n {
			return plain[i:]
		}
		col += runeWidth(r)
	}
	return ""
}

func stripANSI(s string) string {
	var b strings.Builder
	esc := false
	for _, r := range s {
		if r == '\x1b' {
			esc = true
			continue
		}
		if esc {
			if r == 'm' {
				esc = false
			}
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

func runeWidth(r rune) int {
	if r >= 0x1100 && (r <= 0x115f || r == 0x2329 || r == 0x232a ||
		(r >= 0x2e80 && r <= 0x3247) || (r >= 0x3250 && r <= 0x4dbf) ||
		(r >= 0x4e00 && r <= 0xa4c6) || (r >= 0xa960 && r <= 0xa97c) ||
		(r >= 0xac00 && r <= 0xd7a3) || (r >= 0xf900 && r <= 0xfaff) ||
		(r >= 0xfe10 && r <= 0xfe19) || (r >= 0xfe30 && r <= 0xfe6b) ||
		(r >= 0xff01 && r <= 0xff60) || (r >= 0xffe0 && r <= 0xffe6) ||
		(r >= 0x1b000 && r <= 0x1b001) || (r >= 0x1f200 && r <= 0x1f251) ||
		(r >= 0x1f300 && r <= 0x1f64f) || (r >= 0x20000 && r <= 0x2fffd) ||
		(r >= 0x30000 && r <= 0x3fffd)) {
		return 2
	}
	return 1
}

func (m Model) renderPalette() string {
	const modalW = 52
	const maxVisible = 10

	filtered := paletteFilter(m.palette.query)

	label := StyleDim.Render("Command palette")
	input := StyleNormal.Render(m.palette.query) + StyleCursor.Render(" ")
	hint := StyleDim.Render("↑/↓ navigate   enter  run   esc  close")

	var rows []string
	rows = append(rows, "", label, input, "")

	if len(filtered) == 0 {
		rows = append(rows, StyleDim.Render("  no matching commands"))
	} else {
		start := 0
		if m.palette.cursor >= maxVisible {
			start = m.palette.cursor - maxVisible + 1
		}
		end := start + maxVisible
		if end > len(filtered) {
			end = len(filtered)
		}
		innerW := modalW - 4
		for i := start; i < end; i++ {
			c := filtered[i]
			line := fmt.Sprintf("  %s  %s", c.icon, c.label)
			if i == m.palette.cursor {
				rows = append(rows, StyleCursor.Width(innerW).Render(line))
			} else {
				rows = append(rows, StyleNormal.MaxWidth(innerW).Render(line))
			}
		}
	}

	rows = append(rows, "", hint, "")
	box := StylePaneActive.Width(modalW).Render(strings.Join(rows, "\n"))

	bg := m.renderNormal()
	out := overlayCenter(bg, box, m.width, m.height)
	if kitty.IsSupported() {
		out += kitty.ClearAll()
	}
	return out
}

func (m Model) updateGoToModal(msg tea.KeyMsg) (Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEnter:
		target := filepath.Clean(m.goTo.query)
		m.goTo = goToModal{}
		info, err := os.Stat(target)
		if err != nil || !info.IsDir() {
			m.statusMsg = fmt.Sprintf("invalid path: %s", target)
			break
		}
		if m.focus == focusSplit {
			m.cwd2 = target
			m.cursor2 = 0
			m.offset2 = 0
			m.reloadSplit()
		} else {
			m.cwd = target
			m.cursor = 0
			m.offset = 0
			m.reloadMain()
		}
		return m, m.maybeLoadPreview()

	case tea.KeyEsc:
		m.goTo = goToModal{}

	case tea.KeyBackspace:
		runes := []rune(m.goTo.query)
		if len(runes) > 0 {
			m.goTo.query = string(runes[:len(runes)-1])
		}

	case tea.KeyRunes:
		m.goTo.query += string(msg.Runes)

	case tea.KeySpace:
		m.goTo.query += " "
	}
	return m, nil
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
			m.reloadMain()
			if m.err != nil {
				m.statusMsg = fmt.Sprintf("error: %v", m.err)
				m.err = nil
				m.cwd = filepath.Dir(m.cwd)
				m.reloadMainQuiet()
			}
		} else if appfs.IsImage(entry.Name) {
			if !kitty.IsSupported() {
				m.statusMsg = "image preview requires kitty terminal"
			} else {
				return m, m.openImageModal(entry.Path)
			}
		} else if appfs.IsAudio(entry.Name) {
			return m, m.openAudioModal(entry.Path)
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
	if m.focus == focusSplit {
		return m.updateSplitPane(msg)
	}
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
			m.reloadMain()
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
			m.reloadMain()
			if m.err != nil {
				m.statusMsg = fmt.Sprintf("error: %v", m.err)
				m.err = nil
				m.cwd = filepath.Dir(m.cwd)
				m.reloadMainQuiet()
			}
		} else if appfs.IsImage(entry.Name) {
			if !kitty.IsSupported() {
				m.statusMsg = "image preview requires kitty terminal"
			} else {
				return m, m.openImageModal(entry.Path)
			}
		} else if appfs.IsAudio(entry.Name) {
			return m, m.openAudioModal(entry.Path)
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
			m.reloadMain()
		}

	case key.Matches(msg, keyMap.ToggleHidden):
		m.showHidden = !m.showHidden
		m.cursor = 0
		m.offset = 0
		m.reloadMain()
		if m.showSplit {
			m.cursor2 = 0
			m.offset2 = 0
			m.reloadSplit()
		}
		m.saveUIPrefs()

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

	case key.Matches(msg, keyMap.MarkSelect):
		if len(visible) > 0 {
			e := visible[m.cursor]
			if m.selectedPaths == nil {
				m.selectedPaths = make(map[string]bool)
			}
			if m.selectedPaths[e.Path] {
				delete(m.selectedPaths, e.Path)
			} else {
				m.selectedPaths[e.Path] = true
				m.lastSelectedPath = e.Path
			}
		}

	case key.Matches(msg, keyMap.MarkSelectRange):
		if len(visible) > 0 {
			if m.selectedPaths == nil {
				m.selectedPaths = make(map[string]bool)
			}
			cur := visible[m.cursor]
			lastIdx := -1
			for i, e := range visible {
				if e.Path == m.lastSelectedPath {
					lastIdx = i
					break
				}
			}
			if lastIdx == -1 {
				m.selectedPaths[cur.Path] = true
				m.lastSelectedPath = cur.Path
			} else {
				start, end := lastIdx, m.cursor
				if start > end {
					start, end = end, start
				}
				for i := start; i <= end; i++ {
					m.selectedPaths[visible[i].Path] = true
				}
			}
		}
	}
	return m, nil
}

func (m Model) updateSplitPane(msg tea.KeyMsg) (Model, tea.Cmd) {
	listH := m.listHeight()

	switch {
	case key.Matches(msg, keyMap.Up):
		if m.cursor2 > 0 {
			m.cursor2--
			if m.cursor2 < m.offset2 {
				m.offset2 = m.cursor2
			}
		}

	case key.Matches(msg, keyMap.Down):
		if m.cursor2 < len(m.entries2)-1 {
			m.cursor2++
			if m.cursor2 >= m.offset2+listH {
				m.offset2 = m.cursor2 - listH + 1
			}
		}

	case key.Matches(msg, keyMap.Left):
		parent := filepath.Dir(m.cwd2)
		if parent != m.cwd2 {
			prevDir := m.cwd2
			m.cwd2 = parent
			m.cursor2 = 0
			m.offset2 = 0
			m.reloadSplit()
			prevName := filepath.Base(prevDir)
			for i, e := range m.entries2 {
				if e.Name == prevName {
					m.cursor2 = i
					if m.cursor2 >= listH {
						m.offset2 = m.cursor2 - listH/2
					}
					break
				}
			}
		}

	case key.Matches(msg, keyMap.Right):
		if len(m.entries2) == 0 {
			break
		}
		entry := m.entries2[m.cursor2]
		if entry.IsDir {
			m.cwd2 = entry.Path
			m.cursor2 = 0
			m.offset2 = 0
			m.reloadSplit()
		} else if appfs.IsImage(entry.Name) {
			if !kitty.IsSupported() {
				m.statusMsg = "image preview requires kitty terminal"
			} else {
				return m, m.openImageModal(entry.Path)
			}
		} else if appfs.IsAudio(entry.Name) {
			return m, m.openAudioModal(entry.Path)
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
			m.cwd2 = home
			m.cursor2 = 0
			m.offset2 = 0
			m.reloadSplit()
		}

	case key.Matches(msg, keyMap.ToggleHidden):
		m.showHidden = !m.showHidden
		m.cursor = 0
		m.offset = 0
		m.cursor2 = 0
		m.offset2 = 0
		m.reloadMainQuiet()
		m.reloadSplit()
		m.saveUIPrefs()

	case key.Matches(msg, keyMap.ToggleFavorite):
		if len(m.entries2) > 0 {
			e := m.entries2[m.cursor2]
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

	case key.Matches(msg, keyMap.MarkSelect):
		if len(m.entries2) > 0 {
			e := m.entries2[m.cursor2]
			if m.selected2Paths == nil {
				m.selected2Paths = make(map[string]bool)
			}
			if m.selected2Paths[e.Path] {
				delete(m.selected2Paths, e.Path)
			} else {
				m.selected2Paths[e.Path] = true
				m.lastSelected2Path = e.Path
			}
		}

	case key.Matches(msg, keyMap.MarkSelectRange):
		if len(m.entries2) > 0 {
			if m.selected2Paths == nil {
				m.selected2Paths = make(map[string]bool)
			}
			cur := m.entries2[m.cursor2]
			lastIdx := -1
			for i, e := range m.entries2 {
				if e.Path == m.lastSelected2Path {
					lastIdx = i
					break
				}
			}
			if lastIdx == -1 {
				m.selected2Paths[cur.Path] = true
				m.lastSelected2Path = cur.Path
			} else {
				start, end := lastIdx, m.cursor2
				if start > end {
					start, end = end, start
				}
				for i := start; i <= end; i++ {
					m.selected2Paths[m.entries2[i].Path] = true
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

	if m.goTo.open {
		return m.renderGoToModal()
	}

	if m.newItem.open {
		return m.renderNewItemModal()
	}

	if m.renameModal.open {
		return m.renderRenameModal()
	}

	if m.palette.open {
		return m.renderPalette()
	}

	if m.macroManager.open {
		return m.renderMacroManager()
	}

	if m.macroRunner.open {
		return m.renderMacroModal()
	}

	if m.imageModal.open {
		return m.renderImageModal()
	}

	if m.audioPlayer.open {
		return m.renderAudioModal()
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

// overlayModal renders box centered over the normal background view.
// It also clears kitty images. Use for all popup modals.
func (m Model) overlayModal(box string) string {
	bg := m.renderNormal()
	out := overlayCenter(bg, box, m.width, m.height)
	if kitty.IsSupported() {
		out += kitty.ClearAll()
	}
	return out
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
	out := overlayCenter(m.renderNormal(), box, m.width, m.height)

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

func (m Model) renderAudioModal() string {
	a := m.audioPlayer
	const modalW = 60
	modalH := 9

	innerW := modalW - 4 // border(2) + padding(2)

	// Title line.
	title := StyleSelected.Bold(true).Render(" 󰎇  " + a.name)

	// Time display.
	elMin := int(a.elapsed) / 60
	elSec := int(a.elapsed) % 60
	timeStr := fmt.Sprintf("%02d:%02d", elMin, elSec)
	var durStr string
	if a.dur > 0 {
		dMin := int(a.dur) / 60
		dSec := int(a.dur) % 60
		durStr = fmt.Sprintf(" / %02d:%02d", dMin, dSec)
	}
	timeLine := StyleDim.Render(timeStr) + StyleDim.Render(durStr)

	// Progress bar.
	barW := innerW - 4
	if barW < 4 {
		barW = 4
	}
	var progressBar string
	if a.dur > 0 {
		filled := int(float64(barW) * a.elapsed / a.dur)
		if filled > barW {
			filled = barW
		}
		progressBar = StyleSelected.Render(strings.Repeat("█", filled)) +
			StyleDim.Render(strings.Repeat("░", barW-filled))
	} else {
		// Spinning indicator when duration is unknown.
		spinners := []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}
		spin := spinners[int(a.elapsed)%len(spinners)]
		progressBar = StyleDim.Render(spin + " " + strings.Repeat("─", barW-2))
	}

	// Status icon.
	var statusIcon string
	if a.proc == nil {
		statusIcon = StyleDim.Render("󰙧  finished")
	} else if a.paused {
		statusIcon = StyleDim.Render("  paused")
	} else {
		statusIcon = StyleSelected.Render("  playing")
	}

	hint := StyleDim.Render("  ←/→ seek 1s    space/p  pause    q  close")

	lines := []string{
		title,
		"",
		"  " + progressBar,
		"  " + timeLine + "   " + statusIcon,
		"",
		hint,
	}

	box := StylePaneActive.Width(innerW).Height(modalH).Render(strings.Join(lines, "\n"))
	return m.overlayModal(box)
}

func (m Model) renderNormal() string {
	listH := m.listHeight()
	listW := m.fileListWidth()

	hn, _ := os.Hostname()

	titleLine := StyleTitle.Render(" "+hn+" ") +
		StyleDim.Render(" › ") +
		StyleNormal.Render(m.cwd)
	if m.showSplit {
		titleLine += StyleDim.Render("  |  ") + StyleNormal.Render(m.cwd2)
	}

	cols := []string{m.renderSidebar(listH), m.renderFileList(listH, listW)}
	if m.showSplit {
		cols = append(cols, m.renderFilePaneAt(listH, listW, m.entries2, m.cursor2, m.offset2, m.focus == focusSplit, m.selected2Paths, m.gitStatus2))
	}
	if m.showDetails && m.showGitPane {
		// Each pane adds 2 lines of border overhead (top + bottom).
		// Total inner content = listH - 2 border lines.
		available := listH - 2
		gitH := available / 3
		if gitH < 6 {
			gitH = 6
		}
		detailH := available - gitH
		if detailH < 6 {
			detailH = 6
			gitH = available - detailH
		}
		rightCol := lipgloss.JoinVertical(lipgloss.Left,
			m.renderDetails(detailH),
			m.renderGitPane(gitH),
		)
		cols = append(cols, rightCol)
	} else if m.showDetails {
		cols = append(cols, m.renderDetails(listH))
	} else if m.showGitPane {
		cols = append(cols, m.renderGitPane(listH))
	}
	body := lipgloss.JoinHorizontal(lipgloss.Top, cols...)

	statusLine := StyleStatusBar.Width(m.width).Render(m.buildStatus())

	if m.search.active {
		return titleLine + "\n" + body + "\n" + m.renderSearchBar() + "\n" + statusLine
	}
	return titleLine + "\n" + body + "\n" + statusLine
}

func (m Model) renderDeleteModal() string {
	target := m.deleteConfirm.target
	multi := m.deleteConfirm.multiTargets
	const modalW = 46
	var warning, nameLine string
	if len(multi) > 1 {
		warning = StyleDim.Render(fmt.Sprintf("Delete %d items permanently?", len(multi)))
		nameLine = StyleSelected.Bold(true).Render(fmt.Sprintf("%d selected items", len(multi)))
	} else {
		kind := "file"
		if target.IsDir {
			kind = "directory"
		}
		warning = StyleDim.Render(fmt.Sprintf("Delete this %s permanently?", kind))
		nameStyle := StyleSelected.Bold(true)
		nameLine = nameStyle.Render(target.Name)
		if target.IsDir {
			nameLine = StyleDir.Bold(true).Render(target.Name + "/")
		}
	}
	confirm := StyleNormal.Render("  ") + StyleCursor.Render(" y ") +
		StyleNormal.Render(" confirm   ") +
		StyleDim.Render("any other key") + StyleNormal.Render(" cancel")

	content := strings.Join([]string{"", warning, nameLine, "", confirm, ""}, "\n")
	box := StylePaneActive.Width(modalW).Render(content)
	return m.overlayModal(box)
}

func (m Model) renderNewItemModal() string {
	const modalW = 50
	var label string
	if m.newItem.kind == newItemDir {
		label = StyleDim.Render("New folder name:")
	} else {
		label = StyleDim.Render("New file name:")
	}
	input := StyleNormal.Render(m.newItem.query) + StyleCursor.Render(" ")
	hint := StyleDim.Render("enter  confirm   esc  cancel")
	var lines []string
	lines = append(lines, "", label, input, "")
	if m.newItem.err != "" {
		lines = append(lines, StyleSelected.Render("  "+m.newItem.err))
	}
	lines = append(lines, hint, "")
	content := strings.Join(lines, "\n")
	box := StylePaneActive.Width(modalW).Render(content)
	return m.overlayModal(box)
}

func (m Model) renderGoToModal() string {
	const modalW = 60
	label := StyleDim.Render("Go to path:")
	input := StyleNormal.Render(m.goTo.query) + StyleCursor.Render(" ")
	hint := StyleDim.Render("enter  confirm   esc  cancel")
	content := strings.Join([]string{"", label, input, "", hint, ""}, "\n")
	box := StylePaneActive.Width(modalW).Render(content)
	return m.overlayModal(box)
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

	return m.overlayModal(content)
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
		rows = append(rows, "") // spacer between sections
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

		height--
	}

	for len(rows) < height {
		rows = append(rows, "")
	}
	rows = rows[:height]

	paneStyle := StylePane
	if m.focus == focusSidebar {
		paneStyle = StylePaneActive
	}
	return paneStyle.Width(sidebarWidth - 2).Height(height).Render(strings.Join(rows, "\n"))
}

func (m Model) renderFilePaneAt(height, width int, entries []appfs.Entry, cursor, offset int, isActive bool, selectedPaths map[string]bool, gitStatus map[string]appgit.FileStatus) string {
	innerW := width - 4

	var rows []string
	for i := offset; i < len(entries) && i < offset+height; i++ {
		e := entries[i]
		name := e.Name
		if e.IsDir {
			name += "/"
		}

		// Git status indicator.
		var gitPrefix string
		if gs, ok := gitStatus[e.Name]; ok && gs != appgit.StatusNone {
			gitPrefix = gs.Label() + " "
		}

		var style lipgloss.Style
		switch {
		case i == cursor:
			style = StyleCursor
		case selectedPaths[e.Path]:
			style = StyleSelected
		case e.IsDir:
			style = StyleDir
		case appfs.IsHidden(e.Name):
			style = StyleHidden
		default:
			style = StyleNormal
		}

		displayName := gitPrefix + name

		var rendered string
		if i == cursor {
			rendered = style.Width(innerW).Render(displayName)
		} else if gitPrefix != "" {
			gs := gitStatus[e.Name]
			var gsStyle lipgloss.Style
			switch gs {
			case appgit.StatusModified:
				gsStyle = StyleGitModified
			case appgit.StatusStaged:
				gsStyle = StyleGitStaged
			case appgit.StatusUntracked:
				gsStyle = StyleGitUntracked
			case appgit.StatusAdded:
				gsStyle = StyleGitAdded
			case appgit.StatusDeleted:
				gsStyle = StyleGitDeleted
			case appgit.StatusConflict:
				gsStyle = StyleGitConflict
			case appgit.StatusRenamed:
				gsStyle = StyleGitRenamed
			default:
				gsStyle = StyleDim
			}
			rendered = gsStyle.Render(gitPrefix) + style.MaxWidth(innerW-2).Render(name)
		} else {
			rendered = style.MaxWidth(innerW).Render(name)
		}
		rows = append(rows, rendered)
	}

	for len(rows) < height {
		rows = append(rows, "")
	}

	paneStyle := StylePane
	if isActive {
		paneStyle = StylePaneActive
	}
	return paneStyle.Width(width - 2).Height(height).Render(strings.Join(rows, "\n"))
}

func (m Model) renderFileList(height, width int) string {
	return m.renderFilePaneAt(height, width, m.visibleEntries(), m.cursor, m.offset, m.focus == focusList, m.selectedPaths, m.gitStatus)
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
	// 1-based column: past sidebar + all file-list panes, then past left border+padding of details pane.
	paneCount := 1
	if m.showSplit {
		paneCount = 2
	}
	col := sidebarWidth + listW*paneCount + 3
	row := 3 // title(1) + pane top border(1) + first content row(1)
	maxCols := detailsWidth - 4
	c, r := calcPreviewSize(m.previewW, m.previewH, maxCols, previewCellRows)
	return kitty.Place(m.previewEncoded, col, row, c, r, 1)
}

func (m Model) renderDetails(height int) string {
	innerW := detailsWidth - 4
	var rows []string

	visible := m.activeVisible()
	cursor := m.activeCursor()
	if len(visible) == 0 || cursor >= len(visible) {
		for len(rows) < height {
			rows = append(rows, "")
		}
		return StylePane.Width(detailsWidth - 2).Height(height).Render(strings.Join(rows, "\n"))
	}

	e := visible[cursor]

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

func (m Model) renderGitPane(height int) string {
	innerW := detailsWidth - 4
	var rows []string

	label := func(s string) string {
		return StyleDetailsLabel.Render(s)
	}
	val := func(s string) string {
		return StyleDetailsValue.MaxWidth(innerW).Render(s)
	}

	gitRoot := m.gitRoot
	gitStatus := m.gitStatus
	if m.focus == focusSplit {
		gitRoot = m.gitRoot2
		gitStatus = m.gitStatus2
	}

	if gitRoot == "" {
		rows = append(rows, StyleDim.Render("not a git repo"))
	} else {
		rows = append(rows, label("BRANCH"))
		branch := appgit.Branch(gitRoot)
		if branch == "" {
			branch = "(detached)"
		}
		rows = append(rows, val(" "+branch))
		rows = append(rows, "")

		// Status of selected entry
		visible := m.activeVisible()
		cursor := m.activeCursor()
		if len(visible) > 0 && cursor >= 0 && cursor < len(visible) {
			e := visible[cursor]
			rows = append(rows, label("STATUS"))
			if gs, ok := gitStatus[e.Name]; ok && gs != appgit.StatusNone {
				rows = append(rows, val(gitStatusLabel(gs)))
			} else {
				rows = append(rows, val("clean"))
			}
			rows = append(rows, "")
		}

		rows = append(rows, label("ROOT"))
		rows = append(rows, val(gitRoot))
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

func gitStatusLabel(s appgit.FileStatus) string {
	switch s {
	case appgit.StatusModified:
		return "modified"
	case appgit.StatusStaged:
		return "staged"
	case appgit.StatusUntracked:
		return "untracked"
	case appgit.StatusAdded:
		return "added"
	case appgit.StatusDeleted:
		return "deleted"
	case appgit.StatusRenamed:
		return "renamed"
	case appgit.StatusConflict:
		return "conflict"
	case appgit.StatusIgnored:
		return "ignored"
	default:
		return "clean"
	}
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

	active := m.activeVisible()
	total := len(active)
	pos := 0
	if total > 0 {
		pos = m.activeCursor() + 1
	}

	clip := ""
	if len(m.clipboard.items) > 0 {
		switch m.clipboard.op {
		case clipCopy:
			if len(m.clipboard.items) == 1 {
				clip = fmt.Sprintf("  [copy: %s]", m.clipboard.items[0].name)
			} else {
				clip = fmt.Sprintf("  [copy: %d items]", len(m.clipboard.items))
			}
		case clipCut:
			if len(m.clipboard.items) == 1 {
				clip = fmt.Sprintf("  [cut: %s]", m.clipboard.items[0].name)
			} else {
				clip = fmt.Sprintf("  [cut: %d items]", len(m.clipboard.items))
			}
		}
	}

	selCount := len(m.selectedPaths)
	if m.focus == focusSplit {
		selCount = len(m.selected2Paths)
	}
	sel := ""
	if selCount > 0 {
		sel = fmt.Sprintf("  [%d selected]", selCount)
	}

	parts := []string{
		fmt.Sprintf("%d/%d", pos, total),
		"[.] Menu",
		"[w] Sidebar",
		"[e] Panes",
		"[s] Split",
		"[H] Hidden",
		"[:] Go to",
		"[q] Quit",
	}

	return " " + strings.Join(parts, " | ") + clip + sel
}

// ---------------------------------------------------------------------------
// Macro runner
// ---------------------------------------------------------------------------

// shellQuote wraps s in single quotes, escaping any single quotes within.
// This makes the value safe to embed in a sh -c command string.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}

// openMacroModal builds the list of applicable macros and opens the modal.
// If no macros apply it sets a status message instead.
func (m *Model) openMacroModal() {
	allMacros, _ := appconfig.LoadMacros()
	if len(allMacros) == 0 {
		m.statusMsg = "no macros configured — open 'Manage macros' from the palette"
		return
	}

	visible := m.activeVisible()
	cursor := m.activeCursor()

	var currentExt string
	var isDir bool
	if len(visible) > 0 && cursor < len(visible) {
		e := visible[cursor]
		isDir = e.IsDir
		if !e.IsDir {
			currentExt = strings.ToLower(filepath.Ext(e.Name))
		}
	}

	var applicable []appconfig.Macro
	for _, macro := range allMacros {
		if len(macro.Filter) == 0 {
			applicable = append(applicable, macro)
			continue
		}
		if isDir {
			continue
		}
		for _, ext := range macro.Filter {
			if strings.ToLower(ext) == currentExt {
				applicable = append(applicable, macro)
				break
			}
		}
	}

	if len(applicable) == 0 {
		m.statusMsg = "no macros for this file type"
		return
	}

	m.macroRunner = macroModal{open: true, macros: applicable}
	m.statusMsg = ""
}

// updateMacroModal handles key events while the macro modal is open.
func (m Model) updateMacroModal(msg tea.KeyMsg) (Model, tea.Cmd) {
	if m.macroRunner.inputMode {
		return m.updateMacroInput(msg)
	}

	switch msg.Type {
	case tea.KeyEnter:
		if len(m.macroRunner.macros) > 0 {
			macro := m.macroRunner.macros[m.macroRunner.cursor]
			if strings.Contains(macro.Command, "$INPUT") {
				m.macroRunner.inputMode = true
				m.macroRunner.inputQuery = ""
				m.macroRunner.inputMacro = macro
				return m, nil
			}
			m.macroRunner = macroModal{}
			return m.runMacro(macro, "")
		}
		m.macroRunner = macroModal{}

	case tea.KeyEsc:
		m.macroRunner = macroModal{}

	case tea.KeyUp:
		if m.macroRunner.cursor > 0 {
			m.macroRunner.cursor--
		}

	case tea.KeyDown:
		if m.macroRunner.cursor < len(m.macroRunner.macros)-1 {
			m.macroRunner.cursor++
		}
	}
	return m, nil
}

// updateMacroInput handles key events in the $INPUT prompt phase.
func (m Model) updateMacroInput(msg tea.KeyMsg) (Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEnter:
		macro := m.macroRunner.inputMacro
		input := m.macroRunner.inputQuery
		m.macroRunner = macroModal{}
		return m.runMacro(macro, input)

	case tea.KeyEsc:
		m.macroRunner = macroModal{}

	case tea.KeyBackspace:
		runes := []rune(m.macroRunner.inputQuery)
		if len(runes) > 0 {
			m.macroRunner.inputQuery = string(runes[:len(runes)-1])
		}

	case tea.KeyRunes:
		m.macroRunner.inputQuery += string(msg.Runes)

	case tea.KeySpace:
		m.macroRunner.inputQuery += " "
	}
	return m, nil
}

// runMacro expands variables in macro.Command and executes it.
// If macro.Background is true the command is started without suspending the TUI.
func (m Model) runMacro(macro appconfig.Macro, input string) (Model, tea.Cmd) {
	visible := m.activeVisible()
	cursor := m.activeCursor()
	sel := m.activeSelectedPaths()

	// Collect target paths: all marked files, or just the cursor entry.
	var files []string
	for _, e := range visible {
		if sel[e.Path] {
			files = append(files, e.Path)
		}
	}
	if len(files) == 0 && len(visible) > 0 && cursor < len(visible) {
		files = []string{visible[cursor].Path}
	}

	var primaryFile, primaryName string
	if len(files) > 0 {
		primaryFile = files[0]
		primaryName = filepath.Base(primaryFile)
	}

	// Build a space-separated, shell-quoted list of all target paths.
	quotedFiles := make([]string, len(files))
	for i, f := range files {
		quotedFiles[i] = shellQuote(f)
	}
	filesStr := strings.Join(quotedFiles, " ")

	// Substitute variables. $FILES before $FILE to avoid partial replacement.
	command := macro.Command
	command = strings.ReplaceAll(command, "$FILES", filesStr)
	command = strings.ReplaceAll(command, "$FILE", shellQuote(primaryFile))
	command = strings.ReplaceAll(command, "$NAME", shellQuote(primaryName))
	command = strings.ReplaceAll(command, "$DIR", shellQuote(m.activeCwd()))
	command = strings.ReplaceAll(command, "$INPUT", shellQuote(input))

	c := exec.Command("sh", "-c", command)
	c.Dir = m.activeCwd()

	if macro.Background {
		if err := c.Start(); err != nil {
			m.statusMsg = fmt.Sprintf("macro error: %v", err)
		} else {
			m.statusMsg = fmt.Sprintf("running  %s", macro.Name)
			// Reap the child to avoid zombies.
			go func() { _ = c.Wait() }()
		}
		return m, nil
	}

	return m, tea.ExecProcess(c, func(err error) tea.Msg {
		return macroClosedMsg{err: err}
	})
}

// renderMacroModal renders the macro selection list or the $INPUT prompt.
func (m Model) renderMacroModal() string {
	if m.macroRunner.inputMode {
		return m.renderMacroInputModal()
	}

	const modalW = 54
	const maxVisible = 10

	macros := m.macroRunner.macros
	label := StyleDim.Render("Run macro  [,]")
	hint := StyleDim.Render("↑/↓ navigate   enter  run   esc  close")

	var rows []string
	rows = append(rows, "", label, "")

	if len(macros) == 0 {
		rows = append(rows, StyleDim.Render("  no macros available"))
	} else {
		start := 0
		if m.macroRunner.cursor >= maxVisible {
			start = m.macroRunner.cursor - maxVisible + 1
		}
		end := start + maxVisible
		if end > len(macros) {
			end = len(macros)
		}
		innerW := modalW - 4
		for i := start; i < end; i++ {
			macro := macros[i]
			var filterSuffix string
			if len(macro.Filter) > 0 {
				filterSuffix = "  " + StyleDim.Render(strings.Join(macro.Filter, " "))
			}
			name := fmt.Sprintf("  󰆍  %s", macro.Name)
			if i == m.macroRunner.cursor {
				rows = append(rows, StyleCursor.Width(innerW).Render(name)+filterSuffix)
			} else {
				rows = append(rows, StyleNormal.MaxWidth(innerW).Render(name+filterSuffix))
			}
		}
	}

	rows = append(rows, "", hint, "")
	box := StylePaneActive.Width(modalW).Render(strings.Join(rows, "\n"))
	bg := m.renderNormal()
	out := overlayCenter(bg, box, m.width, m.height)
	if kitty.IsSupported() {
		out += kitty.ClearAll()
	}
	return out
}

// renderMacroInputModal renders the text-input prompt for $INPUT macros.
func (m Model) renderMacroInputModal() string {
	const modalW = 54
	macro := m.macroRunner.inputMacro
	label := StyleDim.Render("Input for: " + macro.Name)
	input := StyleNormal.Render(m.macroRunner.inputQuery) + StyleCursor.Render(" ")
	hint := StyleDim.Render("enter  run   esc  cancel")
	content := strings.Join([]string{"", label, input, "", hint, ""}, "\n")
	box := StylePaneActive.Width(modalW).Render(content)
	return m.overlayModal(box)
}

// ---------------------------------------------------------------------------
// Macro manager – open / save helpers
// ---------------------------------------------------------------------------

// openMacroManagerModal loads macros from macros.json and opens the CRUD manager.
func (m *Model) openMacroManagerModal() {
	loaded, _ := appconfig.LoadMacros()
	macros := make([]appconfig.Macro, len(loaded))
	copy(macros, loaded)
	m.macroManager = macroManagerModal{
		open:   true,
		macros: macros,
	}
	m.statusMsg = ""
}

// saveMacrosFromManager persists the manager's macro list to macros.json.
func (m *Model) saveMacrosFromManager() {
	_ = appconfig.SaveMacros(m.macroManager.macros)
}

// startEditMacro switches the manager into edit mode for macros[idx].
func (m *Model) startEditMacro(idx int) {
	mac := m.macroManager.macros[idx]
	m.macroManager.editing = true
	m.macroManager.isNew = false
	m.macroManager.editIdx = idx
	m.macroManager.fieldCursor = 0
	m.macroManager.editName = mac.Name
	m.macroManager.editCommand = mac.Command
	m.macroManager.editFilter = strings.Join(mac.Filter, ", ")
	m.macroManager.editBackground = mac.Background
	m.macroManager.err = ""
}

// startNewMacro switches the manager into create mode with blank fields.
func (m *Model) startNewMacro() {
	m.macroManager.editing = true
	m.macroManager.isNew = true
	m.macroManager.editIdx = -1
	m.macroManager.fieldCursor = 0
	m.macroManager.editName = ""
	m.macroManager.editCommand = ""
	m.macroManager.editFilter = ""
	m.macroManager.editBackground = false
	m.macroManager.err = ""
}

// ---------------------------------------------------------------------------
// Macro manager – update
// ---------------------------------------------------------------------------

func (m Model) updateMacroManager(msg tea.KeyMsg) (Model, tea.Cmd) {
	if m.macroManager.editing {
		return m.updateMacroManagerForm(msg)
	}
	return m.updateMacroManagerList(msg)
}

func (m Model) updateMacroManagerList(msg tea.KeyMsg) (Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEsc:
		m.macroManager.open = false

	case tea.KeyUp:
		if m.macroManager.cursor > 0 {
			m.macroManager.cursor--
		}

	case tea.KeyDown:
		if m.macroManager.cursor < len(m.macroManager.macros)-1 {
			m.macroManager.cursor++
		}

	case tea.KeyEnter:
		if len(m.macroManager.macros) > 0 && m.macroManager.cursor < len(m.macroManager.macros) {
			m.startEditMacro(m.macroManager.cursor)
		}

	case tea.KeyDelete:
		if len(m.macroManager.macros) > 0 && m.macroManager.cursor < len(m.macroManager.macros) {
			idx := m.macroManager.cursor
			m.macroManager.macros = append(
				append([]appconfig.Macro{}, m.macroManager.macros[:idx]...),
				m.macroManager.macros[idx+1:]...,
			)
			if m.macroManager.cursor >= len(m.macroManager.macros) && m.macroManager.cursor > 0 {
				m.macroManager.cursor--
			}
			m.saveMacrosFromManager()
		}

	case tea.KeyRunes:
		switch string(msg.Runes) {
		case "e":
			if len(m.macroManager.macros) > 0 && m.macroManager.cursor < len(m.macroManager.macros) {
				m.startEditMacro(m.macroManager.cursor)
			}
		case "n":
			m.startNewMacro()
		case "d":
			if len(m.macroManager.macros) > 0 && m.macroManager.cursor < len(m.macroManager.macros) {
				idx := m.macroManager.cursor
				m.macroManager.macros = append(
					append([]appconfig.Macro{}, m.macroManager.macros[:idx]...),
					m.macroManager.macros[idx+1:]...,
				)
				if m.macroManager.cursor >= len(m.macroManager.macros) && m.macroManager.cursor > 0 {
					m.macroManager.cursor--
				}
				m.saveMacrosFromManager()
			}
		}
	}
	return m, nil
}

func (m Model) updateMacroManagerForm(msg tea.KeyMsg) (Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEsc:
		m.macroManager.editing = false
		m.macroManager.err = ""

	case tea.KeyUp:
		if m.macroManager.fieldCursor > 0 {
			m.macroManager.fieldCursor--
		}

	case tea.KeyDown, tea.KeyTab:
		if m.macroManager.fieldCursor < 3 {
			m.macroManager.fieldCursor++
		}

	case tea.KeyShiftTab:
		if m.macroManager.fieldCursor > 0 {
			m.macroManager.fieldCursor--
		}

	case tea.KeyEnter:
		return m.saveMacroFromForm()

	case tea.KeyBackspace:
		m.macroManager.deleteFieldChar()

	case tea.KeySpace:
		if m.macroManager.fieldCursor == 3 {
			m.macroManager.editBackground = !m.macroManager.editBackground
		} else {
			m.macroManager.editField(" ")
		}

	case tea.KeyRunes:
		m.macroManager.editField(string(msg.Runes))
	}
	return m, nil
}

// saveMacroFromForm validates form fields and saves the macro.
func (m Model) saveMacroFromForm() (Model, tea.Cmd) {
	name := strings.TrimSpace(m.macroManager.editName)
	command := strings.TrimSpace(m.macroManager.editCommand)

	if name == "" {
		m.macroManager.err = "name is required"
		m.macroManager.fieldCursor = 0
		return m, nil
	}
	if command == "" {
		m.macroManager.err = "command is required"
		m.macroManager.fieldCursor = 1
		return m, nil
	}

	// Parse and normalise filter extensions.
	var filter []string
	for _, raw := range strings.Split(m.macroManager.editFilter, ",") {
		ext := strings.TrimSpace(raw)
		if ext == "" {
			continue
		}
		if !strings.HasPrefix(ext, ".") {
			ext = "." + ext
		}
		filter = append(filter, strings.ToLower(ext))
	}

	macro := appconfig.Macro{
		Name:       name,
		Command:    command,
		Filter:     filter,
		Background: m.macroManager.editBackground,
	}
	if m.macroManager.isNew {
		m.macroManager.macros = append(m.macroManager.macros, macro)
		m.macroManager.cursor = len(m.macroManager.macros) - 1
	} else {
		m.macroManager.macros[m.macroManager.editIdx] = macro
		m.macroManager.cursor = m.macroManager.editIdx
	}
	m.macroManager.editing = false
	m.macroManager.err = ""
	m.saveMacrosFromManager()
	return m, nil
}

// ---------------------------------------------------------------------------
// Macro manager – render
// ---------------------------------------------------------------------------

func (m Model) renderMacroManager() string {
	if m.macroManager.editing {
		return m.renderMacroManagerForm()
	}
	return m.renderMacroManagerList()
}

func (m Model) renderMacroManagerList() string {
	const modalW = 64
	const maxVisible = 10

	macros := m.macroManager.macros
	count := fmt.Sprintf("(%d)", len(macros))
	header := StyleTitle.Render("Macros") + "  " + StyleDim.Render(count)
	hint := StyleDim.Render("[n] New  [e/↵] Edit  [d/del] Delete  [esc] Close")

	var rows []string
	rows = append(rows, "", header, "")

	if len(macros) == 0 {
		rows = append(rows, StyleDim.Render("  No macros yet — press [n] to create one."), "")
	} else {
		start := 0
		if m.macroManager.cursor >= maxVisible {
			start = m.macroManager.cursor - maxVisible + 1
		}
		end := start + maxVisible
		if end > len(macros) {
			end = len(macros)
		}
		innerW := modalW - 4
		for i := start; i < end; i++ {
			mac := macros[i]
			filterStr := strings.Join(mac.Filter, " ")
			name := "  󰆍  " + mac.Name
			if filterStr != "" {
				name += "  " + filterStr
			}
			if mac.Background {
				name += "  bg"
			}
			if i == m.macroManager.cursor {
				rows = append(rows, StyleCursor.Width(innerW).Render(name))
			} else {
				rows = append(rows, StyleNormal.MaxWidth(innerW).Render(name))
			}
		}
		rows = append(rows, "")
	}

	rows = append(rows, hint, "")
	box := StylePaneActive.Width(modalW).Render(strings.Join(rows, "\n"))
	bg := m.renderNormal()
	out := overlayCenter(bg, box, m.width, m.height)
	if kitty.IsSupported() {
		out += kitty.ClearAll()
	}
	return out
}

func (m Model) renderMacroManagerForm() string {
	const modalW = 64
	mm := m.macroManager
	innerW := modalW - 4

	var titleStr string
	if mm.isNew {
		titleStr = "New Macro"
	} else {
		titleStr = "Edit Macro"
	}
	title := StyleTitle.Render(titleStr)

	renderTextField := func(label, value string, fieldIdx int) string {
		active := mm.fieldCursor == fieldIdx
		var lbl string
		if active {
			lbl = StyleSelected.Render(label)
		} else {
			lbl = StyleDetailsLabel.Render(label)
		}
		var valRender string
		if active {
			valRender = StyleNormal.Render("  "+value) + StyleCursor.Render(" ")
		} else {
			disp := value
			if disp == "" {
				disp = "(empty)"
			}
			valRender = StyleDim.Render("  " + disp)
		}
		return lbl + "\n" + valRender
	}

	renderBoolField := func(label string, val bool, fieldIdx int) string {
		active := mm.fieldCursor == fieldIdx
		var lbl string
		if active {
			lbl = StyleSelected.Render(label)
		} else {
			lbl = StyleDetailsLabel.Render(label)
		}
		check := "[ ]"
		if val {
			check = "[x]"
		}
		text := "  " + check + " Run without suspending the TUI"
		var valRender string
		if active {
			valRender = StyleCursor.Width(innerW).Render(text)
		} else {
			valRender = StyleNormal.Render(text)
		}
		return lbl + "\n" + valRender
	}

	vars := StyleDim.Render("  Variables: $FILE  $FILES  $NAME  $DIR  $INPUT")
	hint := StyleDim.Render("↑/↓/tab  navigate   space  toggle bg   enter  save   esc  back")

	var rows []string
	rows = append(rows, "", title, "")
	rows = append(rows, renderTextField("NAME", mm.editName, 0))
	rows = append(rows, "")
	rows = append(rows, renderTextField("COMMAND", mm.editCommand, 1))
	rows = append(rows, "")
	rows = append(rows, renderTextField("FILTER  (comma-separated, e.g. .png, .jpg)", mm.editFilter, 2))
	rows = append(rows, "")
	rows = append(rows, renderBoolField("BACKGROUND", mm.editBackground, 3))
	rows = append(rows, "", vars, "")
	if mm.err != "" {
		rows = append(rows, StyleSelected.Render("  ⚠ "+mm.err), "")
	}
	rows = append(rows, hint, "")

	box := StylePaneActive.Width(modalW).Render(strings.Join(rows, "\n"))
	bg := m.renderNormal()
	out := overlayCenter(bg, box, m.width, m.height)
	if kitty.IsSupported() {
		out += kitty.ClearAll()
	}
	return out
}
