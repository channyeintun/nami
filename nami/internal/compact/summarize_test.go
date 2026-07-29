package compact

import (
	"strings"
	"testing"

	"github.com/channyeintun/nami/internal/api"
)

func TestNormalizeSummaryPrefersTaggedBlock(t *testing.T) {
	raw := `<analysis>scratch work the model should not keep</analysis>
<summary>the real summary</summary>`
	if got := NormalizeSummary(raw); got != "the real summary" {
		t.Fatalf("NormalizeSummary = %q", got)
	}
}

func TestNormalizeSummaryFallsBackToWholeText(t *testing.T) {
	if got := NormalizeSummary("  plain summary  "); got != "plain summary" {
		t.Fatalf("NormalizeSummary = %q", got)
	}
	if got := NormalizeSummary("   "); got != "" {
		t.Fatalf("NormalizeSummary(blank) = %q", got)
	}
}

func TestNormalizeSummaryIgnoresUnclosedTag(t *testing.T) {
	// A truncated response must not silently yield an empty summary, which
	// would discard the entire conversation.
	raw := "<summary>never closed"
	if got := NormalizeSummary(raw); got != raw {
		t.Fatalf("NormalizeSummary = %q, want the raw text", got)
	}
}

func TestSplitMessagesForSummaryPreservesCurrentUserTurn(t *testing.T) {
	messages := []api.Message{
		{Role: api.RoleUser, Content: "first"},
		{Role: api.RoleAssistant, Content: "reply"},
		{Role: api.RoleUser, Content: "current question"},
	}

	prefix, retained := SplitMessagesForSummary(messages)
	if len(prefix) != 2 {
		t.Fatalf("prefix = %+v, want the first two turns", prefix)
	}
	if len(retained) != 1 || retained[0].Content != "current question" {
		t.Fatalf("retained = %+v, want the live user turn", retained)
	}
}

func TestSplitMessagesForSummaryRetainsToolResultTurn(t *testing.T) {
	messages := []api.Message{
		{Role: api.RoleAssistant, Content: "calling"},
		{Role: api.RoleUser, ToolResult: &api.ToolResult{ToolCallID: "call_1", Output: "42"}},
	}
	_, retained := SplitMessagesForSummary(messages)
	if len(retained) != 1 {
		t.Fatalf("retained = %+v, want the tool result turn kept live", retained)
	}
}

func TestSplitMessagesForSummarySummarizesEverythingWhenLastTurnIsNotUser(t *testing.T) {
	messages := []api.Message{
		{Role: api.RoleUser, Content: "first"},
		{Role: api.RoleAssistant, Content: "reply"},
	}
	prefix, retained := SplitMessagesForSummary(messages)
	if len(prefix) != 2 || retained != nil {
		t.Fatalf("prefix = %+v retained = %+v", prefix, retained)
	}
}

func TestSplitMessagesForSummaryDoesNotAliasInput(t *testing.T) {
	messages := []api.Message{{Role: api.RoleUser, Content: "first"}, {Role: api.RoleAssistant, Content: "reply"}}
	prefix, _ := SplitMessagesForSummary(messages)
	prefix[0].Content = "mutated"
	if messages[0].Content != "first" {
		t.Fatal("SplitMessagesForSummary returned a slice aliasing its input")
	}
}

func TestSplitMessagesForSummaryHandlesEmptyInput(t *testing.T) {
	prefix, retained := SplitMessagesForSummary(nil)
	if prefix != nil || retained != nil {
		t.Fatalf("prefix = %+v retained = %+v, want both nil", prefix, retained)
	}
}

func TestBuildSummaryMessagesMarksTheSummaryTurn(t *testing.T) {
	retained := []api.Message{{Role: api.RoleUser, Content: "current"}}
	messages := BuildSummaryMessages("the summary", retained)

	if len(messages) != 2 {
		t.Fatalf("messages = %+v, want summary + retained", messages)
	}
	if !IsSummaryMessage(messages[0]) {
		t.Fatalf("first message is not recognised as a summary: %+v", messages[0])
	}
	if !strings.Contains(messages[0].Content, "the summary") {
		t.Fatalf("summary content missing: %q", messages[0].Content)
	}
	if messages[1].Content != "current" {
		t.Fatalf("retained turn lost: %+v", messages[1])
	}
}

func TestBuildSummaryMessagesWithPrefixKeepsEarlierCompaction(t *testing.T) {
	prefix := []api.Message{{Role: api.RoleSystem, Content: "Conversation summary for continuation:\n\nolder"}}
	messages := BuildSummaryMessagesWithPrefix(prefix, "newer", []api.Message{{Role: api.RoleUser, Content: "live"}})

	if len(messages) != 3 {
		t.Fatalf("messages = %+v, want prefix + summary + retained", messages)
	}
	if !IsSummaryMessage(messages[0]) || !IsSummaryMessage(messages[1]) {
		t.Fatalf("both compaction turns should be summaries: %+v", messages)
	}
}

func TestBuildSummaryMessagesDropsEmptySummary(t *testing.T) {
	// An empty summary must not insert a marker turn with no content.
	retained := []api.Message{{Role: api.RoleUser, Content: "current"}}
	messages := BuildSummaryMessages("   ", retained)
	if len(messages) != 1 || messages[0].Content != "current" {
		t.Fatalf("messages = %+v, want only the retained turn", messages)
	}
}

func TestIsSummaryMessageRequiresSystemRole(t *testing.T) {
	content := "Conversation summary for continuation:\n\nx"
	if !IsSummaryMessage(api.Message{Role: api.RoleSystem, Content: content}) {
		t.Fatal("system summary not recognised")
	}
	// A user quoting the marker must not be mistaken for a compaction turn.
	if IsSummaryMessage(api.Message{Role: api.RoleUser, Content: content}) {
		t.Fatal("user message treated as a summary")
	}
	if IsSummaryMessage(api.Message{Role: api.RoleSystem, Content: "unrelated"}) {
		t.Fatal("unrelated system message treated as a summary")
	}
}

func TestBuildCompactionPromptAppendsSessionMemory(t *testing.T) {
	base := "base prompt"
	if got := BuildCompactionPrompt(base, "   "); got != base {
		t.Fatalf("BuildCompactionPrompt with no memory = %q, want the base unchanged", got)
	}

	got := BuildCompactionPrompt(base, "remembered facts")
	if !strings.HasPrefix(got, base) {
		t.Fatalf("base prompt not preserved: %q", got)
	}
	if !strings.Contains(got, "remembered facts") {
		t.Fatalf("session memory missing: %q", got)
	}
	if !strings.Contains(got, "<already_preserved_session_memory>") {
		t.Fatalf("memory not delimited: %q", got)
	}
}

func TestBuildCompactionRequestMessageWrapsPrompt(t *testing.T) {
	if got := BuildCompactionRequestMessage("   "); got != "" {
		t.Fatalf("BuildCompactionRequestMessage(blank) = %q, want empty", got)
	}

	got := BuildCompactionRequestMessage("summarize please")
	if !strings.Contains(got, "<compaction_request>") || !strings.Contains(got, "summarize please") {
		t.Fatalf("request message = %q", got)
	}
	// The summarizer must not call tools mid-compaction.
	if !strings.Contains(got, "Do not call tools.") {
		t.Fatalf("tool prohibition missing: %q", got)
	}
}

func TestExtractTaggedBlockRequiresBothDelimiters(t *testing.T) {
	if got := extractTaggedBlock("<a>value</a>", "a"); got != "value" {
		t.Errorf("extractTaggedBlock = %q", got)
	}
	if got := extractTaggedBlock("<a>value", "a"); got != "" {
		t.Errorf("unclosed tag = %q, want empty", got)
	}
	if got := extractTaggedBlock("value</a>", "a"); got != "" {
		t.Errorf("unopened tag = %q, want empty", got)
	}
}
