package compact

import (
	"strings"

	"github.com/channyeintun/chan/internal/api"
)

// CompactionPromptTemplate is the 9-section summary format adapted from
// the production-tested prompt in services/compact/prompt.ts.
const CompactionPromptTemplate = `Summarize this conversation for an AI coding assistant. Preserve:

1. **Primary Request**: original user ask
2. **Technical Concepts**: languages, frameworks, APIs, patterns
3. **Files & Code**: all paths, snippets, modifications
4. **Errors & Fixes**: errors, debugging steps, solutions
5. **Decisions**: key choices, trade-offs, approaches tried
6. **User Messages**: intent and specifics of every user message
7. **Pending**: incomplete tasks, open questions
8. **Current Work**: what was active at summary time
9. **Next Step**: clear next action if any

Structured summary for seamless continuation. Omit raw tool calls and API responses — outcomes only.
1000-2000 tokens.`

// PartialCompactionPromptTemplate scopes the summary to recent messages while
// preserving earlier compacted context verbatim.
const PartialCompactionPromptTemplate = `Summarize only RECENT messages. Earlier context stays verbatim — cover only new work.

Preserve from recent messages:

1. **Primary Request**: latest user ask
2. **Technical Concepts**: languages, frameworks, APIs, patterns
3. **Files & Code**: paths, snippets, modifications
4. **Errors & Fixes**: errors, debugging, solutions
5. **Decisions**: choices, trade-offs, approaches tried
6. **User Messages**: intent and specifics of every recent user message
7. **Pending**: incomplete tasks, open questions
8. **Current Work**: what was active immediately before compaction
9. **Next Step**: clear next action if any

Structured summary for seamless continuation. Omit raw tool calls and API responses — outcomes only.
750-1500 tokens.`

const summaryMessagePrefix = "Conversation summary for continuation:"

// BuildCompactionPrompt augments a base compaction prompt with session memory
// context so the summarizer can avoid repeating facts already captured.
func BuildCompactionPrompt(basePrompt string, sessionMemoryContent string) string {
	sessionMemoryContent = strings.TrimSpace(sessionMemoryContent)
	if sessionMemoryContent == "" {
		return basePrompt
	}
	return basePrompt + `

Session memory below is preserved separately. Do NOT repeat these facts, files, or decisions. Summarize only information NOT already captured here.

<already_preserved_session_memory>
` + sessionMemoryContent + `
</already_preserved_session_memory>`
}

// NormalizeSummary strips optional analysis blocks and returns the summary payload.
func NormalizeSummary(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ""
	}
	if summary := extractTaggedBlock(trimmed, "summary"); summary != "" {
		return summary
	}
	return trimmed
}

// SplitMessagesForSummary preserves the current user turn while summarizing prior context.
func SplitMessagesForSummary(messages []api.Message) ([]api.Message, []api.Message) {
	if len(messages) == 0 {
		return nil, nil
	}
	last := messages[len(messages)-1]
	if last.Role == api.RoleUser && (strings.TrimSpace(last.Content) != "" || last.ToolResult != nil) {
		prefix := append([]api.Message(nil), messages[:len(messages)-1]...)
		return prefix, []api.Message{last}
	}
	return append([]api.Message(nil), messages...), nil
}

// BuildSummaryMessages creates the compacted conversation state.
func BuildSummaryMessages(summary string, retained []api.Message) []api.Message {
	return BuildSummaryMessagesWithPrefix(nil, summary, retained)
}

// BuildSummaryMessagesWithPrefix preserves an earlier compacted prefix and
// inserts the new summary after it.
func BuildSummaryMessagesWithPrefix(prefix []api.Message, summary string, retained []api.Message) []api.Message {
	normalized := NormalizeSummary(summary)
	if normalized == "" {
		messages := append([]api.Message(nil), prefix...)
		return append(messages, retained...)
	}
	messages := make([]api.Message, 0, len(prefix)+len(retained)+1)
	if len(prefix) > 0 {
		messages = append(messages, prefix...)
	}
	messages = append(messages, api.Message{
		Role:    api.RoleSystem,
		Content: summaryMessagePrefix + "\n\n" + normalized,
	})
	if len(retained) > 0 {
		messages = append(messages, retained...)
	}
	return messages
}

// IsSummaryMessage reports whether a message was produced by compaction.
func IsSummaryMessage(message api.Message) bool {
	if message.Role != api.RoleSystem {
		return false
	}
	return strings.HasPrefix(strings.TrimSpace(message.Content), summaryMessagePrefix)
}

func extractTaggedBlock(input string, tag string) string {
	openTag := "<" + tag + ">"
	closeTag := "</" + tag + ">"
	start := strings.Index(input, openTag)
	if start < 0 {
		return ""
	}
	start += len(openTag)
	end := strings.Index(input[start:], closeTag)
	if end < 0 {
		return ""
	}
	return strings.TrimSpace(input[start : start+end])
}
