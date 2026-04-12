package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"iter"
	"net/http"
	"os"
	"strings"
	"sync"
)

// OpenAICompatClient implements OpenAI-compatible chat completions streaming.
type OpenAICompatClient struct {
	provider     string
	model        string
	baseURL      string
	apiKey       string
	httpClient   *http.Client
	capabilities ModelCapabilities
}

// NewOpenAICompatClient constructs a streaming client for OpenAI-compatible providers.
func NewOpenAICompatClient(provider, model, apiKey, baseURL string) (*OpenAICompatClient, error) {
	if provider == "" {
		provider = "openai"
	}

	preset, ok := Presets[provider]
	if !ok {
		return nil, fmt.Errorf("unknown OpenAI-compatible provider %q", provider)
	}
	if preset.ClientType != OpenAICompatAPI {
		return nil, fmt.Errorf("provider %q is not OpenAI-compatible", provider)
	}
	if model == "" {
		model = preset.DefaultModel
	}
	if baseURL == "" {
		baseURL = preset.BaseURL
	}
	if provider != "github-copilot" {
		warnCustomBaseURL(provider, preset.BaseURL, baseURL)
	}
	if apiKey == "" {
		apiKey = os.Getenv(preset.EnvKeyVar)
	}
	if apiKey == "" {
		return nil, &APIError{Type: ErrAuth, Message: fmt.Sprintf("missing API key for provider %q", provider)}
	}

	return &OpenAICompatClient{
		provider:     provider,
		model:        model,
		baseURL:      strings.TrimRight(baseURL, "/"),
		apiKey:       apiKey,
		httpClient:   newHTTPClient(),
		capabilities: preset.Capabilities,
	}, nil
}

// ModelID returns the active model identifier.
func (c *OpenAICompatClient) ModelID() string {
	return c.model
}

// Capabilities reports model capabilities.
func (c *OpenAICompatClient) Capabilities() ModelCapabilities {
	return c.capabilities
}

// Warmup preconnects the OpenAI-compatible transport before the first streamed turn.
func (c *OpenAICompatClient) Warmup(ctx context.Context) error {
	headers := map[string]string{
		"accept":        "application/json",
		"authorization": "Bearer " + c.apiKey,
	}
	if c.provider == "github-copilot" {
		for key, value := range GitHubCopilotStaticHeaders() {
			headers[strings.ToLower(key)] = value
		}
	}
	return issueWarmupRequest(ctx, c.httpClient, http.MethodHead, c.baseURL+"/models", headers)
}

// Stream opens a streaming chat completions request and yields model events.
func (c *OpenAICompatClient) Stream(ctx context.Context, req ModelRequest) (iter.Seq2[ModelEvent, error], error) {
	payload, extraHeaders, err := c.buildRequest(req)
	if err != nil {
		return nil, err
	}

	resp, err := c.openStream(ctx, payload, extraHeaders)
	if err != nil {
		return nil, err
	}

	return func(yield func(ModelEvent, error) bool) {
		defer resp.Body.Close()

		state := openAICompatStreamState{
			toolCalls: make(map[int]*openAICompatToolCallState),
		}

		err := readSSE(ctx, resp.Body, func(_ string, data string) error {
			return c.handleEvent(data, &state, yield)
		})
		if err != nil && !errors.Is(err, errStopStream) {
			yield(ModelEvent{}, err)
		}
	}, nil
}

func (c *OpenAICompatClient) openStream(ctx context.Context, payload openAICompatRequest, extraHeaders map[string]string) (*http.Response, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal OpenAI-compatible request: %w", err)
	}

	var (
		resp *http.Response
		mu   sync.Mutex
	)

	err = RetryWithBackoff(ctx, DefaultRetryPolicy(), func() error {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/chat/completions", bytes.NewReader(body))
		if err != nil {
			return fmt.Errorf("create OpenAI-compatible request: %w", err)
		}
		req.Header.Set("content-type", "application/json")
		req.Header.Set("accept", "text/event-stream")
		req.Header.Set("authorization", "Bearer "+c.apiKey)
		for key, value := range extraHeaders {
			req.Header.Set(key, value)
		}

		currentResp, err := c.httpClient.Do(req)
		if err != nil {
			return &APIError{Type: ErrNetwork, Message: fmt.Sprintf("OpenAI-compatible request failed: %v", err), Err: err}
		}
		if currentResp.StatusCode >= http.StatusMultipleChoices {
			defer currentResp.Body.Close()
			bodyBytes, _ := io.ReadAll(io.LimitReader(currentResp.Body, 1<<20))
			return classifyOpenAICompatStatus(currentResp.StatusCode, bodyBytes)
		}

		mu.Lock()
		resp = currentResp
		mu.Unlock()
		return nil
	})
	if err != nil {
		return nil, err
	}

	mu.Lock()
	defer mu.Unlock()
	return resp, nil
}

func (c *OpenAICompatClient) buildRequest(req ModelRequest) (openAICompatRequest, map[string]string, error) {
	maxTokens := req.MaxTokens
	if maxTokens <= 0 {
		maxTokens = c.capabilities.MaxOutputTokens
	}

	messages, err := buildOpenAICompatMessages(req.SystemPrompt, req.Messages)
	if err != nil {
		return openAICompatRequest{}, nil, err
	}

	payload := openAICompatRequest{
		Model:       c.model,
		Messages:    messages,
		Tools:       buildOpenAICompatTools(req.Tools),
		MaxTokens:   maxTokens,
		Stream:      true,
		Stop:        req.StopSequences,
		Temperature: req.Temperature,
	}

	var extraHeaders map[string]string
	if c.provider == "github-copilot" {
		extraHeaders = GitHubCopilotStaticHeaders()
		for key, value := range BuildGitHubCopilotDynamicHeaders(req.Messages) {
			extraHeaders[key] = value
		}
	}

	return payload, extraHeaders, nil
}

func (c *OpenAICompatClient) handleEvent(
	data string,
	state *openAICompatStreamState,
	yield func(ModelEvent, error) bool,
) error {
	trimmed := strings.TrimSpace(data)
	if trimmed == "" {
		return nil
	}
	if trimmed == "[DONE]" {
		return state.emitStop(yield)
	}

	var chunk openAICompatChunk
	if err := json.Unmarshal([]byte(trimmed), &chunk); err != nil {
		return fmt.Errorf("decode OpenAI-compatible stream chunk: %w", err)
	}
	if chunk.Error != nil {
		message := openAICompatErrorMessage(*chunk.Error)
		return &APIError{
			Type:    classifyOpenAICompatErrorType(0, chunk.Error.Type, message),
			Message: message,
		}
	}
	if chunk.Usage != nil {
		state.usage.merge(chunk.Usage)
		if !yield(ModelEvent{Type: ModelEventUsage, Usage: state.usage.toUsage()}, nil) {
			return errStopStream
		}
	}

	for _, choice := range chunk.Choices {
		if reasoning := firstNonEmpty(choice.Delta.Reasoning, choice.Delta.ReasoningContent); reasoning != "" {
			if !yield(ModelEvent{Type: ModelEventThinking, Text: reasoning}, nil) {
				return errStopStream
			}
		}
		if choice.Delta.Content != "" {
			if !yield(ModelEvent{Type: ModelEventToken, Text: choice.Delta.Content}, nil) {
				return errStopStream
			}
		}
		if choice.Delta.Refusal != "" {
			if !yield(ModelEvent{Type: ModelEventToken, Text: choice.Delta.Refusal}, nil) {
				return errStopStream
			}
		}

		for _, toolCall := range choice.Delta.ToolCalls {
			state.applyToolCallDelta(toolCall)
		}
		if choice.Delta.FunctionCall != nil {
			state.applyLegacyFunctionDelta(*choice.Delta.FunctionCall)
		}

		if choice.FinishReason != "" {
			mapped := mapOpenAICompatStopReason(choice.FinishReason)
			if mapped == "tool_use" {
				if err := state.emitToolCalls(yield); err != nil {
					return err
				}
			}
			state.stopReason = mapped
		}
	}

	return nil
}

func buildOpenAICompatMessages(systemPrompt string, messages []Message) ([]openAICompatMessage, error) {
	systemParts := make([]string, 0, len(messages)+1)
	if trimmed := strings.TrimSpace(systemPrompt); trimmed != "" {
		systemParts = append(systemParts, trimmed)
	}

	built := make([]openAICompatMessage, 0, len(messages)+1)
	for _, msg := range messages {
		if msg.Role == RoleSystem {
			if trimmed := strings.TrimSpace(msg.Content); trimmed != "" {
				systemParts = append(systemParts, trimmed)
			}
			continue
		}

		converted, err := convertOpenAICompatMessage(msg)
		if err != nil {
			return nil, err
		}
		built = append(built, converted...)
	}

	if len(systemParts) > 0 {
		built = append([]openAICompatMessage{{Role: "system", Content: strings.Join(systemParts, "\n\n")}}, built...)
	}

	return built, nil
}

func convertOpenAICompatMessage(msg Message) ([]openAICompatMessage, error) {
	trimmed := strings.TrimSpace(msg.Content)
	converted := make([]openAICompatMessage, 0, 2)

	switch msg.Role {
	case RoleUser:
		if trimmed != "" || len(msg.Images) > 0 {
			content := buildOpenAICompatUserContent(trimmed, msg.Images)
			converted = append(converted, openAICompatMessage{Role: "user", Content: content})
		}
		if msg.ToolResult != nil {
			converted = append(converted, openAICompatToolResultMessage(*msg.ToolResult))
		}
	case RoleTool:
		if msg.ToolResult != nil {
			converted = append(converted, openAICompatToolResultMessage(*msg.ToolResult))
		} else if trimmed != "" {
			converted = append(converted, openAICompatMessage{Role: "tool", Content: trimmed})
		}
	case RoleAssistant:
		assistant := openAICompatMessage{Role: "assistant"}
		if trimmed != "" {
			assistant.Content = trimmed
		}
		if len(msg.ToolCalls) > 0 {
			assistant.ToolCalls = make([]openAICompatToolCall, 0, len(msg.ToolCalls))
			for _, toolCall := range msg.ToolCalls {
				assistantCall, err := buildOpenAICompatAssistantToolCall(toolCall)
				if err != nil {
					return nil, err
				}
				assistant.ToolCalls = append(assistant.ToolCalls, assistantCall)
			}
		}
		if assistant.Content != nil || len(assistant.ToolCalls) > 0 {
			converted = append(converted, assistant)
		}
	default:
		if trimmed != "" {
			converted = append(converted, openAICompatMessage{Role: string(msg.Role), Content: trimmed})
		}
	}

	return converted, nil
}

func buildOpenAICompatUserContent(text string, images []ImageAttachment) any {
	if len(images) == 0 {
		return text
	}

	parts := make([]map[string]any, 0, len(images)+1)
	if text != "" {
		parts = append(parts, map[string]any{
			"type": "text",
			"text": text,
		})
	}

	for _, image := range images {
		parts = append(parts, map[string]any{
			"type": "image_url",
			"image_url": map[string]any{
				"url": fmt.Sprintf("data:%s;base64,%s", image.MediaType, image.Data),
			},
		})
	}

	return parts
}

func openAICompatToolResultMessage(result ToolResult) openAICompatMessage {
	content := result.Output
	if result.IsError {
		content = "ERROR: " + content
	}
	return openAICompatMessage{
		Role:       "tool",
		Content:    content,
		ToolCallID: result.ToolCallID,
	}
}

func buildOpenAICompatAssistantToolCall(toolCall ToolCall) (openAICompatToolCall, error) {
	arguments := toolCall.Input
	if strings.TrimSpace(arguments) == "" {
		arguments = "{}"
	}
	if _, err := decodeToolInput(arguments); err != nil {
		return openAICompatToolCall{}, err
	}
	return openAICompatToolCall{
		ID:   toolCall.ID,
		Type: "function",
		Function: openAICompatFunctionCall{
			Name:      toolCall.Name,
			Arguments: arguments,
		},
	}, nil
}

func buildOpenAICompatTools(tools []ToolDefinition) []openAICompatToolDefinition {
	if len(tools) == 0 {
		return nil
	}

	built := make([]openAICompatToolDefinition, 0, len(tools))
	for _, tool := range tools {
		built = append(built, openAICompatToolDefinition{
			Type: "function",
			Function: openAICompatFunctionDefinition{
				Name:        tool.Name,
				Description: tool.Description,
				Parameters:  sanitizeOpenAIToolSchema(tool.InputSchema),
			},
		})
	}
	return built
}

func classifyOpenAICompatStatus(statusCode int, body []byte) error {
	var envelope openAICompatErrorEnvelope
	if err := json.Unmarshal(body, &envelope); err != nil || envelope.Error == nil {
		message := strings.TrimSpace(string(body))
		if message == "" {
			message = http.StatusText(statusCode)
		}
		return &APIError{
			Type:       classifyOpenAICompatErrorType(statusCode, "", message),
			StatusCode: statusCode,
			Message:    message,
		}
	}

	message := openAICompatErrorMessage(*envelope.Error)
	if message == "" {
		message = http.StatusText(statusCode)
	}

	return &APIError{
		Type:       classifyOpenAICompatErrorType(statusCode, envelope.Error.Type, message),
		StatusCode: statusCode,
		Message:    message,
	}
}

func classifyOpenAICompatErrorType(statusCode int, errorType, message string) APIErrorType {
	lowerType := strings.ToLower(errorType)
	lowerMessage := strings.ToLower(message)

	switch {
	case statusCode == http.StatusUnauthorized || statusCode == http.StatusForbidden:
		return ErrAuth
	case statusCode == http.StatusTooManyRequests || strings.Contains(lowerType, "rate_limit"):
		return ErrRateLimit
	case statusCode >= http.StatusInternalServerError || strings.Contains(lowerType, "server_error") || strings.Contains(lowerType, "overloaded"):
		return ErrOverloaded
	case strings.Contains(lowerMessage, "maximum context length") || strings.Contains(lowerMessage, "context length") || strings.Contains(lowerMessage, "prompt too long"):
		return ErrPromptTooLong
	case strings.Contains(lowerMessage, "max_tokens") || strings.Contains(lowerMessage, "maximum output tokens"):
		return ErrMaxTokens
	default:
		return ErrUnknown
	}
}

func mapOpenAICompatStopReason(reason string) string {
	switch reason {
	case "stop", "end_turn":
		return "end_turn"
	case "tool_calls", "function_call":
		return "tool_use"
	case "length", "max_tokens":
		return "max_tokens"
	default:
		return reason
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

type openAICompatRequest struct {
	Model       string                       `json:"model"`
	Messages    []openAICompatMessage        `json:"messages"`
	Tools       []openAICompatToolDefinition `json:"tools,omitempty"`
	MaxTokens   int                          `json:"max_tokens,omitempty"`
	Temperature *float64                     `json:"temperature,omitempty"`
	Stop        []string                     `json:"stop,omitempty"`
	Stream      bool                         `json:"stream"`
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
	PromptTokens     int `json:"prompt_tokens,omitempty"`
	CompletionTokens int `json:"completion_tokens,omitempty"`
	TotalTokens      int `json:"total_tokens,omitempty"`
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
}

func (u openAICompatUsage) toUsage() *Usage {
	return &Usage{
		InputTokens:  u.PromptTokens,
		OutputTokens: u.CompletionTokens,
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

func openAICompatErrorMessage(err openAICompatErrorBody) string {
	message := strings.TrimSpace(err.Message)
	if err.Metadata == nil {
		return message
	}

	raw := strings.TrimSpace(err.Metadata.Raw)
	providerName := strings.TrimSpace(err.Metadata.ProviderName)
	if raw == "" {
		return message
	}

	if message == "" || strings.EqualFold(message, "Provider returned error") {
		if providerName != "" {
			return providerName + ": " + raw
		}
		return raw
	}

	if providerName != "" && !strings.Contains(raw, providerName) {
		return message + " (" + providerName + ": " + raw + ")"
	}
	return message + " (" + raw + ")"
}

type openAICompatStreamState struct {
	usage      openAICompatUsage
	stopReason string
	sentStop   bool
	toolCalls  map[int]*openAICompatToolCallState
}

func (s *openAICompatStreamState) applyToolCallDelta(delta openAICompatDeltaToolCall) {
	state := s.toolCallState(delta.Index)
	if delta.ID != "" {
		state.ID = delta.ID
	}
	if delta.Type != "" {
		state.Type = delta.Type
	}
	if delta.Function.Name != "" {
		state.Name = delta.Function.Name
	}
	if delta.Function.Arguments != "" {
		state.Arguments.WriteString(delta.Function.Arguments)
	}
}

func (s *openAICompatStreamState) applyLegacyFunctionDelta(delta openAICompatFunctionCall) {
	state := s.toolCallState(0)
	if state.Type == "" {
		state.Type = "function"
	}
	if delta.Name != "" {
		state.Name = delta.Name
	}
	if delta.Arguments != "" {
		state.Arguments.WriteString(delta.Arguments)
	}
}

func (s *openAICompatStreamState) toolCallState(index int) *openAICompatToolCallState {
	state, ok := s.toolCalls[index]
	if ok {
		return state
	}
	state = &openAICompatToolCallState{}
	s.toolCalls[index] = state
	return state
}

func (s *openAICompatStreamState) emitToolCalls(yield func(ModelEvent, error) bool) error {
	for index, toolCall := range s.toolCalls {
		if toolCall.ID == "" {
			toolCall.ID = fmt.Sprintf("call_%d", index)
		}
		arguments := toolCall.Arguments.String()
		if strings.TrimSpace(arguments) == "" {
			arguments = "{}"
		}
		if _, err := decodeToolInput(arguments); err != nil {
			return fmt.Errorf("decode OpenAI-compatible tool input: %w", err)
		}
		if !yield(ModelEvent{
			Type: ModelEventToolCall,
			ToolCall: &ToolCall{
				ID:    toolCall.ID,
				Name:  toolCall.Name,
				Input: arguments,
			},
		}, nil) {
			return errStopStream
		}
		delete(s.toolCalls, index)
	}
	return nil
}

func (s *openAICompatStreamState) emitStop(yield func(ModelEvent, error) bool) error {
	if s.sentStop {
		return nil
	}
	if len(s.toolCalls) > 0 {
		if err := s.emitToolCalls(yield); err != nil {
			return err
		}
		if s.stopReason == "" {
			s.stopReason = "tool_use"
		}
	}
	if s.stopReason == "" {
		s.stopReason = "end_turn"
	}
	s.sentStop = true
	if !yield(ModelEvent{Type: ModelEventStop, StopReason: s.stopReason}, nil) {
		return errStopStream
	}
	return errStopStream
}

type openAICompatToolCallState struct {
	ID        string
	Name      string
	Type      string
	Arguments strings.Builder
}
