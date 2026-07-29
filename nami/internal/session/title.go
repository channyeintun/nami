package session

import (
	"context"
	"fmt"
	"strings"
	"time"
	"unicode"

	"github.com/channyeintun/nami/internal/api"
	"github.com/channyeintun/nami/internal/localmodel"
)

const (
	maxTitleConversationChars = 1000
	maxTitleChars             = 80
	titlePrompt               = `Generate a short title (3-7 words, sentence case) for the following conversation. Output ONLY the title, nothing else. No quotes.

Conversation:
%s

Title:`
)

// GenerateTitle prefers the local model when available, falls back to the
// active remote model, and finally derives a deterministic title from the
// transcript so sessions are never left untitled.
func GenerateTitle(router *localmodel.Router, client api.LLMClient, messages []api.Message) string {
	text := extractConversationText(messages)
	if strings.TrimSpace(text) == "" {
		return ""
	}

	prompt := fmt.Sprintf(titlePrompt, text)
	if title := generateTitleWithLocal(router, prompt); title != "" {
		return title
	}
	if title := generateTitleWithRemote(client, prompt); title != "" {
		return title
	}
	return heuristicTitle(messages)
}

func generateTitleWithLocal(router *localmodel.Router, prompt string) string {
	if router == nil {
		return ""
	}

	response, used, err := router.TryLocal(localmodel.TaskTitleGen, prompt, 64)
	if !used || err != nil {
		return ""
	}
	return cleanTitle(response)
}

func generateTitleWithRemote(client api.LLMClient, prompt string) string {
	if client == nil {
		return ""
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	stream, err := client.Stream(ctx, api.ModelRequest{
		Messages: []api.Message{{
			Role:    api.RoleUser,
			Content: prompt,
		}},
		MaxTokens: 64,
	})
	if err != nil {
		return ""
	}

	var builder strings.Builder
	for event, streamErr := range stream {
		if streamErr != nil {
			return ""
		}
		if event.Type == api.ModelEventToken {
			builder.WriteString(event.Text)
		}
	}

	return cleanTitle(builder.String())
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
		// Keeping the tail can start mid-rune; drop the partial leading rune.
		text = strings.ToValidUTF8(text[len(text)-maxTitleConversationChars:], "")
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
	title = strings.Trim(title, ".,;:- ")
	// Truncate overly long titles. The cut is by byte, so drop a trailing
	// partial rune rather than surfacing mojibake in the resume list.
	if len(title) > maxTitleChars {
		title = strings.ToValidUTF8(title[:maxTitleChars], "")
	}
	return title
}

func heuristicTitle(messages []api.Message) string {
	for _, msg := range messages {
		if msg.Role != api.RoleUser {
			continue
		}
		candidate := cleanTitle(deriveTitleFromText(msg.Content))
		if candidate != "" {
			return candidate
		}
	}

	for _, msg := range messages {
		candidate := cleanTitle(deriveTitleFromText(msg.Content))
		if candidate != "" {
			return candidate
		}
	}

	return "Conversation"
}

func deriveTitleFromText(input string) string {
	text := normalizeTitleSource(input)
	if text == "" {
		return ""
	}

	words := strings.Fields(text)
	if len(words) == 0 {
		return ""
	}
	if len(words) > 7 {
		words = words[:7]
	}
	return strings.Join(words, " ")
}

func normalizeTitleSource(input string) string {
	trimmed := strings.TrimSpace(input)
	if trimmed == "" {
		return ""
	}

	var builder strings.Builder
	spacePending := false
	for _, r := range trimmed {
		switch {
		case unicode.IsLetter(r), unicode.IsNumber(r):
			if spacePending && builder.Len() > 0 {
				builder.WriteByte(' ')
			}
			builder.WriteRune(r)
			spacePending = false
		case unicode.IsSpace(r), strings.ContainsRune("-_/:.,()[]{}", r):
			spacePending = true
		default:
			spacePending = true
		}
	}

	return strings.TrimSpace(builder.String())
}
