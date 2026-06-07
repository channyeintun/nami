package tui

import "charm.land/lipgloss/v2"

var (
	statusStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#E6EDF3")).
			Background(lipgloss.Color("#1F6FEB"))

	footerStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#8B949E"))

	errorStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#F85149"))

	dialogStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#E6EDF3")).
			Background(lipgloss.Color("#6E40C9")).
			Padding(0, 1)
)
