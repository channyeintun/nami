package api

import (
	"encoding/json"
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
