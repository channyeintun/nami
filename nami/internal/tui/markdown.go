package tui

import (
	"strings"
)

func renderMarkdownText(text string) string {
	if strings.TrimSpace(text) == "" {
		return text
	}
	lines := strings.Split(text, "\n")
	var out []string
	inFence := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") {
			inFence = !inFence
			continue
		}
		if inFence {
			out = append(out, codeTranscriptStyle.Render(line))
			continue
		}
		out = append(out, stripInlineMarkdown(line))
	}
	return strings.Join(out, "\n")
}

func stripInlineMarkdown(line string) string {
	replacer := strings.NewReplacer("**", "", "__", "", "`", "")
	return replacer.Replace(line)
}
