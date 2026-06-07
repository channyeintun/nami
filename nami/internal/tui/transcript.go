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
	lines := make([]string, 0, len(entries))
	for _, entry := range entries {
		if line := renderTranscriptEntry(entry); strings.TrimSpace(line) != "" {
			lines = append(lines, line)
		}
	}
	return strings.Join(lines, "\n")
}

func renderTranscriptEntry(entry transcriptEntry) string {
	text := strings.TrimRight(entry.Text, "\n")
	switch entry.Kind {
	case "user":
		return userTranscriptStyle.Render("> " + text)
	case "assistant":
		return assistantTranscriptStyle.Render("assistant: " + text)
	case "error":
		return errorStyle.Render(text)
	case "tool":
		return toolTranscriptStyle.Render(text)
	default:
		return mutedTranscriptStyle.Render(text)
	}
}

var (
	userTranscriptStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("#E6EDF3"))

	assistantTranscriptStyle = lipgloss.NewStyle().
					Foreground(lipgloss.Color("#D2A8FF"))

	toolTranscriptStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("#7EE787"))

	mutedTranscriptStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("#8B949E"))
)
