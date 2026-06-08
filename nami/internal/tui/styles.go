package tui

import "charm.land/lipgloss/v2"

var (
	appBackgroundStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("#ECECF1")).
				Background(lipgloss.Color("#50515A"))

	statusStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#ECECF1")).
			Background(lipgloss.Color("#25262D")).
			Padding(0, 1)

	statusReadyStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("#9AD97A")).
				Background(lipgloss.Color("#25262D")).
				Bold(true)

	statusModeStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#F1D66B")).
			Background(lipgloss.Color("#25262D")).
			Bold(true)

	statusModelStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("#F5F5F7")).
				Background(lipgloss.Color("#30313A")).
				Bold(true).
				Padding(0, 1)

	statusMutedStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("#A7A8B3")).
				Background(lipgloss.Color("#25262D"))

	transcriptPanelStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("#ECECF1")).
				Background(lipgloss.Color("#50515A")).
				Padding(1, 2)

	promptPanelStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("#ECECF1")).
				Background(lipgloss.Color("#50515A")).
				Border(lipgloss.NormalBorder(), true, false, false, false).
				BorderForeground(lipgloss.Color("#3B3C46")).
				Padding(0, 1)

	promptMarkerStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("#F1D66B")).
				Background(lipgloss.Color("#50515A")).
				Bold(true)

	footerStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#A7A8B3")).
			Background(lipgloss.Color("#50515A")).
			Padding(0, 2)

	errorStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#FF7B72")).
			Background(lipgloss.Color("#3A2426")).
			Padding(0, 1)

	dialogStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#F5F5F7")).
			Background(lipgloss.Color("#343541")).
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("#1BA9E8")).
			Padding(0, 2)

	dialogTitleStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("#1BA9E8")).
				Background(lipgloss.Color("#343541")).
				Bold(true)

	dialogKeyStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#F1D66B")).
			Background(lipgloss.Color("#343541")).
			Bold(true)
)
