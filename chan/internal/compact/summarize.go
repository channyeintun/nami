package compact

import (
	"strings"

	"github.com/channyeintun/gocode/internal/api"
)

// CompactionPromptTemplate is the 9-section summary format adapted from
// the production-tested prompt in services/compact/prompt.ts.
const CompactionPromptTemplate = `Summarize the following conversation for an AI coding assistant. Preserve ALL of the following:

1. **Primary Request**: What the user originally asked for
2. **Technical Concepts**: Languages, frameworks, APIs, patterns discussed
3. **Files & Code**: All file paths mentioned, code snippets written or discussed, modifications made
4. **Errors & Fixes**: Any errors encountered, debugging steps taken, solutions found
5. **Problem Solving**: Key decisions, trade-offs discussed, approaches tried
6. **All User Messages**: Preserve the intent and specifics of every user message
7. **Pending Tasks**: Anything not yet completed, open questions
8. **Current Work**: What was being worked on when this summary was requested
9. **Optional Next Step**: If there's a clear next action, state it

Format as a structured summary that another AI can use to continue the conversation seamlessly.
Do NOT include tool call details or raw API responses — only their meaningful outcomes.
Keep the summary concise but complete. Aim for 1000-2000 tokens.`

// PartialCompactionPromptTemplate scopes the summary to recent messages while
// preserving earlier compacted context verbatim.
const PartialCompactionPromptTemplate = `Summarize only the RECENT portion of the conversation for an AI coding assistant. Earlier retained context will stay verbatim, so preserve only the recent work needed to continue seamlessly.

Preserve ALL of the following from the recent messages:

1. **Primary Request**: What the user most recently asked for
2. **Technical Concepts**: Languages, frameworks, APIs, patterns discussed recently
3. **Files & Code**: All file paths mentioned recently, code snippets written or discussed, modifications made
4. **Errors & Fixes**: Any recent errors encountered, debugging steps taken, solutions found
5. **Problem Solving**: Key recent decisions, trade-offs discussed, approaches tried
6. **All User Messages**: Preserve the intent and specifics of every recent user message
7. **Pending Tasks**: Anything not yet completed from the recent work
8. **Current Work**: What was being worked on immediately before compaction
9. **Optional Next Step**: If there's a clear next action, state it

Format as a structured summary that another AI can use to continue the conversation seamlessly.
Do NOT include tool call details or raw API responses — only their meaningful outcomes.
Keep the summary concise but complete. Aim for 750-1500 tokens.`

const summaryMessagePrefix = "Conversation summary for continuation:"

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
