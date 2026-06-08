package tui

import (
	"strings"

	"charm.land/lipgloss/v2"
)

type transcriptEntry struct {
	Kind string
	Text string
}

func renderTranscript(entries []transcriptEntry) string {
	if len(entries) == 0 {
		return welcomeTranscript()
	}
	lines := make([]string, 0, len(entries))
	for _, entry := range entries {
		if line := renderTranscriptEntry(entry); strings.TrimSpace(line) != "" {
			lines = append(lines, line)
		}
	}
	return strings.Join(lines, "\n")
}

func renderTranscriptEntry(entry transcriptEntry) string {
	text := renderMarkdownText(strings.TrimRight(entry.Text, "\n"))
	switch entry.Kind {
	case "user":
		return userTranscriptStyle.Render("YOU  " + text)
	case "assistant":
		return assistantTranscriptStyle.Render("NAMI " + text)
	case "error":
		return errorTranscriptStyle.Render("ERR  " + text)
	case "tool", "progress":
		return toolTranscriptStyle.Render("TOOL " + text)
	case "artifact":
		return artifactTranscriptStyle.Render("ART  " + text)
	case "background":
		return backgroundTranscriptStyle.Render("BG   " + text)
	default:
		return mutedTranscriptStyle.Render(text)
	}
}

func welcomeTranscript() string {
	logo := strings.Join([]string{
		"NN   NN    AA    MM   MM III",
		"NNN  NN   AAAA   MMM MMM III",
		"NN N NN  AA  AA  MM M MM III",
		"NN  NNN  AAAAAA  MM   MM III",
		"NN   NN AA    AA MM   MM III",
	}, "\n")
	hint := mutedTranscriptStyle.Render("Ask Nami to inspect, plan, or edit code.")
	return lipgloss.JoinVertical(
		lipgloss.Left,
		welcomeLogoStyle.Render(logo),
		"",
		hint,
	)
}

var (
	userTranscriptStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("#F5F5F7")).
				Background(lipgloss.Color("#50515A")).
				Bold(true)

	assistantTranscriptStyle = lipgloss.NewStyle().
					Foreground(lipgloss.Color("#D8D8E2")).
					Background(lipgloss.Color("#50515A"))

	toolTranscriptStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("#9AD97A")).
				Background(lipgloss.Color("#50515A"))

	codeTranscriptStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("#F5F5F7")).
				Background(lipgloss.Color("#30313A")).
				Padding(0, 1)

	artifactTranscriptStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("#F1D66B")).
				Background(lipgloss.Color("#50515A"))

	backgroundTranscriptStyle = lipgloss.NewStyle().
					Foreground(lipgloss.Color("#7DD3FC")).
					Background(lipgloss.Color("#50515A"))

	errorTranscriptStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("#FF7B72")).
				Background(lipgloss.Color("#50515A"))

	mutedTranscriptStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("#B3B4BE")).
				Background(lipgloss.Color("#50515A"))

	welcomeLogoStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("#1BA9E8")).
				Background(lipgloss.Color("#50515A")).
				Bold(true)
)
