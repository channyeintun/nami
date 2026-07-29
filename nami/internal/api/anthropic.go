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
	"strconv"
	"strings"
	"sync"
)

const anthropicVersion = "2023-06-01"

var errStopStream = errors.New("stop stream")

// AnthropicClient implements the Anthropic Messages API streaming protocol.
type AnthropicClient struct {
	provider         string
	model            string
	baseURL          string
	enterpriseDomain string
	apiKey           string
	apiKeyFunc       func() (string, error)
	httpClient       *http.Client
	capabilities     ModelCapabilities
}

// SetAPIKeyFunc sets a callback that returns a fresh API key on each call.
// When set, the client calls this instead of using the static apiKey.
func (c *AnthropicClient) SetAPIKeyFunc(fn func() (string, error)) {
	c.apiKeyFunc = fn
}

func (c *AnthropicClient) SetGitHubCopilotEnterpriseDomain(domain string) {
	c.enterpriseDomain = strings.TrimSpace(domain)
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
	if apiKey == "" && provider != "github-copilot" {
		return nil, &APIError{Type: ErrAuth, Message: fmt.Sprintf("missing API key for provider %q", provider)}
	}

	return &AnthropicClient{
		provider:     provider,
		model:        model,
		baseURL:      strings.TrimRight(baseURL, "/"),
		apiKey:       apiKey,
		httpClient:   newHTTPClient(),
		capabilities: ResolveModelCapabilities(provider, model),
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
	baseURL := c.resolveBaseURL(apiKey)
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

	return issueWarmupRequest(ctx, c.httpClient, http.MethodHead, baseURL+"/v1/messages", headers)
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

	return &RateLimitWindow{Utilization: utilization, ResetsAt: resetsAt}
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
		baseURL := c.resolveBaseURL(apiKey)
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/v1/messages", bytes.NewReader(body))
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
		if !yield(ModelEvent{Type: ModelEventToolCall, ToolCall: &ToolCall{ID: toolState.ID, Name: toolState.Name, Input: inputJSON}}, nil) {
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
		return &APIError{Type: classifyAnthropicErrorType(0, evt.Error.Type, evt.Error.Message), Message: evt.Error.Message}
	default:
		return nil
	}
}

func handleAnthropicBlockStart(evt anthropicContentBlockStartEvent, state *anthropicStreamState, yield func(ModelEvent, error) bool) error {
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

func handleAnthropicBlockDelta(evt anthropicContentBlockDeltaEvent, state *anthropicStreamState, yield func(ModelEvent, error) bool) error {
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
