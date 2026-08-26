package goal

import (
	"strings"
	"testing"

	"github.com/channyeintun/nami/internal/api"
)

func TestParseVerdictAcceptsBareJSON(t *testing.T) {
	verdict, ok := ParseVerdict(`{"met": true, "reason": "all tests pass"}`)
	if !ok || !verdict.Met || verdict.Reason != "all tests pass" {
		t.Fatalf("verdict = %+v, ok = %v", verdict, ok)
	}
}

// Models routinely wrap JSON in prose or a code fence, so the parser has to
// find the object rather than require the whole reply to be one.
func TestParseVerdictFindsJSONInsideProse(t *testing.T) {
	raw := "Here is my assessment:\n```json\n{\"met\": false, \"reason\": \"two tests still fail\"}\n```\nHope that helps."
	verdict, ok := ParseVerdict(raw)
	if !ok || verdict.Met || verdict.Reason != "two tests still fail" {
		t.Fatalf("verdict = %+v, ok = %v", verdict, ok)
	}
}

// A reason containing a brace must not end the object scan early.
func TestParseVerdictHandlesBracesInsideStrings(t *testing.T) {
	verdict, ok := ParseVerdict(`{"met": false, "reason": "the literal {\"a\": 1} is still wrong"}`)
	if !ok {
		t.Fatal("failed to parse an object with a brace in a string")
	}
	if !strings.Contains(verdict.Reason, `{"a": 1}`) {
		t.Fatalf("Reason = %q", verdict.Reason)
	}
}

func TestParseVerdictRejectsNonJSON(t *testing.T) {
	for _, raw := range []string{"", "the goal is met", "{unbalanced", `{"met": "yes"}`} {
		if _, ok := ParseVerdict(raw); ok {
			t.Fatalf("ParseVerdict(%q) accepted a bad reply", raw)
		}
	}
}

// Impossible is terminal, so a reply claiming both must not read as satisfied —
// that would clear the goal as achieved.
func TestParseVerdictImpossibleOverridesMet(t *testing.T) {
	verdict, ok := ParseVerdict(`{"met": true, "impossible": true, "reason": "contradicts itself"}`)
	if !ok {
		t.Fatal("failed to parse")
	}
	if verdict.Met || !verdict.Impossible {
		t.Fatalf("verdict = %+v", verdict)
	}
}

// A judge that cannot answer must never be able to trap the user in a loop.
func TestEvaluateWithoutAClientDoesNotBlock(t *testing.T) {
	verdict := Evaluate(t.Context(), nil, "anything", nil)
	if !verdict.Met {
		t.Fatalf("verdict = %+v, want a non-blocking Met", verdict)
	}
}

func TestEvaluateWithoutAConditionDoesNotBlock(t *testing.T) {
	verdict := Evaluate(t.Context(), nil, "   ", nil)
	if !verdict.Met {
		t.Fatalf("verdict = %+v", verdict)
	}
}

func TestTranscriptLabelsEvidenceByKind(t *testing.T) {
	transcript := Transcript([]api.Message{
		{Role: api.RoleUser, Content: "fix the build"},
		{Role: api.RoleAssistant, Content: "running tests", ToolCalls: []api.ToolCall{{Name: "bash", Input: `{"command":"go test ./..."}`}}},
		{Role: api.RoleTool, Content: "FAIL", ToolResult: &api.ToolResult{IsError: true}},
		{Role: api.RoleTool, Content: "ok", ToolResult: &api.ToolResult{}},
	})
	for _, want := range []string{"[USER] fix the build", "[ASSISTANT] running tests", "[TOOL CALL] bash", "[TOOL ERROR] FAIL", "[TOOL RESULT] ok"} {
		if !strings.Contains(transcript, want) {
			t.Fatalf("transcript missing %q:\n%s", want, transcript)
		}
	}
}

func TestTranscriptSkipsEmptyMessages(t *testing.T) {
	transcript := Transcript([]api.Message{
		{Role: api.RoleUser, Content: "   "},
		{Role: api.RoleAssistant, Content: ""},
	})
	if strings.TrimSpace(transcript) != "" {
		t.Fatalf("transcript = %q, want empty", transcript)
	}
}

// The evidence that settles a goal is the recent work, so an overlong
// transcript keeps its tail and says what it dropped.
func TestTranscriptKeepsTheTailWhenTruncating(t *testing.T) {
	messages := make([]api.Message, 0, 400)
	for i := range 400 {
		messages = append(messages, api.Message{Role: api.RoleUser, Content: strings.Repeat("x", 200)})
		if i == 399 {
			messages[i].Content = "FINAL EVIDENCE"
		}
	}
	transcript := Transcript(messages)
	if len(transcript) > maxTranscriptChars+len("[earlier turns omitted]\n") {
		t.Fatalf("transcript is %d chars", len(transcript))
	}
	if !strings.Contains(transcript, "[earlier turns omitted]") {
		t.Fatal("truncation was not marked")
	}
	if !strings.Contains(transcript, "FINAL EVIDENCE") {
		t.Fatal("truncation dropped the most recent evidence")
	}
}

func TestTranscriptTruncatesAVeryLongLine(t *testing.T) {
	transcript := Transcript([]api.Message{
		{Role: api.RoleTool, Content: strings.Repeat("y", maxTranscriptLineChars*2), ToolResult: &api.ToolResult{}},
	})
	if !strings.Contains(transcript, "[…]") {
		t.Fatal("a very long line was not truncated")
	}
}
