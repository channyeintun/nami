package api

import (
	"context"
	"fmt"
	"io"
	"iter"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/channyeintun/gocode/internal/ipc"
)

// streamingHTTPTimeout is the per-request timeout for streaming API calls.
// Long enough for slow models; context cancellation handles early termination.
const streamingHTTPTimeout = 5 * time.Minute

// newHTTPClient returns an *http.Client with a 5-minute timeout suitable for
// streaming responses. Context cancellation handles premature abort.
func newHTTPClient() *http.Client {
	return &http.Client{
		Timeout: streamingHTTPTimeout,
	}
}

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
	Role             Role              `json:"role"`
	Content          string            `json:"content,omitempty"`
	ReasoningContent string            `json:"reasoning_content,omitempty"`
	Images           []ImageAttachment `json:"images,omitempty"`
	ToolCalls        []ToolCall        `json:"tool_calls,omitempty"`
	ToolResult       *ToolResult       `json:"tool_result,omitempty"`
}

// ToolCall represents a model-requested tool invocation.
type ToolCall struct {
	ID               string `json:"id"`
	Name             string `json:"name"`
	Input            string `json:"input"` // JSON string
	ThoughtSignature string `json:"thought_signature,omitempty"`
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
	Messages        []Message
	SystemPrompt    string
	Tools           []ToolDefinition
	MaxTokens       int
	Temperature     *float64
	StopSequences   []string
	ThinkingBudget  int // 0 = no extended thinking
	ReasoningEffort string
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

type capabilitiesOverrideClient struct {
	inner        LLMClient
	capabilities ModelCapabilities
}

func WithCapabilities(client LLMClient, capabilities ModelCapabilities) LLMClient {
	if client == nil {
		return nil
	}
	return &capabilitiesOverrideClient{inner: client, capabilities: capabilities}
}

func (c *capabilitiesOverrideClient) Stream(ctx context.Context, req ModelRequest) (iter.Seq2[ModelEvent, error], error) {
	return c.inner.Stream(ctx, req)
}

func (c *capabilitiesOverrideClient) ModelID() string {
	return c.inner.ModelID()
}

func (c *capabilitiesOverrideClient) Capabilities() ModelCapabilities {
	return c.capabilities
}

// WarmupCapable is implemented by clients that can preconnect their transport
// during startup without affecting normal request behavior.
type WarmupCapable interface {
	Warmup(ctx context.Context) error
}

// APIKeyFuncSetter is implemented by clients that support lazy API key resolution.
type APIKeyFuncSetter interface {
	SetAPIKeyFunc(fn func() (string, error))
}

// SetAPIKeyFunc sets an API key resolver on the client if it supports it.
// It unwraps decorator layers (e.g. WithCapabilities) to reach the inner client.
func SetAPIKeyFunc(client LLMClient, fn func() (string, error)) {
	if setter, ok := client.(APIKeyFuncSetter); ok {
		setter.SetAPIKeyFunc(fn)
		return
	}
	if wrapper, ok := client.(*capabilitiesOverrideClient); ok {
		SetAPIKeyFunc(wrapper.inner, fn)
	}
}

func (c *capabilitiesOverrideClient) Warmup(ctx context.Context) error {
	warmable, ok := c.inner.(WarmupCapable)
	if !ok || warmable == nil {
		return nil
	}
	return warmable.Warmup(ctx)
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

// warnCustomBaseURL prints a security warning when the caller provides a
// non-default base URL for a provider, since the API key will be forwarded to
// that endpoint.  Callers should opt-in explicitly via GOCODE_ALLOW_CUSTOM_BASE_URL=1.
func warnCustomBaseURL(provider, defaultURL, actualURL string) {
	if strings.TrimRight(actualURL, "/") == strings.TrimRight(defaultURL, "/") {
		return
	}
	if os.Getenv("GOCODE_ALLOW_CUSTOM_BASE_URL") == "1" {
		return
	}
	fmt.Fprintf(os.Stderr,
		"warning: %s base_url is overridden to %q — your API key will be sent to this endpoint; "+
			"set GOCODE_ALLOW_CUSTOM_BASE_URL=1 to suppress this warning\n",
		provider, actualURL)
}

// DeepCopyMessages returns a deep copy of msgs where each Message's slice and
// pointer fields are independent of the originals, safe to read from a
// separate goroutine while the main loop continues to modify messages.
func DeepCopyMessages(msgs []Message) []Message {
	if msgs == nil {
		return nil
	}
	copied := make([]Message, len(msgs))
	for i, m := range msgs {
		copied[i] = m
		if m.ToolCalls != nil {
			copied[i].ToolCalls = make([]ToolCall, len(m.ToolCalls))
			copy(copied[i].ToolCalls, m.ToolCalls)
		}
		if m.Images != nil {
			copied[i].Images = make([]ImageAttachment, len(m.Images))
			copy(copied[i].Images, m.Images)
		}
		if m.ToolResult != nil {
			tr := *m.ToolResult
			copied[i].ToolResult = &tr
		}
	}
	return copied
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
