package api

import (
	"context"
	"io"
	"iter"

	"github.com/channyeintun/gocode/internal/ipc"
)

// ClientType identifies the API protocol to use.
type ClientType int

const (
	AnthropicAPI ClientType = iota
	GeminiAPI
	OpenAICompatAPI
	OllamaAPI
)

// ModelCapabilities describes what a model supports.
type ModelCapabilities struct {
	SupportsToolUse          bool
	SupportsExtendedThinking bool
	SupportsVision           bool
	SupportsJsonMode         bool
	SupportsCaching          bool
	MaxContextWindow         int
	MaxOutputTokens          int
}

// Role represents a message role.
type Role string

const (
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool"
)

type ImageAttachment struct {
	ID         int    `json:"id,omitempty"`
	Data       string `json:"data,omitempty"`
	MediaType  string `json:"media_type,omitempty"`
	Filename   string `json:"filename,omitempty"`
	SourcePath string `json:"source_path,omitempty"`
}

// Message is a conversation message.
type Message struct {
	Role       Role              `json:"role"`
	Content    string            `json:"content,omitempty"`
	Images     []ImageAttachment `json:"images,omitempty"`
	ToolCalls  []ToolCall        `json:"tool_calls,omitempty"`
	ToolResult *ToolResult       `json:"tool_result,omitempty"`
}

// ToolCall represents a model-requested tool invocation.
type ToolCall struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Input string `json:"input"` // JSON string
}

// ToolResult is the outcome of a tool execution.
type ToolResult struct {
	ToolCallID string `json:"tool_call_id"`
	Output     string `json:"output"`
	IsError    bool   `json:"is_error,omitempty"`
}

// ToolDefinition describes a tool for the model.
type ToolDefinition struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	InputSchema any    `json:"input_schema"` // JSON Schema object
}

// ModelRequest is the input to an LLM call.
type ModelRequest struct {
	Messages       []Message
	SystemPrompt   string
	Tools          []ToolDefinition
	MaxTokens      int
	Temperature    *float64
	StopSequences  []string
	ThinkingBudget int // 0 = no extended thinking
}

// ModelEventType discriminates model stream events.
type ModelEventType int

const (
	ModelEventToken      ModelEventType = iota // text delta
	ModelEventThinking                         // thinking delta
	ModelEventToolCall                         // complete tool call
	ModelEventStop                             // generation complete
	ModelEventUsage                            // token counts
	ModelEventRateLimits                       // rate limit windows from provider headers
)

// ModelEvent is one event from a streaming model response.
type ModelEvent struct {
	Type       ModelEventType
	Text       string    // for Token/Thinking
	ToolCall   *ToolCall // for ToolCall
	StopReason string    // for Stop: "end_turn", "tool_use", "max_tokens"
	Usage      *Usage    // for Usage
	RateLimits *RateLimits
}

// Usage reports token consumption for a model call.
type Usage struct {
	InputTokens         int `json:"input_tokens"`
	OutputTokens        int `json:"output_tokens"`
	CacheReadTokens     int `json:"cache_read_tokens,omitempty"`
	CacheCreationTokens int `json:"cache_creation_tokens,omitempty"`
}

type RateLimitWindow struct {
	Utilization float64 `json:"utilization"`
	ResetsAt    int64   `json:"resets_at"`
}

type RateLimits struct {
	FiveHour *RateLimitWindow `json:"five_hour,omitempty"`
	SevenDay *RateLimitWindow `json:"seven_day,omitempty"`
}

// LLMClient is the universal interface for all model providers.
type LLMClient interface {
	// Stream returns model events as they arrive.
	Stream(ctx context.Context, req ModelRequest) (iter.Seq2[ModelEvent, error], error)

	// ModelID returns the active model identifier.
	ModelID() string

	// Capabilities reports what this model supports.
	Capabilities() ModelCapabilities
}

// StreamEventAdapter converts ModelEvents to IPC StreamEvents.
func StreamEventAdapter(event ModelEvent) *ipc.StreamEvent {
	switch event.Type {
	case ModelEventToken:
		return nil // caller handles token deltas directly
	case ModelEventThinking:
		return nil // caller handles thinking deltas directly
	default:
		return nil
	}
}

func closeReadCloserOnCancel(ctx context.Context, body io.Reader) func() {
	closer, ok := body.(io.Closer)
	if !ok {
		return func() {}
	}

	done := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			_ = closer.Close()
		case <-done:
		}
	}()

	return func() {
		close(done)
	}
}
