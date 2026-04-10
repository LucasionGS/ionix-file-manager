package ui

import "github.com/charmbracelet/lipgloss"

var (
	colorBase      = lipgloss.AdaptiveColor{Light: "#282828", Dark: "#ebdbb2"}
	colorDim       = lipgloss.AdaptiveColor{Light: "#928374", Dark: "#928374"}
	colorHighlight = lipgloss.AdaptiveColor{Light: "#458588", Dark: "#83a598"}
	colorDir       = lipgloss.AdaptiveColor{Light: "#076678", Dark: "#83a598"}
	colorHidden    = lipgloss.AdaptiveColor{Light: "#928374", Dark: "#665c54"}
	colorSelected  = lipgloss.AdaptiveColor{Light: "#d65d0e", Dark: "#fe8019"}
	colorBorder    = lipgloss.AdaptiveColor{Light: "#bdae93", Dark: "#504945"}
	colorStatusBar = lipgloss.AdaptiveColor{Light: "#3c3836", Dark: "#32302f"}

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
			Foreground(lipgloss.Color("#1d2021")).
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
				Foreground(lipgloss.Color("#1d2021")).
				Bold(true).
				PaddingLeft(1)

	StyleSidebarCursorInactive = lipgloss.NewStyle().
					Background(colorBorder).
					Foreground(colorBase).
					PaddingLeft(1)

	StyleSidebarLabel = lipgloss.NewStyle().
				Foreground(colorDim).
				Bold(true).
				PaddingLeft(1).
				MarginTop(1)

	StyleDetailsLabel = lipgloss.NewStyle().
				Foreground(colorDim).
				Bold(true)

	StyleDetailsValue = lipgloss.NewStyle().
				Foreground(colorBase)

	StyleDetailsValueDir = lipgloss.NewStyle().
					Foreground(colorDir).
					Bold(true)
)
