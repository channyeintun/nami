package api

import "strings"

type openAICompatRequest struct {
	Model               string                       `json:"model"`
	Messages            []openAICompatMessage        `json:"messages"`
	Tools               []openAICompatToolDefinition `json:"tools,omitempty"`
	MaxTokens           int                          `json:"max_tokens,omitempty"`
	MaxCompletionTokens int                          `json:"max_completion_tokens,omitempty"`
	Temperature         *float64                     `json:"temperature,omitempty"`
	Stop                []string                     `json:"stop,omitempty"`
	Stream              bool                         `json:"stream"`
	StreamOptions       *openAICompatStreamOptions   `json:"stream_options,omitempty"`
}

type openAICompatStreamOptions struct {
	IncludeUsage bool `json:"include_usage"`
}

type openAICompatMessage struct {
	Role       string                 `json:"role"`
	Content    any                    `json:"content,omitempty"`
	ToolCalls  []openAICompatToolCall `json:"tool_calls,omitempty"`
	ToolCallID string                 `json:"tool_call_id,omitempty"`
}

type openAICompatToolDefinition struct {
	Type     string                         `json:"type"`
	Function openAICompatFunctionDefinition `json:"function"`
}

type openAICompatFunctionDefinition struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Parameters  any    `json:"parameters"`
}

type openAICompatToolCall struct {
	ID       string                   `json:"id,omitempty"`
	Type     string                   `json:"type"`
	Function openAICompatFunctionCall `json:"function"`
}

type openAICompatFunctionCall struct {
	Name      string `json:"name,omitempty"`
	Arguments string `json:"arguments,omitempty"`
}

type openAICompatChunk struct {
	Choices []openAICompatChoice   `json:"choices"`
	Usage   *openAICompatUsage     `json:"usage,omitempty"`
	Error   *openAICompatErrorBody `json:"error,omitempty"`
}

type openAICompatChoice struct {
	Delta        openAICompatDelta `json:"delta"`
	FinishReason string            `json:"finish_reason,omitempty"`
}

type openAICompatDelta struct {
	Content          string                      `json:"content,omitempty"`
	Reasoning        string                      `json:"reasoning,omitempty"`
	ReasoningContent string                      `json:"reasoning_content,omitempty"`
	Refusal          string                      `json:"refusal,omitempty"`
	ToolCalls        []openAICompatDeltaToolCall `json:"tool_calls,omitempty"`
	FunctionCall     *openAICompatFunctionCall   `json:"function_call,omitempty"`
}

type openAICompatDeltaToolCall struct {
	Index    int                      `json:"index,omitempty"`
	ID       string                   `json:"id,omitempty"`
	Type     string                   `json:"type,omitempty"`
	Function openAICompatFunctionCall `json:"function"`
}

type openAICompatUsage struct {
	PromptTokens        int `json:"prompt_tokens,omitempty"`
	CompletionTokens    int `json:"completion_tokens,omitempty"`
	TotalTokens         int `json:"total_tokens,omitempty"`
	PromptTokensDetails struct {
		CachedTokens int `json:"cached_tokens,omitempty"`
	} `json:"prompt_tokens_details"`
}

func (u *openAICompatUsage) merge(other *openAICompatUsage) {
	if other == nil {
		return
	}
	if other.PromptTokens > 0 {
		u.PromptTokens = other.PromptTokens
	}
	if other.CompletionTokens > 0 {
		u.CompletionTokens = other.CompletionTokens
	}
	if other.TotalTokens > 0 {
		u.TotalTokens = other.TotalTokens
	}
	if other.PromptTokensDetails.CachedTokens > 0 {
		u.PromptTokensDetails.CachedTokens = other.PromptTokensDetails.CachedTokens
	}
}

// toUsage reports the cached prefix, which prompt_tokens includes, apart from
// the rest of the prompt, since it is billed at the cache-read rate.
func (u openAICompatUsage) toUsage() *Usage {
	cached := u.PromptTokensDetails.CachedTokens
	return &Usage{
		InputTokens:     max(u.PromptTokens-cached, 0),
		OutputTokens:    u.CompletionTokens,
		CacheReadTokens: cached,
	}
}

type openAICompatErrorEnvelope struct {
	Error *openAICompatErrorBody `json:"error,omitempty"`
}

type openAICompatErrorBody struct {
	Type     string                     `json:"type,omitempty"`
	Message  string                     `json:"message,omitempty"`
	Metadata *openAICompatErrorMetadata `json:"metadata,omitempty"`
}

type openAICompatErrorMetadata struct {
	Raw          string `json:"raw,omitempty"`
	ProviderName string `json:"provider_name,omitempty"`
}

type openAICompatStreamState struct {
	usage      openAICompatUsage
	stopReason string
	sentStop   bool
	// toolCalls holds the calls still being assembled, in the order the
	// model started them.
	toolCalls []*openAICompatToolCallState
}

type openAICompatToolCallState struct {
	Index     int
	ID        string
	Name      string
	Type      string
	Arguments strings.Builder
}
