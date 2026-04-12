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

// OpenAIResponsesClient implements streaming over the OpenAI Responses API.
type OpenAIResponsesClient struct {
	provider     string
	model        string
	baseURL      string
	apiKey       string
	apiKeyFunc   func() (string, error)
	httpClient   *http.Client
	capabilities ModelCapabilities
}

// SetAPIKeyFunc sets a callback that returns a fresh API key on each call.
// When set, the client calls this instead of using the static apiKey.
func (c *OpenAIResponsesClient) SetAPIKeyFunc(fn func() (string, error)) {
	c.apiKeyFunc = fn
}

func (c *OpenAIResponsesClient) resolveAPIKey() (string, error) {
	if c.apiKeyFunc != nil {
		return c.apiKeyFunc()
	}
	return c.apiKey, nil
}

// NewOpenAIResponsesClient constructs a streaming client for Responses-compatible providers.
func NewOpenAIResponsesClient(provider, model, apiKey, baseURL string) (*OpenAIResponsesClient, error) {
	if provider == "" {
		provider = "openai"
	}

	preset, ok := Presets[provider]
	if !ok {
		return nil, fmt.Errorf("unknown Responses-compatible provider %q", provider)
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

	return &OpenAIResponsesClient{
		provider:     provider,
		model:        model,
		baseURL:      strings.TrimRight(baseURL, "/"),
		apiKey:       apiKey,
		httpClient:   newHTTPClient(),
		capabilities: preset.Capabilities,
	}, nil
}

func (c *OpenAIResponsesClient) ModelID() string {
	return c.model
}

func (c *OpenAIResponsesClient) Capabilities() ModelCapabilities {
	return c.capabilities
}

func (c *OpenAIResponsesClient) Warmup(ctx context.Context) error {
	apiKey, err := c.resolveAPIKey()
	if err != nil {
		return err
	}
	headers := map[string]string{
		"accept":        "application/json",
		"authorization": "Bearer " + apiKey,
	}
	if c.provider == "github-copilot" {
		for key, value := range GitHubCopilotStaticHeaders() {
			headers[strings.ToLower(key)] = value
		}
	}
	return issueWarmupRequest(ctx, c.httpClient, http.MethodHead, c.baseURL+"/models", headers)
}

func (c *OpenAIResponsesClient) Stream(ctx context.Context, req ModelRequest) (iter.Seq2[ModelEvent, error], error) {
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

		state := openAIResponsesStreamState{}
		sseBody := sseBodyWithDebug(resp.Body, "responses")
		err := readSSE(ctx, sseBody, func(_ string, data string) error {
			return c.handleEvent(data, &state, yield)
		})
		if err != nil && !errors.Is(err, errStopStream) {
			yield(ModelEvent{}, err)
			return
		}
		// Safety net: if the SSE stream ended without a terminal event, emit
		// a stop so the agent loop always receives a proper stop reason.
		if !state.sentStop {
			state.emitStop("end_turn", yield)
		}
	}, nil
}

func (c *OpenAIResponsesClient) openStream(ctx context.Context, payload openAIResponsesRequest, extraHeaders map[string]string) (*http.Response, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal OpenAI Responses request: %w", err)
	}

	apiKey, err := c.resolveAPIKey()
	if err != nil {
		return nil, err
	}

	var (
		resp *http.Response
		mu   sync.Mutex
	)

	err = RetryWithBackoff(ctx, DefaultRetryPolicy(), func() error {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/responses", bytes.NewReader(body))
		if err != nil {
			return fmt.Errorf("create OpenAI Responses request: %w", err)
		}
		req.Header.Set("content-type", "application/json")
		req.Header.Set("accept", "text/event-stream")
		req.Header.Set("authorization", "Bearer "+apiKey)
		for key, value := range extraHeaders {
			req.Header.Set(key, value)
		}

		currentResp, err := c.httpClient.Do(req)
		if err != nil {
			return &APIError{Type: ErrNetwork, Message: fmt.Sprintf("OpenAI Responses request failed: %v", err), Err: err}
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

func (c *OpenAIResponsesClient) buildRequest(req ModelRequest) (openAIResponsesRequest, map[string]string, error) {
	maxTokens := req.MaxTokens
	if maxTokens <= 0 {
		maxTokens = c.capabilities.MaxOutputTokens
	}

	input, err := buildOpenAIResponsesInput(req.SystemPrompt, req.Messages, openAIResponsesUsesDeveloperRole(c.model))
	if err != nil {
		return openAIResponsesRequest{}, nil, err
	}

	payload := openAIResponsesRequest{
		Model:           c.model,
		Input:           input,
		Tools:           buildOpenAIResponsesTools(req.Tools),
		MaxOutputTokens: maxTokens,
		Temperature:     req.Temperature,
		Stream:          true,
		Store:           false,
	}
	if effort := ClampReasoningEffort(c.model, req.ReasoningEffort); effort != "" {
		payload.Reasoning = &openAIResponsesReasoning{
			Effort:  effort,
			Summary: "auto",
		}
		payload.Include = []string{"reasoning.encrypted_content"}
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

func (c *OpenAIResponsesClient) handleEvent(data string, state *openAIResponsesStreamState, yield func(ModelEvent, error) bool) error {
	trimmed := strings.TrimSpace(data)
	if trimmed == "" {
		return nil
	}
	if trimmed == "[DONE]" {
		return state.emitStop("end_turn", yield)
	}

	var envelope openAIResponsesEnvelope
	if err := json.Unmarshal([]byte(trimmed), &envelope); err != nil {
		return fmt.Errorf("decode OpenAI Responses event: %w", err)
	}

	switch envelope.Type {
	case "response.created", "response.reasoning_summary_part.added", "response.content_part.added":
		return nil
	case "response.output_item.added":
		return state.handleOutputItemAdded(trimmed)
	case "response.reasoning_summary_text.delta":
		var evt openAIResponsesReasoningDeltaEvent
		if err := json.Unmarshal([]byte(trimmed), &evt); err != nil {
			return fmt.Errorf("decode OpenAI Responses reasoning delta: %w", err)
		}
		if evt.Delta == "" {
			return nil
		}
		state.sawReasoningText = true
		if !yield(ModelEvent{Type: ModelEventThinking, Text: evt.Delta}, nil) {
			return errStopStream
		}
		return nil
	case "response.reasoning_summary_part.done":
		if !state.sawReasoningText {
			return nil
		}
		if !yield(ModelEvent{Type: ModelEventThinking, Text: "\n\n"}, nil) {
			return errStopStream
		}
		return nil
	case "response.output_text.delta":
		var evt openAIResponsesTextDeltaEvent
		if err := json.Unmarshal([]byte(trimmed), &evt); err != nil {
			return fmt.Errorf("decode OpenAI Responses text delta: %w", err)
		}
		if evt.Delta == "" {
			return nil
		}
		state.currentText.WriteString(evt.Delta)
		state.sawContentText = true
		if !yield(ModelEvent{Type: ModelEventToken, Text: evt.Delta}, nil) {
			return errStopStream
		}
		return nil
	case "response.output_text.done":
		var evt openAIResponsesOutputTextDoneEvent
		if err := json.Unmarshal([]byte(trimmed), &evt); err != nil {
			return fmt.Errorf("decode OpenAI Responses output text done: %w", err)
		}
		if evt.Text == "" {
			return nil
		}
		streamed := state.currentText.String()
		if streamed == "" {
			state.currentText.WriteString(evt.Text)
			state.sawContentText = true
			if !yield(ModelEvent{Type: ModelEventToken, Text: evt.Text}, nil) {
				return errStopStream
			}
		} else if strings.HasPrefix(evt.Text, streamed) {
			suffix := evt.Text[len(streamed):]
			if suffix != "" {
				state.currentText.WriteString(suffix)
				if !yield(ModelEvent{Type: ModelEventToken, Text: suffix}, nil) {
					return errStopStream
				}
			}
		}
		return nil
	case "response.refusal.delta":
		var evt openAIResponsesTextDeltaEvent
		if err := json.Unmarshal([]byte(trimmed), &evt); err != nil {
			return fmt.Errorf("decode OpenAI Responses refusal delta: %w", err)
		}
		if evt.Delta == "" {
			return nil
		}
		state.currentText.WriteString(evt.Delta)
		state.sawContentText = true
		if !yield(ModelEvent{Type: ModelEventToken, Text: evt.Delta}, nil) {
			return errStopStream
		}
		return nil
	case "response.function_call_arguments.delta":
		var evt openAIResponsesToolArgumentsDeltaEvent
		if err := json.Unmarshal([]byte(trimmed), &evt); err != nil {
			return fmt.Errorf("decode OpenAI Responses tool delta: %w", err)
		}
		state.appendToolArguments(evt.Delta)
		return nil
	case "response.function_call_arguments.done":
		var evt openAIResponsesToolArgumentsDoneEvent
		if err := json.Unmarshal([]byte(trimmed), &evt); err != nil {
			return fmt.Errorf("decode OpenAI Responses tool final arguments: %w", err)
		}
		state.setToolArguments(evt.Arguments)
		return nil
	case "response.output_item.done":
		return state.handleOutputItemDone(trimmed, yield)
	case "response.completed", "response.incomplete", "response.done":
		var evt openAIResponsesCompletedEvent
		if err := json.Unmarshal([]byte(trimmed), &evt); err != nil {
			return fmt.Errorf("decode OpenAI Responses completed event: %w", err)
		}
		if !state.sawContentText && !state.sawToolCall {
			for _, item := range evt.Response.Output {
				if item.Type != "message" {
					continue
				}
				text := strings.TrimSpace(joinOpenAIResponsesMessageContent(item.Content))
				if text == "" {
					continue
				}
				state.sawContentText = true
				if !yield(ModelEvent{Type: ModelEventToken, Text: text}, nil) {
					return errStopStream
				}
				break
			}
		}
		usage := evt.Response.Usage.toUsage()
		if usage != nil {
			if !yield(ModelEvent{Type: ModelEventUsage, Usage: usage}, nil) {
				return errStopStream
			}
		}
		return state.emitStop(openAIResponsesStopReason(evt.Response.Status), yield)
	case "response.failed":
		var evt openAIResponsesFailedEvent
		if err := json.Unmarshal([]byte(trimmed), &evt); err != nil {
			return fmt.Errorf("decode OpenAI Responses failed event: %w", err)
		}
		message := strings.TrimSpace(evt.Response.Error.Message)
		if message == "" {
			message = strings.TrimSpace(evt.Response.IncompleteDetails.Reason)
		}
		if message == "" {
			message = "OpenAI Responses request failed"
		}
		return &APIError{
			Type:    classifyOpenAICompatErrorType(0, evt.Response.Error.Code, message),
			Message: message,
		}
	case "error":
		var evt openAIResponsesErrorEvent
		if err := json.Unmarshal([]byte(trimmed), &evt); err != nil {
			return fmt.Errorf("decode OpenAI Responses error event: %w", err)
		}
		message := strings.TrimSpace(evt.Message)
		if message == "" {
			message = "OpenAI Responses stream error"
		}
		return &APIError{
			Type:    classifyOpenAICompatErrorType(0, evt.Code, message),
			Message: message,
		}
	default:
		return nil
	}
}

func buildOpenAIResponsesInput(systemPrompt string, messages []Message, developerRole bool) ([]map[string]any, error) {
	items := make([]map[string]any, 0, len(messages)+1)
	if trimmed := strings.TrimSpace(systemPrompt); trimmed != "" {
		role := "system"
		if developerRole {
			role = "developer"
		}
		items = append(items, map[string]any{
			"role":    role,
			"content": trimmed,
		})
	}

	assistantIndex := 0
	toolIndex := 0
	for _, msg := range messages {
		trimmed := strings.TrimSpace(msg.Content)
		switch msg.Role {
		case RoleSystem:
			if trimmed == "" {
				continue
			}
			role := "system"
			if developerRole {
				role = "developer"
			}
			items = append(items, map[string]any{
				"role":    role,
				"content": trimmed,
			})
		case RoleUser:
			content := make([]map[string]any, 0, len(msg.Images)+1)
			if trimmed != "" {
				content = append(content, map[string]any{
					"type": "input_text",
					"text": trimmed,
				})
			}
			for _, image := range msg.Images {
				content = append(content, map[string]any{
					"type":      "input_image",
					"detail":    "auto",
					"image_url": fmt.Sprintf("data:%s;base64,%s", image.MediaType, image.Data),
				})
			}
			if len(content) > 0 {
				items = append(items, map[string]any{
					"role":    "user",
					"content": content,
				})
			}
			if msg.ToolResult != nil {
				items = append(items, openAIResponsesToolResultItem(*msg.ToolResult))
			}
		case RoleTool:
			if msg.ToolResult == nil {
				continue
			}
			items = append(items, openAIResponsesToolResultItem(*msg.ToolResult))
		case RoleAssistant:
			if trimmed != "" {
				items = append(items, map[string]any{
					"type":   "message",
					"role":   "assistant",
					"status": "completed",
					"id":     fmt.Sprintf("msg_%d", assistantIndex),
					"content": []map[string]any{{
						"type":        "output_text",
						"text":        trimmed,
						"annotations": []any{},
					}},
				})
				assistantIndex++
			}
			for _, toolCall := range msg.ToolCalls {
				arguments, err := sanitizeOpenAIResponsesToolArguments(toolCall.Input)
				if err != nil {
					arguments = "{}"
				}
				items = append(items, map[string]any{
					"type":      "function_call",
					"id":        fmt.Sprintf("fc_%d", toolIndex),
					"call_id":   toolCall.ID,
					"name":      toolCall.Name,
					"arguments": arguments,
				})
				toolIndex++
			}
		}
	}

	return items, nil
}

func openAIResponsesToolResultItem(result ToolResult) map[string]any {
	output := result.Output
	if result.IsError {
		output = "ERROR: " + output
	}
	return map[string]any{
		"type":    "function_call_output",
		"call_id": result.ToolCallID,
		"output":  output,
	}
}

func buildOpenAIResponsesTools(tools []ToolDefinition) []openAIResponsesToolDefinition {
	if len(tools) == 0 {
		return nil
	}

	built := make([]openAIResponsesToolDefinition, 0, len(tools))
	for _, tool := range tools {
		built = append(built, openAIResponsesToolDefinition{
			Type:        "function",
			Name:        tool.Name,
			Description: tool.Description,
			Parameters:  sanitizeOpenAIToolSchema(tool.InputSchema),
			Strict:      false,
		})
	}
	return built
}

func openAIResponsesUsesDeveloperRole(model string) bool {
	lower := strings.ToLower(strings.TrimSpace(model))
	return strings.HasPrefix(lower, "gpt-5") || strings.HasPrefix(lower, "o1") || strings.HasPrefix(lower, "o3") || strings.HasPrefix(lower, "o4")
}

func openAIResponsesStopReason(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "completed", "in_progress", "queued", "":
		return "end_turn"
	case "incomplete":
		return "max_tokens"
	default:
		return "end_turn"
	}
}

type openAIResponsesRequest struct {
	Model           string                          `json:"model"`
	Input           []map[string]any                `json:"input"`
	Tools           []openAIResponsesToolDefinition `json:"tools,omitempty"`
	MaxOutputTokens int                             `json:"max_output_tokens,omitempty"`
	Temperature     *float64                        `json:"temperature,omitempty"`
	Reasoning       *openAIResponsesReasoning       `json:"reasoning,omitempty"`
	Include         []string                        `json:"include,omitempty"`
	Stream          bool                            `json:"stream"`
	Store           bool                            `json:"store"`
}

type openAIResponsesReasoning struct {
	Effort  string `json:"effort,omitempty"`
	Summary string `json:"summary,omitempty"`
}

type openAIResponsesToolDefinition struct {
	Type        string `json:"type"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Parameters  any    `json:"parameters"`
	Strict      bool   `json:"strict"`
}

type openAIResponsesEnvelope struct {
	Type string `json:"type"`
}

type openAIResponsesOutputItemEvent struct {
	Item openAIResponsesOutputItem `json:"item"`
}

type openAIResponsesOutputItem struct {
	Type      string                          `json:"type"`
	ID        string                          `json:"id,omitempty"`
	CallID    string                          `json:"call_id,omitempty"`
	Name      string                          `json:"name,omitempty"`
	Arguments string                          `json:"arguments,omitempty"`
	Content   []openAIResponsesMessageContent `json:"content,omitempty"`
	Summary   []openAIResponsesSummaryPart    `json:"summary,omitempty"`
}

type openAIResponsesMessageContent struct {
	Type    string `json:"type,omitempty"`
	Text    string `json:"text,omitempty"`
	Refusal string `json:"refusal,omitempty"`
}

type openAIResponsesSummaryPart struct {
	Text string `json:"text,omitempty"`
}

type openAIResponsesReasoningDeltaEvent struct {
	Delta string `json:"delta"`
}

type openAIResponsesTextDeltaEvent struct {
	Delta string `json:"delta"`
}

type openAIResponsesToolArgumentsDeltaEvent struct {
	Delta string `json:"delta"`
}

type openAIResponsesToolArgumentsDoneEvent struct {
	Arguments string `json:"arguments"`
}

type openAIResponsesOutputTextDoneEvent struct {
	Text string `json:"text"`
}

type openAIResponsesCompletedEvent struct {
	Response struct {
		Status string                      `json:"status"`
		Usage  openAIResponsesUsage        `json:"usage"`
		Output []openAIResponsesOutputItem `json:"output,omitempty"`
	} `json:"response"`
}

type openAIResponsesUsage struct {
	InputTokens        int `json:"input_tokens,omitempty"`
	OutputTokens       int `json:"output_tokens,omitempty"`
	TotalTokens        int `json:"total_tokens,omitempty"`
	InputTokensDetails struct {
		CachedTokens int `json:"cached_tokens,omitempty"`
	} `json:"input_tokens_details,omitempty"`
}

func (u openAIResponsesUsage) toUsage() *Usage {
	if u.InputTokens == 0 && u.OutputTokens == 0 && u.TotalTokens == 0 && u.InputTokensDetails.CachedTokens == 0 {
		return nil
	}
	inputTokens := u.InputTokens - u.InputTokensDetails.CachedTokens
	if inputTokens < 0 {
		inputTokens = 0
	}
	return &Usage{
		InputTokens:     inputTokens,
		OutputTokens:    u.OutputTokens,
		CacheReadTokens: u.InputTokensDetails.CachedTokens,
	}
}

type openAIResponsesFailedEvent struct {
	Response struct {
		Error struct {
			Code    string `json:"code,omitempty"`
			Message string `json:"message,omitempty"`
		} `json:"error,omitempty"`
		IncompleteDetails struct {
			Reason string `json:"reason,omitempty"`
		} `json:"incomplete_details,omitempty"`
	} `json:"response"`
}

type openAIResponsesErrorEvent struct {
	Code    string `json:"code,omitempty"`
	Message string `json:"message,omitempty"`
}

type openAIResponsesStreamState struct {
	currentTool      *openAIResponsesToolCallState
	currentText      strings.Builder
	sawReasoningText bool
	sawContentText   bool
	sawToolCall      bool
	sentStop         bool
}

type openAIResponsesToolCallState struct {
	ID        string
	Name      string
	Arguments strings.Builder
}

func (s *openAIResponsesStreamState) handleOutputItemAdded(data string) error {
	var evt openAIResponsesOutputItemEvent
	if err := json.Unmarshal([]byte(data), &evt); err != nil {
		return fmt.Errorf("decode OpenAI Responses output item: %w", err)
	}
	if evt.Item.Type == "message" {
		s.currentText.Reset()
		return nil
	}
	if evt.Item.Type != "function_call" {
		return nil
	}
	tool := &openAIResponsesToolCallState{
		ID:   strings.TrimSpace(evt.Item.CallID),
		Name: strings.TrimSpace(evt.Item.Name),
	}
	tool.Arguments.WriteString(evt.Item.Arguments)
	s.currentTool = tool
	return nil
}

func (s *openAIResponsesStreamState) appendToolArguments(delta string) {
	if s.currentTool == nil || delta == "" {
		return
	}
	s.currentTool.Arguments.WriteString(delta)
}

func (s *openAIResponsesStreamState) setToolArguments(arguments string) {
	if s.currentTool == nil {
		return
	}
	s.currentTool.Arguments.Reset()
	s.currentTool.Arguments.WriteString(arguments)
}

func (s *openAIResponsesStreamState) handleOutputItemDone(data string, yield func(ModelEvent, error) bool) error {
	var evt openAIResponsesOutputItemEvent
	if err := json.Unmarshal([]byte(data), &evt); err != nil {
		return fmt.Errorf("decode OpenAI Responses output item done: %w", err)
	}
	if evt.Item.Type == "message" {
		return s.emitMessageSuffix(evt.Item, yield)
	}
	if evt.Item.Type == "reasoning" {
		if len(evt.Item.Summary) > 0 {
			s.sawReasoningText = true
		}
		return nil
	}
	if evt.Item.Type != "function_call" {
		return nil
	}

	tool := s.currentTool
	if tool == nil {
		tool = &openAIResponsesToolCallState{
			ID:   strings.TrimSpace(evt.Item.CallID),
			Name: strings.TrimSpace(evt.Item.Name),
		}
		tool.Arguments.WriteString(evt.Item.Arguments)
	}

	if tool.ID == "" {
		tool.ID = strings.TrimSpace(evt.Item.CallID)
	}
	if tool.ID == "" {
		tool.ID = strings.TrimSpace(evt.Item.ID)
	}
	if tool.Name == "" {
		tool.Name = strings.TrimSpace(evt.Item.Name)
	}

	arguments, err := resolveOpenAIResponsesToolArguments(tool.Arguments.String(), evt.Item.Arguments)
	if err != nil {
		return fmt.Errorf("decode OpenAI Responses tool input: %w", err)
	}

	s.currentTool = nil
	s.sawToolCall = true
	if !yield(ModelEvent{Type: ModelEventToolCall, ToolCall: &ToolCall{ID: tool.ID, Name: tool.Name, Input: arguments}}, nil) {
		return errStopStream
	}
	return nil
}

func (s *openAIResponsesStreamState) emitMessageSuffix(item openAIResponsesOutputItem, yield func(ModelEvent, error) bool) error {
	finalText := strings.TrimSpace(joinOpenAIResponsesMessageContent(item.Content))
	streamed := s.currentText.String()
	s.currentText.Reset()
	if finalText == "" {
		return nil
	}

	suffix := finalText
	if streamed != "" && strings.HasPrefix(finalText, streamed) {
		suffix = finalText[len(streamed):]
	}
	if suffix == "" {
		return nil
	}
	s.sawContentText = true
	if !yield(ModelEvent{Type: ModelEventToken, Text: suffix}, nil) {
		return errStopStream
	}
	return nil
}

func resolveOpenAIResponsesToolArguments(streamed string, final string) (string, error) {
	candidates := []string{streamed}
	if final != streamed {
		candidates = append(candidates, final)
	}

	var lastErr error
	for _, candidate := range candidates {
		normalized, err := sanitizeOpenAIResponsesToolArguments(candidate)
		if err == nil {
			return normalized, nil
		}
		lastErr = err
	}
	return "", lastErr
}

func sanitizeOpenAIResponsesToolArguments(arguments string) (string, error) {
	normalized := strings.TrimSpace(arguments)
	if normalized == "" {
		normalized = "{}"
	}
	if _, err := decodeToolInput(normalized); err != nil {
		return "", err
	}
	return normalized, nil
}

func joinOpenAIResponsesMessageContent(content []openAIResponsesMessageContent) string {
	var builder strings.Builder
	for _, part := range content {
		switch part.Type {
		case "output_text":
			builder.WriteString(part.Text)
		case "refusal":
			builder.WriteString(part.Refusal)
		}
	}
	return builder.String()
}

func (s *openAIResponsesStreamState) emitStop(stopReason string, yield func(ModelEvent, error) bool) error {
	if s.sentStop {
		return nil
	}
	if s.sawToolCall {
		stopReason = "tool_use"
	}
	if stopReason == "" {
		stopReason = "end_turn"
	}
	s.sentStop = true
	if !yield(ModelEvent{Type: ModelEventStop, StopReason: stopReason}, nil) {
		return errStopStream
	}
	return errStopStream
}
