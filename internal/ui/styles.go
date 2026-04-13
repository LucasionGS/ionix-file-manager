package ui

import (
	appconfig "github.com/LucasionGS/ionix-file-manager/internal/config"
	"github.com/charmbracelet/lipgloss"
)

// Default palette — purple/blurple dark theme.
const (
	defaultBase      = "#e2d9f3" // pale lavender text
	defaultBasLight  = "#1a1025" // deep purple-black for light terminals
	defaultDim       = "#7c6f9e" // muted purple-gray
	defaultHighlight = "#b792f5" // bright violet accent
	defaultHigLight  = "#7c3aed" // deep violet for light terminals
	defaultDir       = "#818cf8" // indigo-blue directories
	defaultDirLight  = "#4338ca"
	defaultHidden    = "#3d3058" // very muted purple for hidden files
	defaultHidLight  = "#7c6f9e"
	defaultSelected  = "#c084fc" // bright purple for selections
	defaultSelLight  = "#9333ea"
	defaultBorder    = "#2d1f4e" // dark purple border
	defaultBorLight  = "#a78bfa"
	defaultStatusBg  = "#130d24" // near-black purple status bar
	defaultStaBgLt   = "#ede9fe"
	defaultCursorFg  = "#0d0717" // almost-black text on bright cursor
)

var (
	StyleNormal                lipgloss.Style
	StyleDir                   lipgloss.Style
	StyleHidden                lipgloss.Style
	StyleSelected              lipgloss.Style
	StyleCursor                lipgloss.Style
	StylePane                  lipgloss.Style
	StylePaneActive            lipgloss.Style
	StyleStatusBar             lipgloss.Style
	StyleDim                   lipgloss.Style
	StyleTitle                 lipgloss.Style
	StyleSidebarItem           lipgloss.Style
	StyleSidebarCursor         lipgloss.Style
	StyleSidebarCursorInactive lipgloss.Style
	StyleSidebarLabel          lipgloss.Style
	StyleDetailsLabel          lipgloss.Style
	StyleDetailsValue          lipgloss.Style
	StyleDetailsValueDir       lipgloss.Style
	StyleGitModified           lipgloss.Style
	StyleGitStaged             lipgloss.Style
	StyleGitUntracked          lipgloss.Style
	StyleGitAdded              lipgloss.Style
	StyleGitDeleted            lipgloss.Style
	StyleGitConflict           lipgloss.Style
	StyleGitRenamed            lipgloss.Style
)

func init() {
	ApplyColors(appconfig.Colors{})
}

// adaptive returns an AdaptiveColor using override for both themes when set,
// otherwise falling back to lightDefault/darkDefault.
func adaptive(override, lightDefault, darkDefault string) lipgloss.AdaptiveColor {
	if override != "" {
		return lipgloss.AdaptiveColor{Light: override, Dark: override}
	}
	return lipgloss.AdaptiveColor{Light: lightDefault, Dark: darkDefault}
}

// ApplyColors rebuilds all package-level style vars from the provided Colors.
// Call this once after loading config. Empty fields use built-in defaults.
func ApplyColors(c appconfig.Colors) {
	colorBase := adaptive(c.Base, defaultBasLight, defaultBase)
	colorDim := adaptive(c.Dim, defaultDim, defaultDim)
	colorHighlight := adaptive(c.Highlight, defaultHigLight, defaultHighlight)
	colorDir := adaptive(c.Dir, defaultDirLight, defaultDir)
	colorHidden := adaptive(c.Hidden, defaultHidLight, defaultHidden)
	colorSelected := adaptive(c.Selected, defaultSelLight, defaultSelected)
	colorBorder := adaptive(c.Border, defaultBorLight, defaultBorder)
	colorStatusBar := adaptive(c.StatusBg, defaultStaBgLt, defaultStatusBg)

	cursorFg := c.CursorFg
	if cursorFg == "" {
		cursorFg = defaultCursorFg
	}

	StyleNormal = lipgloss.NewStyle().Foreground(colorBase)

	StyleDir = lipgloss.NewStyle().
		Foreground(colorDir).
		Bold(true)

	StyleHidden = lipgloss.NewStyle().
		Foreground(colorHidden)

	StyleSelected = lipgloss.NewStyle().
		Foreground(colorSelected).
		Bold(true)

	StyleCursor = lipgloss.NewStyle().
		Background(colorHighlight).
		Foreground(lipgloss.Color(cursorFg)).
		Bold(true).
		PaddingLeft(1).
		PaddingRight(1)

	StylePane = lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(colorBorder).
		Padding(0, 1)

	StylePaneActive = lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(colorHighlight).
		Padding(0, 1)

	StyleStatusBar = lipgloss.NewStyle().
		Background(colorStatusBar).
		Foreground(colorBase).
		Padding(0, 1)

	StyleDim = lipgloss.NewStyle().Foreground(colorDim)

	StyleTitle = lipgloss.NewStyle().
		Foreground(colorHighlight).
		Bold(true)

	StyleSidebarItem = lipgloss.NewStyle().
		Foreground(colorBase).
		PaddingLeft(1)

	StyleSidebarCursor = lipgloss.NewStyle().
		Background(colorSelected).
		Foreground(lipgloss.Color(cursorFg)).
		Bold(true).
		PaddingLeft(1)

	StyleSidebarCursorInactive = lipgloss.NewStyle().
		Background(colorBorder).
		Foreground(colorBase).
		PaddingLeft(1)

	StyleSidebarLabel = lipgloss.NewStyle().
		Foreground(colorDim).
		Bold(true).
		PaddingLeft(1)

	StyleDetailsLabel = lipgloss.NewStyle().
		Foreground(colorDim).
		Bold(true)

	StyleDetailsValue = lipgloss.NewStyle().
		Foreground(colorBase)

	StyleDetailsValueDir = lipgloss.NewStyle().
		Foreground(colorDir).
		Bold(true)

	StyleGitModified = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "#b45309", Dark: "#fbbf24"})
	StyleGitStaged = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "#15803d", Dark: "#4ade80"})
	StyleGitUntracked = lipgloss.NewStyle().Foreground(colorDim)
	StyleGitAdded = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "#15803d", Dark: "#4ade80"})
	StyleGitDeleted = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "#b91c1c", Dark: "#f87171"})
	StyleGitConflict = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "#b91c1c", Dark: "#f87171"}).Bold(true)
	StyleGitRenamed = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "#1d4ed8", Dark: "#60a5fa"})
}
