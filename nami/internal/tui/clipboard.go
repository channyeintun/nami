package tui

import (
	"regexp"
	"strconv"
	"strings"
)

const pastePreviewLimit = 120

var imageReferencePattern = regexp.MustCompile(`\[Image #([0-9]+)\]`)

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

func referencedImageIDs(text string) map[int]bool {
	ids := make(map[int]bool)
	for _, match := range imageReferencePattern.FindAllStringSubmatch(text, -1) {
		id, err := strconv.Atoi(match[1])
		if err == nil && id > 0 {
			ids[id] = true
		}
	}
	return ids
}
