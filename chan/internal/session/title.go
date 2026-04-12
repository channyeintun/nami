package session

import (
	"fmt"
	"strings"

	"github.com/channyeintun/gocode/internal/api"
	"github.com/channyeintun/gocode/internal/localmodel"
)

const (
	maxTitleConversationChars = 1000
	titlePrompt               = `Generate a short title (3-7 words, sentence case) for the following conversation. Output ONLY the title, nothing else. No quotes.

Conversation:
%s

Title:`
)

// GenerateTitle uses the local model to generate a short session title
// from the conversation so far. Returns empty string on failure.
func GenerateTitle(router *localmodel.Router, messages []api.Message) string {
	if router == nil || !router.IsLocalAvailable() {
		return ""
	}

	text := extractConversationText(messages)
	if strings.TrimSpace(text) == "" {
		return ""
	}

	prompt := fmt.Sprintf(titlePrompt, text)
	response, used, err := router.TryLocal(localmodel.TaskTitleGen, prompt, 64)
	if !used || err != nil {
		return ""
	}

	return cleanTitle(response)
}

func extractConversationText(messages []api.Message) string {
	var b strings.Builder
	for _, msg := range messages {
		content := strings.TrimSpace(msg.Content)
		if content == "" {
			continue
		}
		b.WriteString("[")
		b.WriteString(strings.ToUpper(string(msg.Role)))
		b.WriteString("] ")
		b.WriteString(content)
		b.WriteString("\n")
	}
	text := b.String()
	if len(text) > maxTitleConversationChars {
		text = text[len(text)-maxTitleConversationChars:]
	}
	return text
}

func cleanTitle(raw string) string {
	title := strings.TrimSpace(raw)
	// Remove surrounding quotes
	if len(title) >= 2 && (title[0] == '"' || title[0] == '\'') && title[len(title)-1] == title[0] {
		title = title[1 : len(title)-1]
	}
	// Strip common prefixes models sometimes add
	for _, prefix := range []string{"Title:", "title:", "Title -", "Title —"} {
		title = strings.TrimPrefix(title, prefix)
	}
	title = strings.TrimSpace(title)
	// Truncate overly long titles
	if len(title) > 80 {
		title = title[:80]
	}
	return title
}
