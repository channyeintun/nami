package api

import (
	"encoding/json"
	"fmt"
	"slices"
	"testing"
)

// contentBlocks asserts the block list out of the message's any-typed Content.
func contentBlocks(t *testing.T, msg anthropicMessage) []map[string]any {
	t.Helper()
	blocks, ok := msg.Content.([]map[string]any)
	if !ok {
		t.Fatalf("Content is %T, want []map[string]any", msg.Content)
	}
	return blocks
}

func TestBuildAnthropicMessagesHoistsSystemContent(t *testing.T) {
	system, messages, err := buildAnthropicMessages("  top level  ", []Message{
		{Role: RoleSystem, Content: "extra rules"},
		{Role: RoleSystem, Content: "   "},
		{Role: RoleUser, Content: "hello"},
	})
	if err != nil {
		t.Fatalf("buildAnthropicMessages: %v", err)
	}

	// The system prompt is trimmed, and system-role turns are lifted out of the
	// message list into system blocks rather than sent inline.
	if len(system) != 2 {
		t.Fatalf("system = %+v, want 2 blocks", system)
	}
	if system[0].Text != "top level" || system[1].Text != "extra rules" {
		t.Fatalf("system = %+v", system)
	}
	if len(messages) != 1 || messages[0].Role != "user" {
		t.Fatalf("messages = %+v, want a single user turn", messages)
	}
}

func TestConvertAnthropicMessageOrdersToolResultBeforeText(t *testing.T) {
	// Anthropic requires the tool_result block to lead the user turn that
	// answers a tool call.
	msg := Message{
		Role:       RoleUser,
		Content:    "and here is more context",
		ToolResult: &ToolResult{ToolCallID: "call_1", Output: "42"},
	}

	converted, skip, err := convertAnthropicMessage(msg)
	if err != nil || skip {
		t.Fatalf("convert = skip:%v err:%v", skip, err)
	}
	blocks := contentBlocks(t, converted)
	if len(blocks) != 2 {
		t.Fatalf("content = %+v, want 2 blocks", blocks)
	}
	if blocks[0]["type"] != "tool_result" {
		t.Fatalf("first block = %v, want tool_result", blocks[0]["type"])
	}
	if blocks[1]["type"] != "text" {
		t.Fatalf("second block = %v, want text", blocks[1]["type"])
	}
}

func TestConvertAnthropicMessageSkipsEmptyTurns(t *testing.T) {
	for name, msg := range map[string]Message{
		"empty user":      {Role: RoleUser, Content: "   "},
		"empty assistant": {Role: RoleAssistant, Content: ""},
		"empty tool":      {Role: RoleTool, Content: ""},
	} {
		_, skip, err := convertAnthropicMessage(msg)
		if err != nil {
			t.Fatalf("%s: unexpected error %v", name, err)
		}
		if !skip {
			t.Fatalf("%s: expected the turn to be skipped", name)
		}
	}
}

func TestConvertAnthropicMessageToolRoleWithoutResultBecomesUserText(t *testing.T) {
	converted, skip, err := convertAnthropicMessage(Message{Role: RoleTool, Content: "plain output"})
	if err != nil || skip {
		t.Fatalf("convert = skip:%v err:%v", skip, err)
	}
	// Anthropic has no tool role, so a bare tool turn has to arrive as user text.
	if converted.Role != "user" {
		t.Fatalf("role = %q, want user", converted.Role)
	}
	if blocks := contentBlocks(t, converted); blocks[0]["text"] != "plain output" {
		t.Fatalf("content = %+v", blocks)
	}
}

func TestConvertAnthropicMessageEncodesToolCalls(t *testing.T) {
	converted, skip, err := convertAnthropicMessage(Message{
		Role:      RoleAssistant,
		Content:   "calling a tool",
		ToolCalls: []ToolCall{{ID: "call_1", Name: "read", Input: `{"path":"a.go"}`}},
	})
	if err != nil || skip {
		t.Fatalf("convert = skip:%v err:%v", skip, err)
	}
	blocks := contentBlocks(t, converted)
	if len(blocks) != 2 {
		t.Fatalf("content = %+v, want text + tool_use", blocks)
	}
	use := blocks[1]
	if use["type"] != "tool_use" || use["id"] != "call_1" || use["name"] != "read" {
		t.Fatalf("tool_use block = %+v", use)
	}
	// Input must be decoded into a real object, not forwarded as a JSON string.
	input, ok := use["input"].(map[string]any)
	if !ok || input["path"] != "a.go" {
		t.Fatalf("input = %#v, want decoded object", use["input"])
	}
}

func TestConvertAnthropicMessageRejectsMalformedToolInput(t *testing.T) {
	_, _, err := convertAnthropicMessage(Message{
		Role:      RoleAssistant,
		ToolCalls: []ToolCall{{ID: "call_1", Name: "read", Input: "{not json"}},
	})
	if err == nil {
		t.Fatal("expected an error for malformed tool input")
	}
}

func TestToolResultBlockMarksErrorsOnly(t *testing.T) {
	ok := toolResultBlock(ToolResult{ToolCallID: "call_1", Output: "fine"})
	if _, present := ok["is_error"]; present {
		t.Fatalf("successful result should not carry is_error: %+v", ok)
	}

	failed := toolResultBlock(ToolResult{ToolCallID: "call_2", Output: "boom", IsError: true})
	if failed["is_error"] != true {
		t.Fatalf("failed result = %+v, want is_error true", failed)
	}
}

func TestDecodeToolInput(t *testing.T) {
	// An absent input has to become an empty object; providers reject a bare "".
	empty, err := decodeToolInput("   ")
	if err != nil {
		t.Fatalf("decodeToolInput(blank): %v", err)
	}
	decoded, ok := empty.(map[string]any)
	if !ok || len(decoded) != 0 {
		t.Fatalf("blank input = %#v, want empty object", empty)
	}

	value, err := decodeToolInput(`{"a":1}`)
	if err != nil {
		t.Fatalf("decodeToolInput: %v", err)
	}
	if raw, _ := json.Marshal(value); string(raw) != `{"a":1}` {
		t.Fatalf("round trip = %s", raw)
	}

	if _, err := decodeToolInput("{"); err == nil {
		t.Fatal("expected an error for malformed JSON")
	}
}

func TestBuildRequestSendsThinkingInTheFormTheModelAccepts(t *testing.T) {
	adaptive := anthropicThinking{Type: "adaptive"}
	budget := anthropicThinking{Type: "enabled", BudgetTokens: 4096}
	cases := []struct {
		model string
		want  anthropicThinking
	}{
		// Opus 4.7 and every later model reject budget_tokens with a 400; 4.6
		// deprecated it in favour of adaptive thinking.
		{"claude-sonnet-5", adaptive},
		{"claude-opus-5", adaptive},
		{"claude-opus-5-5", adaptive},
		{"claude-fable-5", adaptive},
		{"claude-opus-4-8", adaptive},
		{"claude-opus-4-7", adaptive},
		{"claude-opus-4-6", adaptive},
		{"claude-sonnet-4.6", adaptive},
		{"some-future-alias", adaptive},
		// The older generations only understand a fixed budget.
		{"claude-haiku-4-5", budget},
		{"claude-haiku-4.5", budget},
		{"claude-sonnet-4-5-20250929", budget},
		{"claude-opus-4-1-20250805", budget},
		{"claude-sonnet-4-20250514", budget},
		{"claude-3-7-sonnet-20250219", budget},
		{"claude-3.7-sonnet", budget},
	}
	for _, tc := range cases {
		client := &AnthropicClient{provider: "anthropic", model: tc.model}
		payload, _, err := client.buildRequest(ModelRequest{
			Messages:       []Message{{Role: RoleUser, Content: "ultrathink"}},
			MaxTokens:      8000,
			ThinkingBudget: 4096,
		})
		if err != nil {
			t.Fatalf("%s: buildRequest: %v", tc.model, err)
		}
		if payload.Thinking == nil || *payload.Thinking != tc.want {
			t.Errorf("%s: thinking = %+v, want %+v", tc.model, payload.Thinking, tc.want)
		}
	}
}

// cacheBreakpoints lists where a request carries cache_control: "tool:<name>",
// "system:<index>" or "message:<index>".
func cacheBreakpoints(t *testing.T, payload anthropicRequest) []string {
	t.Helper()
	var points []string
	for _, tool := range payload.Tools {
		if tool.CacheControl != nil {
			points = append(points, "tool:"+tool.Name)
		}
	}
	for index, block := range payload.System {
		if block.CacheControl != nil {
			points = append(points, fmt.Sprintf("system:%d", index))
		}
	}
	for index, message := range payload.Messages {
		for _, block := range contentBlocks(t, message) {
			if block["cache_control"] != nil {
				points = append(points, fmt.Sprintf("message:%d", index))
			}
		}
	}
	return points
}

func TestBuildRequestCachesTheHistoryBeforeTheTransientTail(t *testing.T) {
	client := &AnthropicClient{provider: "anthropic", model: "claude-sonnet-5", capabilities: ModelCapabilities{SupportsCaching: true}}
	payload, _, err := client.buildRequest(ModelRequest{
		SystemPrompt: "stable rules",
		Tools:        []ToolDefinition{{Name: "read", InputSchema: map[string]any{"type": "object"}}},
		Messages: []Message{
			{Role: RoleUser, Content: "read a.go"},
			{Role: RoleAssistant, ToolCalls: []ToolCall{{ID: "call_1", Name: "read", Input: `{}`}}},
			{Role: RoleTool, ToolResult: &ToolResult{ToolCallID: "call_1", Output: "package a"}},
			// The agent ends every request with per-turn context that the
			// next request no longer carries.
			{Role: RoleUser, Content: "Runtime context for the current turn below."},
		},
		MaxTokens: 8000,
	})
	if err != nil {
		t.Fatalf("buildRequest: %v", err)
	}

	// A read can only land where an earlier request wrote a breakpoint, so the
	// last message the next request will share, the tool result, has to be
	// one. Anthropic allows four breakpoints in all.
	want := []string{"tool:read", "system:0", "message:2", "message:3"}
	if got := cacheBreakpoints(t, payload); !slices.Equal(got, want) {
		t.Fatalf("breakpoints = %v, want %v", got, want)
	}
}

func TestBuildRequestOmitsThinkingUnlessRequested(t *testing.T) {
	client := &AnthropicClient{provider: "anthropic", model: "claude-sonnet-5"}
	payload, _, err := client.buildRequest(ModelRequest{
		Messages:  []Message{{Role: RoleUser, Content: "hi"}},
		MaxTokens: 8000,
	})
	if err != nil {
		t.Fatalf("buildRequest: %v", err)
	}
	if payload.Thinking != nil {
		t.Fatalf("thinking = %+v, want none", payload.Thinking)
	}
}
