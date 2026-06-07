package tui

import "strings"

const pastePreviewLimit = 120

func appendPromptText(current, pasted string) string {
	if current == "" {
		return pasted
	}
	return current + pasted
}

func pasteNotice(pasted string) string {
	trimmed := strings.TrimSpace(pasted)
	if trimmed == "" {
		return ""
	}
	lineCount := strings.Count(trimmed, "\n") + 1
	if len(trimmed) <= pastePreviewLimit && lineCount == 1 {
		return "pasted text"
	}
	return "pasted text block"
}
