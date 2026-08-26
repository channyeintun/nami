package goal

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/channyeintun/nami/internal/api"
)

const (
	evaluationTimeout = 45 * time.Second
	evaluationTokens  = 512
	// maxTranscriptChars bounds what the judge reads. The evidence that decides
	// a goal is almost always recent, and a whole long session would crowd out
	// the instructions.
	maxTranscriptChars = 24000
)

// Verdict is the judge's answer.
type Verdict struct {
	// Met reports that the condition holds.
	Met bool `json:"met"`
	// Impossible reports that the condition cannot be satisfied at all, so
	// looping further would only waste turns.
	Impossible bool `json:"impossible,omitempty"`
	// Reason is the evidence behind the call, shown to the user and fed back to
	// the agent when the goal blocks.
	Reason string `json:"reason,omitempty"`
}

// evaluatorSystemPrompt tells the judge to decide from evidence rather than
// from the agent's own claims. The distinction matters: an agent that has
// stalled will report success, and a judge that takes that at face value turns
// the loop into a rubber stamp.
const evaluatorSystemPrompt = `You judge whether a stated condition has been satisfied, based only on evidence in a transcript of an agent's work.

Reply with ONE JSON object and nothing else:
  {"met": true, "reason": "<evidence that it holds>"}
  {"met": false, "reason": "<what is still missing>"}
  {"met": false, "impossible": true, "reason": "<why it can never hold>"}

Rules:
- Decide from what the transcript SHOWS happened — commands run, files changed, output observed. Quote the specific evidence in your reason.
- The agent asserting it finished is evidence, not proof. If the condition names a checkable outcome and the transcript does not show it checked, the condition is not met.
- The agent claiming the condition is impossible is likewise evidence, not proof. Confirm it independently before returning impossible.
- Set impossible only for a condition that cannot hold no matter how long the agent works — it contradicts itself, or it depends on something that does not and cannot exist. A task that is merely hard, slow, or unfinished is NOT impossible.
- With insufficient evidence either way, return met: false and say what evidence is missing.
- Keep reason under 300 characters and make it specific enough to act on.`

// Evaluate asks the model whether the condition holds.
//
// Every failure path returns Met: true. A judge that cannot answer must not be
// able to trap the user in a loop, so an API error, a timeout, or an
// unparseable reply all end the turn normally rather than blocking it.
func Evaluate(ctx context.Context, client api.LLMClient, condition string, messages []api.Message) Verdict {
	condition = strings.TrimSpace(condition)
	if client == nil || condition == "" {
		return Verdict{Met: true, Reason: "goal evaluation unavailable"}
	}

	ctx, cancel := context.WithTimeout(ctx, evaluationTimeout)
	defer cancel()

	stream, err := client.Stream(ctx, api.ModelRequest{
		SystemPrompt: evaluatorSystemPrompt,
		Messages: []api.Message{{
			Role:    api.RoleUser,
			Content: evaluationPrompt(condition, messages),
		}},
		MaxTokens: evaluationTokens,
	})
	if err != nil {
		return Verdict{Met: true, Reason: fmt.Sprintf("goal evaluation failed: %v", err)}
	}

	var builder strings.Builder
	for event, streamErr := range stream {
		if streamErr != nil {
			return Verdict{Met: true, Reason: fmt.Sprintf("goal evaluation failed: %v", streamErr)}
		}
		if event.Type == api.ModelEventToken {
			builder.WriteString(event.Text)
		}
	}

	verdict, ok := ParseVerdict(builder.String())
	if !ok {
		return Verdict{Met: true, Reason: "goal evaluation returned no usable verdict"}
	}
	return verdict
}

func evaluationPrompt(condition string, messages []api.Message) string {
	var b strings.Builder
	b.WriteString("Condition to judge:\n")
	b.WriteString(condition)
	b.WriteString("\n\nTranscript of the agent's work:\n")
	b.WriteString(Transcript(messages))
	b.WriteString("\n\nHas the condition been satisfied? Reply with the JSON object only.")
	return b.String()
}

// Transcript renders the messages the judge reads, keeping the tail because
// the evidence that settles a goal is almost always the most recent work.
func Transcript(messages []api.Message) string {
	var b strings.Builder
	for _, message := range messages {
		line := transcriptLine(message)
		if line == "" {
			continue
		}
		b.WriteString(line)
		b.WriteString("\n")
	}

	text := b.String()
	if len(text) <= maxTranscriptChars {
		return text
	}
	// Trimming from the front can land mid-rune; drop the partial leading rune
	// rather than emitting invalid UTF-8.
	trimmed := strings.ToValidUTF8(text[len(text)-maxTranscriptChars:], "")
	return "[earlier turns omitted]\n" + trimmed
}

func transcriptLine(message api.Message) string {
	content := strings.TrimSpace(message.Content)

	switch message.Role {
	case api.RoleTool:
		// Tool results are the strongest evidence a transcript carries, so they
		// are labelled as results rather than folded into narration.
		if message.ToolResult != nil && message.ToolResult.IsError {
			return "[TOOL ERROR] " + truncateLine(content)
		}
		if content == "" {
			return ""
		}
		return "[TOOL RESULT] " + truncateLine(content)
	case api.RoleAssistant:
		var parts []string
		if content != "" {
			parts = append(parts, "[ASSISTANT] "+truncateLine(content))
		}
		for _, call := range message.ToolCalls {
			parts = append(parts, "[TOOL CALL] "+call.Name+" "+truncateLine(call.Input))
		}
		return strings.Join(parts, "\n")
	default:
		if content == "" {
			return ""
		}
		return "[" + strings.ToUpper(string(message.Role)) + "] " + truncateLine(content)
	}
}

const maxTranscriptLineChars = 2000

func truncateLine(value string) string {
	value = strings.TrimSpace(value)
	if len(value) <= maxTranscriptLineChars {
		return value
	}
	return strings.ToValidUTF8(value[:maxTranscriptLineChars], "") + " […]"
}

// ParseVerdict extracts a verdict from a model reply. Models routinely wrap
// JSON in prose or a code fence, so this scans for the first balanced object
// rather than requiring the whole reply to parse.
func ParseVerdict(raw string) (Verdict, bool) {
	object, ok := firstJSONObject(raw)
	if !ok {
		return Verdict{}, false
	}
	var verdict Verdict
	if err := json.Unmarshal([]byte(object), &verdict); err != nil {
		return Verdict{}, false
	}
	verdict.Reason = strings.TrimSpace(verdict.Reason)
	// "Impossible" is a terminal answer, so it can never also mean satisfied;
	// a reply claiming both would otherwise clear the goal as achieved.
	if verdict.Impossible {
		verdict.Met = false
	}
	return verdict, true
}

// firstJSONObject returns the first balanced {...} run, ignoring braces inside
// strings so a reason containing one does not end the scan early.
func firstJSONObject(raw string) (string, bool) {
	start := strings.IndexByte(raw, '{')
	if start < 0 {
		return "", false
	}

	depth := 0
	inString := false
	escaped := false
	for i := start; i < len(raw); i++ {
		char := raw[i]
		if inString {
			switch {
			case escaped:
				escaped = false
			case char == '\\':
				escaped = true
			case char == '"':
				inString = false
			}
			continue
		}
		switch char {
		case '"':
			inString = true
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return raw[start : i+1], true
			}
		}
	}
	return "", false
}
