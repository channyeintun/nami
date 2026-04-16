package api

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"iter"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const anthropicVersion = "2023-06-01"

var errStopStream = errors.New("stop stream")

// AnthropicClient implements the Anthropic Messages API streaming protocol.
type AnthropicClient struct {
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
func (c *AnthropicClient) SetAPIKeyFunc(fn func() (string, error)) {
	c.apiKeyFunc = fn
}

func (c *AnthropicClient) resolveAPIKey() (string, error) {
	if c.apiKeyFunc != nil {
		return c.apiKeyFunc()
	}
	return c.apiKey, nil
}

// NewAnthropicClient constructs a streaming Anthropic client using configured defaults.
func NewAnthropicClient(model, apiKey, baseURL string) (*AnthropicClient, error) {
	return NewAnthropicClientForProvider("anthropic", model, apiKey, baseURL)
}

// NewAnthropicClientForProvider constructs a streaming Anthropic-compatible client
// using the auth and default settings for the specified provider.
func NewAnthropicClientForProvider(provider, model, apiKey, baseURL string) (*AnthropicClient, error) {
	if strings.TrimSpace(provider) == "" {
		provider = "anthropic"
	}

	preset, ok := Presets[provider]
	if !ok {
		return nil, fmt.Errorf("unknown Anthropic-compatible provider %q", provider)
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

	return &AnthropicClient{
		provider:     provider,
		model:        model,
		baseURL:      strings.TrimRight(baseURL, "/"),
		apiKey:       apiKey,
		httpClient:   newHTTPClient(),
		capabilities: preset.Capabilities,
	}, nil
}

// ModelID returns the active model identifier.
func (c *AnthropicClient) ModelID() string {
	return c.model
}

// Capabilities reports Anthropic model capabilities.
func (c *AnthropicClient) Capabilities() ModelCapabilities {
	return c.capabilities
}

// Warmup preconnects the Anthropic transport so the first real request avoids
// paying the initial connection handshake cost on the critical path.
func (c *AnthropicClient) Warmup(ctx context.Context) error {
	apiKey, err := c.resolveAPIKey()
	if err != nil {
		return err
	}
	headers := map[string]string{
		"accept":            "application/json",
		"anthropic-version": anthropicVersion,
	}
	if c.provider == "github-copilot" {
		headers["authorization"] = "Bearer " + apiKey
		for key, value := range GitHubCopilotStaticHeaders() {
			headers[strings.ToLower(key)] = value
		}
	} else {
		headers["x-api-key"] = apiKey
	}

	return issueWarmupRequest(ctx, c.httpClient, http.MethodHead, c.baseURL+"/v1/messages", headers)
}

// Stream opens a streaming Messages API request and yields model events.
func (c *AnthropicClient) Stream(ctx context.Context, req ModelRequest) (iter.Seq2[ModelEvent, error], error) {
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

		state := anthropicStreamState{
			toolBlocks: make(map[int]*anthropicToolUseState),
		}
		rateLimits := extractAnthropicRateLimits(resp.Header)
		if rateLimits != nil {
			if !yield(ModelEvent{Type: ModelEventRateLimits, RateLimits: rateLimits}, nil) {
				return
			}
		}

		sseBody := sseBodyWithDebug(resp.Body, "anthropic")
		err := readSSE(ctx, sseBody, func(eventName, data string) error {
			return c.handleEvent(eventName, data, &state, yield)
		})
		if err != nil && !errors.Is(err, errStopStream) {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				yield(ModelEvent{}, err)
				return
			}
			yield(ModelEvent{}, err)
		}
	}, nil
}

func extractAnthropicRateLimits(headers http.Header) *RateLimits {
	if len(headers) == 0 {
		return nil
	}

	rateLimits := &RateLimits{
		FiveHour: extractAnthropicRateLimitWindow(headers, "5h"),
		SevenDay: extractAnthropicRateLimitWindow(headers, "7d"),
	}
	if rateLimits.FiveHour == nil && rateLimits.SevenDay == nil {
		return nil
	}
	return rateLimits
}

func extractAnthropicRateLimitWindow(headers http.Header, window string) *RateLimitWindow {
	utilizationText := strings.TrimSpace(headers.Get("anthropic-ratelimit-unified-" + window + "-utilization"))
	resetText := strings.TrimSpace(headers.Get("anthropic-ratelimit-unified-" + window + "-reset"))
	if utilizationText == "" || resetText == "" {
		return nil
	}

	utilization, err := strconv.ParseFloat(utilizationText, 64)
	if err != nil {
		return nil
	}
	resetsAt, err := strconv.ParseInt(resetText, 10, 64)
	if err != nil {
		return nil
	}

	return &RateLimitWindow{
		Utilization: utilization,
		ResetsAt:    resetsAt,
	}
}

func (c *AnthropicClient) openStream(ctx context.Context, payload anthropicRequest, extraHeaders map[string]string) (*http.Response, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal anthropic request: %w", err)
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
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/v1/messages", bytes.NewReader(body))
		if err != nil {
			return fmt.Errorf("create anthropic request: %w", err)
		}
		req.Header.Set("content-type", "application/json")
		req.Header.Set("accept", "text/event-stream")
		req.Header.Set("anthropic-version", anthropicVersion)
		if c.provider == "github-copilot" {
			req.Header.Set("authorization", "Bearer "+apiKey)
			for key, value := range GitHubCopilotStaticHeaders() {
				req.Header.Set(key, value)
			}
		} else {
			req.Header.Set("x-api-key", apiKey)
		}
		for key, value := range extraHeaders {
			req.Header.Set(key, value)
		}

		currentResp, err := c.httpClient.Do(req)
		if err != nil {
			return &APIError{Type: ErrNetwork, Message: fmt.Sprintf("anthropic request failed: %v", err), Err: err}
		}
		if currentResp.StatusCode >= http.StatusMultipleChoices {
			defer currentResp.Body.Close()
			bodyBytes, _ := io.ReadAll(io.LimitReader(currentResp.Body, 1<<20))
			return classifyAnthropicStatus(currentResp.StatusCode, bodyBytes)
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

func (c *AnthropicClient) buildRequest(req ModelRequest) (anthropicRequest, map[string]string, error) {
	maxTokens := req.MaxTokens
	if maxTokens <= 0 {
		maxTokens = c.capabilities.MaxOutputTokens
	}

	systemPrompt, messages, err := buildAnthropicMessages(req.SystemPrompt, req.Messages)
	if err != nil {
		return anthropicRequest{}, nil, err
	}
	tools := buildAnthropicTools(req.Tools, c.provider == "github-copilot", c.capabilities.SupportsCaching)
	if c.capabilities.SupportsCaching {
		applyAnthropicCacheControl(systemPrompt, messages)
	}

	payload := anthropicRequest{
		Model:         c.model,
		System:        systemPrompt,
		Messages:      messages,
		Tools:         tools,
		MaxTokens:     maxTokens,
		Stream:        true,
		StopSequences: req.StopSequences,
	}

	var extraHeaders map[string]string
	if c.provider == "github-copilot" {
		extraHeaders = GitHubCopilotStaticHeaders()
		for key, value := range BuildGitHubCopilotDynamicHeaders(req.Messages) {
			extraHeaders[key] = value
		}
	}

	if req.ThinkingBudget > 0 {
		payload.Thinking = &anthropicThinking{
			Type:         "enabled",
			BudgetTokens: req.ThinkingBudget,
		}
	} else if req.Temperature != nil {
		payload.Temperature = req.Temperature
	}

	return payload, extraHeaders, nil
}

func (c *AnthropicClient) handleEvent(
	eventName, data string,
	state *anthropicStreamState,
	yield func(ModelEvent, error) bool,
) error {
	switch eventName {
	case "", "ping":
		return nil
	case "message_start":
		var evt anthropicMessageStartEvent
		if err := json.Unmarshal([]byte(data), &evt); err != nil {
			return fmt.Errorf("decode anthropic message_start: %w", err)
		}
		if evt.Message.Usage != nil {
			state.usage.merge(*evt.Message.Usage)
			if !yield(ModelEvent{Type: ModelEventUsage, Usage: state.usage.clone()}, nil) {
				return errStopStream
			}
		}
		return nil
	case "content_block_start":
		var evt anthropicContentBlockStartEvent
		if err := json.Unmarshal([]byte(data), &evt); err != nil {
			return fmt.Errorf("decode anthropic content_block_start: %w", err)
		}
		return handleAnthropicBlockStart(evt, state, yield)
	case "content_block_delta":
		var evt anthropicContentBlockDeltaEvent
		if err := json.Unmarshal([]byte(data), &evt); err != nil {
			return fmt.Errorf("decode anthropic content_block_delta: %w", err)
		}
		return handleAnthropicBlockDelta(evt, state, yield)
	case "content_block_stop":
		var evt anthropicContentBlockStopEvent
		if err := json.Unmarshal([]byte(data), &evt); err != nil {
			return fmt.Errorf("decode anthropic content_block_stop: %w", err)
		}
		toolState, ok := state.toolBlocks[evt.Index]
		if !ok {
			return nil
		}
		delete(state.toolBlocks, evt.Index)

		inputJSON, err := toolState.inputJSON()
		if err != nil {
			return err
		}
		if !yield(ModelEvent{
			Type: ModelEventToolCall,
			ToolCall: &ToolCall{
				ID:    toolState.ID,
				Name:  toolState.Name,
				Input: inputJSON,
			},
		}, nil) {
			return errStopStream
		}
		return nil
	case "message_delta":
		var evt anthropicMessageDeltaEvent
		if err := json.Unmarshal([]byte(data), &evt); err != nil {
			return fmt.Errorf("decode anthropic message_delta: %w", err)
		}
		if evt.Delta.StopReason != "" {
			state.stopReason = evt.Delta.StopReason
		}
		if evt.Usage != nil {
			state.usage.merge(*evt.Usage)
			if !yield(ModelEvent{Type: ModelEventUsage, Usage: state.usage.clone()}, nil) {
				return errStopStream
			}
		}
		return nil
	case "message_stop":
		stopReason := state.stopReason
		if stopReason == "" {
			stopReason = "end_turn"
		}
		if !yield(ModelEvent{Type: ModelEventStop, StopReason: stopReason}, nil) {
			return errStopStream
		}
		return errStopStream
	case "error":
		var evt anthropicStreamErrorEvent
		if err := json.Unmarshal([]byte(data), &evt); err != nil {
			return fmt.Errorf("decode anthropic stream error: %w", err)
		}
		return &APIError{
			Type:    classifyAnthropicErrorType(0, evt.Error.Type, evt.Error.Message),
			Message: evt.Error.Message,
		}
	default:
		return nil
	}
}

func handleAnthropicBlockStart(
	evt anthropicContentBlockStartEvent,
	state *anthropicStreamState,
	yield func(ModelEvent, error) bool,
) error {
	switch evt.ContentBlock.Type {
	case "text":
		if evt.ContentBlock.Text == "" {
			return nil
		}
		if !yield(ModelEvent{Type: ModelEventToken, Text: evt.ContentBlock.Text}, nil) {
			return errStopStream
		}
	case "thinking":
		if evt.ContentBlock.Thinking == "" {
			return nil
		}
		if !yield(ModelEvent{Type: ModelEventThinking, Text: evt.ContentBlock.Thinking}, nil) {
			return errStopStream
		}
	case "tool_use":
		state.toolBlocks[evt.Index] = &anthropicToolUseState{
			ID:      evt.ContentBlock.ID,
			Name:    evt.ContentBlock.Name,
			Initial: evt.ContentBlock.Input,
		}
	}
	return nil
}

func handleAnthropicBlockDelta(
	evt anthropicContentBlockDeltaEvent,
	state *anthropicStreamState,
	yield func(ModelEvent, error) bool,
) error {
	switch evt.Delta.Type {
	case "text_delta":
		if evt.Delta.Text == "" {
			return nil
		}
		if !yield(ModelEvent{Type: ModelEventToken, Text: evt.Delta.Text}, nil) {
			return errStopStream
		}
	case "thinking_delta":
		if evt.Delta.Thinking == "" {
			return nil
		}
		if !yield(ModelEvent{Type: ModelEventThinking, Text: evt.Delta.Thinking}, nil) {
			return errStopStream
		}
	case "input_json_delta":
		toolState, ok := state.toolBlocks[evt.Index]
		if !ok {
			return nil
		}
		toolState.Builder.WriteString(evt.Delta.PartialJSON)
	}
	return nil
}

func buildAnthropicMessages(systemPrompt string, messages []Message) ([]anthropicTextBlock, []anthropicMessage, error) {
	system := make([]anthropicTextBlock, 0, len(messages)+1)
	if trimmed := strings.TrimSpace(systemPrompt); trimmed != "" {
		system = append(system, anthropicTextBlock{Type: "text", Text: trimmed})
	}

	built := make([]anthropicMessage, 0, len(messages))
	for _, msg := range messages {
		if msg.Role == RoleSystem {
			if trimmed := strings.TrimSpace(msg.Content); trimmed != "" {
				system = append(system, anthropicTextBlock{Type: "text", Text: trimmed})
			}
			continue
		}

		converted, skip, err := convertAnthropicMessage(msg)
		if err != nil {
			return nil, nil, err
		}
		if skip {
			continue
		}
		built = append(built, converted)
	}

	return system, built, nil
}

func convertAnthropicMessage(msg Message) (anthropicMessage, bool, error) {
	trimmed := strings.TrimSpace(msg.Content)
	blocks := make([]map[string]any, 0, 1+len(msg.ToolCalls)+len(msg.Images))

	switch msg.Role {
	case RoleUser:
		if msg.ToolResult != nil {
			blocks = append(blocks, toolResultBlock(*msg.ToolResult))
		}
		if trimmed != "" {
			blocks = append(blocks, map[string]any{
				"type": "text",
				"text": trimmed,
			})
		}
		for _, image := range msg.Images {
			blocks = append(blocks, map[string]any{
				"type": "image",
				"source": map[string]any{
					"type":       "base64",
					"media_type": image.MediaType,
					"data":       image.Data,
				},
			})
		}
		if len(blocks) == 0 {
			return anthropicMessage{}, true, nil
		}
		return anthropicMessage{Role: "user", Content: blocks}, false, nil
	case RoleTool:
		if msg.ToolResult == nil {
			if trimmed == "" {
				return anthropicMessage{}, true, nil
			}
			return anthropicMessage{Role: "user", Content: []map[string]any{{
				"type": "text",
				"text": trimmed,
			}}}, false, nil
		}
		blocks = append(blocks, toolResultBlock(*msg.ToolResult))
		return anthropicMessage{Role: "user", Content: blocks}, false, nil
	case RoleAssistant:
		if trimmed != "" {
			blocks = append(blocks, map[string]any{
				"type": "text",
				"text": trimmed,
			})
		}
		for _, toolCall := range msg.ToolCalls {
			input, err := decodeToolInput(toolCall.Input)
			if err != nil {
				return anthropicMessage{}, false, err
			}
			blocks = append(blocks, map[string]any{
				"type":  "tool_use",
				"id":    toolCall.ID,
				"name":  toolCall.Name,
				"input": input,
			})
		}
		if len(blocks) == 0 {
			return anthropicMessage{}, true, nil
		}
		return anthropicMessage{Role: "assistant", Content: blocks}, false, nil
	default:
		if trimmed != "" {
			blocks = append(blocks, map[string]any{
				"type": "text",
				"text": trimmed,
			})
		}
		if len(blocks) == 0 {
			return anthropicMessage{}, true, nil
		}
		return anthropicMessage{Role: string(msg.Role), Content: blocks}, false, nil
	}
}

func toolResultBlock(result ToolResult) map[string]any {
	block := map[string]any{
		"type":        "tool_result",
		"tool_use_id": result.ToolCallID,
		"content":     result.Output,
	}
	if result.IsError {
		block["is_error"] = true
	}
	return block
}

func decodeToolInput(input string) (any, error) {
	if strings.TrimSpace(input) == "" {
		return map[string]any{}, nil
	}

	var decoded any
	if err := json.Unmarshal([]byte(input), &decoded); err != nil {
		return nil, fmt.Errorf("decode tool input JSON: %w", err)
	}
	return decoded, nil
}

func buildAnthropicTools(tools []ToolDefinition, flattenTopLevelCombinators bool, enablePromptCaching bool) []anthropicToolDefinition {
	if len(tools) == 0 {
		return nil
	}

	sorted := append([]ToolDefinition(nil), tools...)
	sort.SliceStable(sorted, func(i, j int) bool {
		return sorted[i].Name < sorted[j].Name
	})

	built := make([]anthropicToolDefinition, 0, len(sorted))
	for _, tool := range sorted {
		built = append(built, anthropicToolDefinition{
			Name:        tool.Name,
			Description: tool.Description,
			InputSchema: normalizeAnthropicToolSchema(tool.InputSchema, flattenTopLevelCombinators),
		})
	}
	if enablePromptCaching {
		built[len(built)-1].CacheControl = defaultAnthropicCacheControl()
	}
	return built
}

func applyAnthropicCacheControl(system []anthropicTextBlock, messages []anthropicMessage) {
	if len(system) > 0 {
		system[len(system)-1].CacheControl = defaultAnthropicCacheControl()
	}
	applyAnthropicMessageCacheControl(messages)
}

func applyAnthropicMessageCacheControl(messages []anthropicMessage) {
	for i := len(messages) - 1; i >= 0; i-- {
		blocks, ok := messages[i].Content.([]map[string]any)
		if !ok || len(blocks) == 0 {
			continue
		}
		blocks[len(blocks)-1]["cache_control"] = defaultAnthropicCacheControl()
		return
	}
}

func defaultAnthropicCacheControl() *anthropicCacheControl {
	return &anthropicCacheControl{Type: "ephemeral"}
}

func normalizeAnthropicToolSchema(schema any, flattenTopLevelCombinators bool) any {
	if !flattenTopLevelCombinators {
		return schema
	}
	root, ok := schema.(map[string]any)
	if !ok {
		return schema
	}
	if len(schemaOptionList(root["oneOf"])) == 0 && len(schemaOptionList(root["anyOf"])) == 0 && len(schemaOptionList(root["allOf"])) == 0 {
		return schema
	}

	clone := make(map[string]any, len(root))
	for key, value := range root {
		clone[key] = value
	}
	delete(clone, "oneOf")
	delete(clone, "anyOf")
	delete(clone, "allOf")

	description := strings.TrimSpace(stringValue(clone["description"]))
	note := "Provide arguments using the documented properties. Top-level schema alternatives were flattened for GitHub Copilot compatibility."
	if description == "" {
		clone["description"] = note
	} else {
		clone["description"] = description + " " + note
	}
	return clone
}

func schemaOptionList(value any) []map[string]any {
	items, ok := value.([]map[string]any)
	if ok {
		return items
	}
	rawItems, ok := value.([]any)
	if !ok {
		return nil
	}
	result := make([]map[string]any, 0, len(rawItems))
	for _, item := range rawItems {
		entry, ok := item.(map[string]any)
		if ok {
			result = append(result, entry)
		}
	}
	return result
}

func stringValue(value any) string {
	text, _ := value.(string)
	return text
}

func readSSE(ctx context.Context, body io.Reader, handle func(eventName, data string) error) error {
	stopCancelCloser := closeReadCloserOnCancel(ctx, body)
	defer stopCancelCloser()

	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 0, 64*1024), 2*1024*1024)

	var eventName string
	var dataLines []string

	flush := func() error {
		if len(dataLines) == 0 {
			eventName = ""
			return nil
		}
		data := strings.Join(dataLines, "\n")
		dataLines = dataLines[:0]
		err := handle(eventName, data)
		eventName = ""
		return err
	}

	for scanner.Scan() {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		line := scanner.Text()
		if line == "" {
			if err := flush(); err != nil {
				return err
			}
			continue
		}
		if strings.HasPrefix(line, ":") {
			continue
		}
		switch {
		case strings.HasPrefix(line, "event:"):
			eventName = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
		case strings.HasPrefix(line, "data:"):
			dataLines = append(dataLines, strings.TrimSpace(strings.TrimPrefix(line, "data:")))
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := scanner.Err(); err != nil {
		return &APIError{Type: ErrNetwork, Message: "read anthropic stream", Err: err}
	}
	return flush()
}

func classifyAnthropicStatus(statusCode int, body []byte) error {
	var envelope anthropicErrorEnvelope
	if err := json.Unmarshal(body, &envelope); err != nil {
		message := strings.TrimSpace(string(body))
		if message == "" {
			message = http.StatusText(statusCode)
		}
		return &APIError{
			Type:       classifyAnthropicErrorType(statusCode, "", message),
			StatusCode: statusCode,
			Message:    message,
		}
	}

	message := envelope.Error.Message
	if message == "" {
		message = http.StatusText(statusCode)
	}

	return &APIError{
		Type:       classifyAnthropicErrorType(statusCode, envelope.Error.Type, message),
		StatusCode: statusCode,
		Message:    message,
	}
}

func classifyAnthropicErrorType(statusCode int, errorType, message string) APIErrorType {
	lowerType := strings.ToLower(errorType)
	lowerMessage := strings.ToLower(message)

	switch {
	case statusCode == http.StatusUnauthorized || statusCode == http.StatusForbidden:
		return ErrAuth
	case statusCode == http.StatusTooManyRequests || strings.Contains(lowerType, "rate_limit"):
		return ErrRateLimit
	case statusCode == 529 || statusCode >= http.StatusInternalServerError || strings.Contains(lowerType, "overloaded"):
		return ErrOverloaded
	case strings.Contains(lowerMessage, "prompt is too long") || strings.Contains(lowerMessage, "prompt too long") || strings.Contains(lowerMessage, "context length"):
		return ErrPromptTooLong
	case strings.Contains(lowerMessage, "max tokens"):
		return ErrMaxTokens
	default:
		return ErrUnknown
	}
}

type anthropicRequest struct {
	Model         string                    `json:"model"`
	System        []anthropicTextBlock      `json:"system,omitempty"`
	Messages      []anthropicMessage        `json:"messages"`
	Tools         []anthropicToolDefinition `json:"tools,omitempty"`
	MaxTokens     int                       `json:"max_tokens"`
	Temperature   *float64                  `json:"temperature,omitempty"`
	StopSequences []string                  `json:"stop_sequences,omitempty"`
	Thinking      *anthropicThinking        `json:"thinking,omitempty"`
	Stream        bool                      `json:"stream"`
}

type anthropicThinking struct {
	Type         string `json:"type"`
	BudgetTokens int    `json:"budget_tokens,omitempty"`
}

type anthropicCacheControl struct {
	Type string `json:"type"`
}

type anthropicTextBlock struct {
	Type         string                 `json:"type"`
	Text         string                 `json:"text"`
	CacheControl *anthropicCacheControl `json:"cache_control,omitempty"`
}

type anthropicMessage struct {
	Role    string `json:"role"`
	Content any    `json:"content"`
}

type anthropicToolDefinition struct {
	Name         string                 `json:"name"`
	Description  string                 `json:"description,omitempty"`
	InputSchema  any                    `json:"input_schema"`
	CacheControl *anthropicCacheControl `json:"cache_control,omitempty"`
}

type anthropicUsage struct {
	InputTokens         int `json:"input_tokens,omitempty"`
	OutputTokens        int `json:"output_tokens,omitempty"`
	CacheReadTokens     int `json:"cache_read_input_tokens,omitempty"`
	CacheCreationTokens int `json:"cache_creation_input_tokens,omitempty"`
}

func (u *anthropicUsage) merge(other anthropicUsage) {
	if other.InputTokens > 0 {
		u.InputTokens = other.InputTokens
	}
	if other.OutputTokens > 0 {
		u.OutputTokens = other.OutputTokens
	}
	if other.CacheReadTokens > 0 {
		u.CacheReadTokens = other.CacheReadTokens
	}
	if other.CacheCreationTokens > 0 {
		u.CacheCreationTokens = other.CacheCreationTokens
	}
}

func (u anthropicUsage) clone() *Usage {
	return &Usage{
		InputTokens:         u.InputTokens,
		OutputTokens:        u.OutputTokens,
		CacheReadTokens:     u.CacheReadTokens,
		CacheCreationTokens: u.CacheCreationTokens,
	}
}

type anthropicMessageStartEvent struct {
	Message struct {
		Usage *anthropicUsage `json:"usage,omitempty"`
	} `json:"message"`
}

type anthropicContentBlockStartEvent struct {
	Index        int                   `json:"index"`
	ContentBlock anthropicContentBlock `json:"content_block"`
}

type anthropicContentBlock struct {
	Type     string          `json:"type"`
	Text     string          `json:"text,omitempty"`
	Thinking string          `json:"thinking,omitempty"`
	ID       string          `json:"id,omitempty"`
	Name     string          `json:"name,omitempty"`
	Input    json.RawMessage `json:"input,omitempty"`
}

type anthropicContentBlockDeltaEvent struct {
	Index int                 `json:"index"`
	Delta anthropicBlockDelta `json:"delta"`
}

type anthropicBlockDelta struct {
	Type        string `json:"type"`
	Text        string `json:"text,omitempty"`
	Thinking    string `json:"thinking,omitempty"`
	PartialJSON string `json:"partial_json,omitempty"`
}

type anthropicContentBlockStopEvent struct {
	Index int `json:"index"`
}

type anthropicMessageDeltaEvent struct {
	Delta struct {
		StopReason string `json:"stop_reason,omitempty"`
	} `json:"delta"`
	Usage *anthropicUsage `json:"usage,omitempty"`
}

type anthropicStreamErrorEvent struct {
	Error struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error"`
}

type anthropicErrorEnvelope struct {
	Error struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error"`
}

type anthropicStreamState struct {
	usage      anthropicUsage
	stopReason string
	toolBlocks map[int]*anthropicToolUseState
}

type anthropicToolUseState struct {
	ID      string
	Name    string
	Initial json.RawMessage
	Builder strings.Builder
}

func (s *anthropicToolUseState) inputJSON() (string, error) {
	if s.Builder.Len() > 0 {
		var decoded any
		if err := json.Unmarshal([]byte(s.Builder.String()), &decoded); err != nil {
			return "", fmt.Errorf("decode anthropic tool input delta: %w", err)
		}
		encoded, err := json.Marshal(decoded)
		if err != nil {
			return "", fmt.Errorf("encode anthropic tool input delta: %w", err)
		}
		return string(encoded), nil
	}
	if len(s.Initial) > 0 {
		var decoded any
		if err := json.Unmarshal(s.Initial, &decoded); err != nil {
			return "", fmt.Errorf("decode anthropic tool input: %w", err)
		}
		encoded, err := json.Marshal(decoded)
		if err != nil {
			return "", fmt.Errorf("encode anthropic tool input: %w", err)
		}
		return string(encoded), nil
	}
	return "{}", nil
}

func newAnthropicClientForTesting(model, apiKey, baseURL string, httpClient *http.Client) *AnthropicClient {
	preset := Presets["anthropic"]
	return &AnthropicClient{
		model:        model,
		baseURL:      strings.TrimRight(baseURL, "/"),
		apiKey:       apiKey,
		httpClient:   httpClient,
		capabilities: preset.Capabilities,
	}
}

func init() {
	_ = time.Second
}
