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
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// GeminiClient implements the native Gemini streaming GenerateContent API.
type GeminiClient struct {
	model        string
	baseURL      string
	apiKey       string
	httpClient   *http.Client
	capabilities ModelCapabilities
}

// NewGeminiClient constructs a native Gemini streaming client.
func NewGeminiClient(model, apiKey, baseURL string) (*GeminiClient, error) {
	preset := Presets["gemini"]
	if model == "" {
		model = preset.DefaultModel
	}
	if baseURL == "" {
		baseURL = preset.BaseURL
	}
	warnCustomBaseURL("gemini", preset.BaseURL, baseURL)
	if apiKey == "" {
		apiKey = os.Getenv(preset.EnvKeyVar)
	}
	if apiKey == "" {
		return nil, &APIError{Type: ErrAuth, Message: "missing Gemini API key"}
	}

	return &GeminiClient{
		model:        model,
		baseURL:      strings.TrimRight(baseURL, "/"),
		apiKey:       apiKey,
		httpClient:   newHTTPClient(),
		capabilities: preset.Capabilities,
	}, nil
}

// ModelID returns the active model identifier.
func (c *GeminiClient) ModelID() string {
	return c.model
}

// Capabilities reports Gemini model capabilities.
func (c *GeminiClient) Capabilities() ModelCapabilities {
	return c.capabilities
}

// Warmup preconnects the Gemini transport before the first streaming request.
func (c *GeminiClient) Warmup(ctx context.Context) error {
	return issueWarmupRequest(ctx, c.httpClient, http.MethodHead, c.baseURL+"/models", map[string]string{
		"accept":         "application/json",
		"x-goog-api-key": c.apiKey,
	})
}

// geminiMaxEmptyRetries is the number of extra attempts when Gemini returns a
// 200 OK but an empty SSE body (no events). Each retry doubles the backoff
// starting at 500 ms.
const geminiMaxEmptyRetries = 2

// Stream opens a Gemini streamGenerateContent request and yields model events.
func (c *GeminiClient) Stream(ctx context.Context, req ModelRequest) (iter.Seq2[ModelEvent, error], error) {
	payload, err := c.buildRequest(req)
	if err != nil {
		return nil, err
	}

	return func(yield func(ModelEvent, error) bool) {
		for attempt := 0; attempt <= geminiMaxEmptyRetries; attempt++ {
			if attempt > 0 {
				delay := time.Duration(500<<uint(attempt-1)) * time.Millisecond
				select {
				case <-ctx.Done():
					yield(ModelEvent{}, ctx.Err())
					return
				case <-time.After(delay):
				}
			}

			resp, err := c.openStream(ctx, payload)
			if err != nil {
				yield(ModelEvent{}, err)
				return
			}

			state := geminiStreamState{}
			eventCount := 0
			sseBody := sseBodyWithDebug(resp.Body, "gemini")
			sseErr := readSSE(ctx, sseBody, func(_ string, data string) error {
				eventCount++
				return c.handleEvent(data, &state, yield)
			})
			resp.Body.Close()

			if sseErr != nil && !errors.Is(sseErr, errStopStream) {
				yield(ModelEvent{}, sseErr)
				return
			}

			// Empty stream — retry if attempts remain.
			if eventCount == 0 && attempt < geminiMaxEmptyRetries {
				continue
			}
			if eventCount == 0 {
				yield(ModelEvent{}, &APIError{Type: ErrOverloaded, Message: "Gemini returned an empty response stream"})
			}
			return
		}
	}, nil
}

// geminiMaxRetryAfter caps the server-specified Retry-After delay.
// If the server asks us to wait longer than this, we fail immediately.
const geminiMaxRetryAfter = 60 * time.Second

// geminiRetryAfterDelay extracts the server-requested retry delay from a
// 429 (or 503) response. Sources are checked in priority order:
//  1. Retry-After header (seconds integer or HTTP-date)
//  2. X-RateLimit-Reset header (unix timestamp seconds)
//  3. X-RateLimit-Reset-After header (seconds float)
//  4. Body text: "Please retry in Xs" or retryDelay JSON field
//
// Returns 0 if no usable delay hint is found.
var geminiRetryDelayBodyRe = regexp.MustCompile(`(?i)(?:please retry in\s+(\d+(?:\.\d+)?)\s*s|"retryDelay"\s*:\s*"(\d+(?:\.\d+)?)s")`)

func geminiRetryAfterDelay(resp *http.Response, body []byte) time.Duration {
	// 1. Retry-After
	if v := resp.Header.Get("Retry-After"); v != "" {
		if secs, err := strconv.ParseFloat(strings.TrimSpace(v), 64); err == nil && secs > 0 {
			return time.Duration(secs * float64(time.Second))
		}
		if t, err := http.ParseTime(v); err == nil {
			if d := time.Until(t); d > 0 {
				return d
			}
		}
	}
	// 2. X-RateLimit-Reset (unix timestamp)
	if v := resp.Header.Get("X-RateLimit-Reset"); v != "" {
		if ts, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64); err == nil && ts > 0 {
			if d := time.Until(time.Unix(ts, 0)); d > 0 {
				return d
			}
		}
	}
	// 3. X-RateLimit-Reset-After (seconds)
	if v := resp.Header.Get("X-RateLimit-Reset-After"); v != "" {
		if secs, err := strconv.ParseFloat(strings.TrimSpace(v), 64); err == nil && secs > 0 {
			return time.Duration(secs * float64(time.Second))
		}
	}
	// 4. Body patterns
	if m := geminiRetryDelayBodyRe.FindSubmatch(body); m != nil {
		raw := string(m[1])
		if raw == "" {
			raw = string(m[2])
		}
		if secs, err := strconv.ParseFloat(raw, 64); err == nil && secs > 0 {
			return time.Duration(secs * float64(time.Second))
		}
	}
	return 0
}

func (c *GeminiClient) openStream(ctx context.Context, payload geminiGenerateContentRequest) (*http.Response, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal Gemini request: %w", err)
	}

	var (
		resp *http.Response
		mu   sync.Mutex
	)

	err = RetryWithBackoff(ctx, DefaultRetryPolicy(), func() error {
		endpoint := fmt.Sprintf("%s/models/%s:streamGenerateContent?alt=sse", c.baseURL, url.PathEscape(geminiModelName(c.model)))
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
		if err != nil {
			return fmt.Errorf("create Gemini request: %w", err)
		}
		req.Header.Set("content-type", "application/json")
		req.Header.Set("accept", "text/event-stream")
		req.Header.Set("x-goog-api-key", c.apiKey)

		currentResp, err := c.httpClient.Do(req)
		if err != nil {
			return &APIError{Type: ErrNetwork, Message: "Gemini request failed", Err: err}
		}
		if currentResp.StatusCode >= http.StatusMultipleChoices {
			defer currentResp.Body.Close()
			bodyBytes, _ := io.ReadAll(io.LimitReader(currentResp.Body, 1<<20))
			apiErr := classifyGeminiStatus(currentResp.StatusCode, bodyBytes)
			// Attach server-specified retry delay so RetryWithBackoff honours it.
			if d := geminiRetryAfterDelay(currentResp, bodyBytes); d > 0 {
				var ae *APIError
				if errors.As(apiErr, &ae) {
					if d > geminiMaxRetryAfter {
						// Server wants us to wait too long — fail immediately.
						ae.RetryAfter = 0
						return ae
					}
					ae.RetryAfter = d
				}
			}
			return apiErr
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

func (c *GeminiClient) buildRequest(req ModelRequest) (geminiGenerateContentRequest, error) {
	contents, systemInstruction, err := buildGeminiContents(c.model, req.SystemPrompt, req.Messages)
	if err != nil {
		return geminiGenerateContentRequest{}, err
	}

	maxTokens := req.MaxTokens
	if maxTokens <= 0 {
		maxTokens = c.capabilities.MaxOutputTokens
	}

	payload := geminiGenerateContentRequest{
		Contents:          contents,
		SystemInstruction: systemInstruction,
		Tools:             buildGeminiTools(req.Tools),
		GenerationConfig: geminiGenerationConfig{
			MaxOutputTokens: maxTokens,
			Temperature:     req.Temperature,
			StopSequences:   req.StopSequences,
		},
	}

	return payload, nil
}

func (c *GeminiClient) handleEvent(
	data string,
	state *geminiStreamState,
	yield func(ModelEvent, error) bool,
) error {
	trimmed := strings.TrimSpace(data)
	if trimmed == "" {
		return nil
	}

	var response geminiGenerateContentResponse
	if err := json.Unmarshal([]byte(trimmed), &response); err != nil {
		return fmt.Errorf("decode Gemini stream chunk: %w", err)
	}
	if response.Error != nil {
		return &APIError{
			Type:    classifyGeminiErrorType(0, response.Error.Status, response.Error.Message),
			Message: response.Error.Message,
		}
	}
	if response.UsageMetadata != nil {
		state.usage.merge(response.UsageMetadata)
		if !yield(ModelEvent{Type: ModelEventUsage, Usage: state.usage.toUsage()}, nil) {
			return errStopStream
		}
	}

	for _, candidate := range response.Candidates {
		for _, part := range candidate.Content.Parts {
			switch {
			case part.FunctionCall != nil:
				input, err := json.Marshal(part.FunctionCall.Args)
				if err != nil {
					return fmt.Errorf("encode Gemini function call args: %w", err)
				}
				if !yield(ModelEvent{
					Type: ModelEventToolCall,
					ToolCall: &ToolCall{
						ID:               firstNonEmpty(part.FunctionCall.ID, part.FunctionCall.Name),
						Name:             part.FunctionCall.Name,
						Input:            string(input),
						ThoughtSignature: part.ThoughtSignature,
					},
				}, nil) {
					return errStopStream
				}
			case part.Text != "" && part.Thought:
				if !yield(ModelEvent{Type: ModelEventThinking, Text: part.Text}, nil) {
					return errStopStream
				}
			case part.Text != "":
				if !yield(ModelEvent{Type: ModelEventToken, Text: part.Text}, nil) {
					return errStopStream
				}
			}
		}

		if candidate.FinishReason != "" {
			state.stopReason = mapGeminiStopReason(candidate.FinishReason)
		}
	}

	if response.PromptFeedback != nil && response.PromptFeedback.BlockReason != "" {
		state.stopReason = mapGeminiStopReason(response.PromptFeedback.BlockReason)
	}

	if state.stopReason != "" && !state.sentStop {
		state.sentStop = true
		if !yield(ModelEvent{Type: ModelEventStop, StopReason: state.stopReason}, nil) {
			return errStopStream
		}
		return errStopStream
	}

	return nil
}

// geminiContentIsOnlyFunctionResponses reports whether all parts in c are
// functionResponse parts (no text, inlineData, or functionCall parts).
func geminiContentIsOnlyFunctionResponses(c geminiContent) bool {
	if len(c.Parts) == 0 {
		return false
	}
	for _, p := range c.Parts {
		if p.FunctionResponse == nil {
			return false
		}
	}
	return true
}

func buildGeminiContents(modelID, systemPrompt string, messages []Message) ([]geminiContent, *geminiContent, error) {
	systemParts := make([]geminiPart, 0, len(messages)+1)
	if trimmed := strings.TrimSpace(systemPrompt); trimmed != "" {
		systemParts = append(systemParts, geminiPart{Text: trimmed})
	}

	// Build a map from toolCallID → toolName so function responses can reference
	// the correct function name rather than the opaque call ID.
	toolNames := make(map[string]string)
	for _, msg := range messages {
		for _, tc := range msg.ToolCalls {
			if tc.ID != "" && tc.Name != "" {
				toolNames[tc.ID] = tc.Name
			}
		}
	}

	contents := make([]geminiContent, 0, len(messages))

	// Track tool calls emitted by the most recent model turn that have not yet
	// been answered by a tool-result message. If a new model turn or user text
	// turn arrives before all pending tool calls are resolved, synthetic error
	// responses are injected so Gemini always sees a balanced call/response history.
	type pendingCall struct{ id, name string }
	var pendingCalls []pendingCall
	pendingCallIDs := map[string]struct{}{}

	flushOrphanedCalls := func() {
		if len(pendingCalls) == 0 {
			return
		}
		for _, pc := range pendingCalls {
			if _, resolved := pendingCallIDs[pc.id]; resolved {
				continue
			}
			name := pc.name
			if name == "" {
				name = pc.id
			}
			part := geminiPart{
				FunctionResponse: &geminiFunctionResponse{
					ID:       pc.id,
					Name:     name,
					Response: map[string]any{"error": "No result provided"},
				},
			}
			if len(contents) > 0 {
				last := &contents[len(contents)-1]
				if last.Role == "user" && geminiContentIsOnlyFunctionResponses(*last) {
					last.Parts = append(last.Parts, part)
					continue
				}
			}
			contents = append(contents, geminiContent{Role: "user", Parts: []geminiPart{part}})
		}
		pendingCalls = pendingCalls[:0]
		pendingCallIDs = map[string]struct{}{}
	}

	for _, msg := range messages {
		if msg.Role == RoleSystem {
			if trimmed := strings.TrimSpace(msg.Content); trimmed != "" {
				systemParts = append(systemParts, geminiPart{Text: trimmed})
			}
			continue
		}

		// A new model turn or a user text message means any outstanding tool calls
		// from the previous model turn will never receive results — inject synthetics.
		isUserText := (msg.Role == RoleUser || msg.Role == RoleTool) && msg.ToolResult == nil &&
			strings.TrimSpace(msg.Content) != ""
		if msg.Role == RoleAssistant || isUserText {
			flushOrphanedCalls()
		}

		// Track tool calls about to be added.
		if msg.Role == RoleAssistant && len(msg.ToolCalls) > 0 {
			for _, tc := range msg.ToolCalls {
				if tc.ID != "" {
					pendingCalls = append(pendingCalls, pendingCall{id: tc.ID, name: tc.Name})
				}
			}
		}

		// Mark resolved tool call IDs.
		if msg.ToolResult != nil && msg.ToolResult.ToolCallID != "" {
			pendingCallIDs[msg.ToolResult.ToolCallID] = struct{}{}
		}

		converted, err := convertGeminiMessage(msg, toolNames)
		if err != nil {
			return nil, nil, err
		}
		for _, c := range converted {
			// Gemini requires all functionResponse parts for a given model turn to be
			// in a single user Content. Merge consecutive tool-result user turns so
			// parallel tool results don't produce separate Content entries.
			if c.Role == "user" && geminiContentIsOnlyFunctionResponses(c) && len(contents) > 0 {
				last := &contents[len(contents)-1]
				if last.Role == "user" && geminiContentIsOnlyFunctionResponses(*last) {
					last.Parts = append(last.Parts, c.Parts...)
					continue
				}
			}
			contents = append(contents, c)
		}
	}

	var instruction *geminiContent
	if len(systemParts) > 0 {
		instruction = &geminiContent{Role: "system", Parts: systemParts}
	}
	contents = ensureGeminiActiveLoopThoughtSignatures(contents, modelID)
	return contents, instruction, nil
}

const geminiSyntheticThoughtSignature = "skip_thought_signature_validator"

// geminiMajorVersion extracts the major version number from a Gemini model ID
// (e.g. "gemini-2.5-flash" → 2, "gemini-3.0-pro" → 3). Returns 0 if unparseable.
func geminiMajorVersion(modelID string) int {
	lower := strings.ToLower(modelID)
	// Strip optional "models/" prefix and match "gemini[-live]-<major>"
	lower = strings.TrimPrefix(lower, "models/")
	for _, prefix := range []string{"gemini-live-", "gemini-"} {
		if !strings.HasPrefix(lower, prefix) {
			continue
		}
		rest := lower[len(prefix):]
		// Parse leading digit(s)
		i := 0
		for i < len(rest) && rest[i] >= '0' && rest[i] <= '9' {
			i++
		}
		if i == 0 {
			return 0
		}
		v := 0
		for _, ch := range rest[:i] {
			v = v*10 + int(ch-'0')
		}
		return v
	}
	return 0
}

func ensureGeminiActiveLoopThoughtSignatures(contents []geminiContent, modelID string) []geminiContent {
	activeLoopStart := -1
	for index := len(contents) - 1; index >= 0; index-- {
		content := contents[index]
		if content.Role != "user" {
			continue
		}
		for _, part := range content.Parts {
			if strings.TrimSpace(part.Text) != "" {
				activeLoopStart = index
				break
			}
		}
		if activeLoopStart >= 0 {
			break
		}
	}

	if activeLoopStart < 0 {
		return contents
	}

	updated := make([]geminiContent, len(contents))
	copy(updated, contents)
	for index := activeLoopStart; index < len(updated); index++ {
		content := updated[index]
		if content.Role != "model" || len(content.Parts) == 0 {
			continue
		}
		parts := make([]geminiPart, len(content.Parts))
		copy(parts, content.Parts)
		for partIndex := range parts {
			if parts[partIndex].FunctionCall == nil {
				continue
			}
			// Only inject the sentinel on Gemini 3+ where thought signatures are mandatory.
			// On Gemini 2.x, omitting the signature is valid and the sentinel is unnecessary.
			if strings.TrimSpace(parts[partIndex].ThoughtSignature) == "" && geminiMajorVersion(modelID) >= 3 {
				parts[partIndex].ThoughtSignature = geminiSyntheticThoughtSignature
				updated[index] = geminiContent{Role: content.Role, Parts: parts}
			}
			break
		}
	}

	return updated
}

func convertGeminiMessage(msg Message, toolNames map[string]string) ([]geminiContent, error) {
	trimmed := strings.TrimSpace(msg.Content)
	parts := make([]geminiPart, 0, 1+len(msg.ToolCalls)+len(msg.Images))

	switch msg.Role {
	case RoleUser:
		if trimmed != "" {
			parts = append(parts, geminiPart{Text: trimmed})
		}
		for _, image := range msg.Images {
			parts = append(parts, geminiPart{
				InlineData: &geminiInlineData{
					MimeType: image.MediaType,
					Data:     image.Data,
				},
			})
		}
		if msg.ToolResult != nil {
			resultPart, err := geminiFunctionResponsePart(*msg.ToolResult, toolNames)
			if err != nil {
				return nil, err
			}
			parts = append(parts, resultPart)
		}
		if len(parts) == 0 {
			return nil, nil
		}
		return []geminiContent{{Role: "user", Parts: parts}}, nil
	case RoleTool:
		if msg.ToolResult == nil {
			if trimmed == "" {
				return nil, nil
			}
			return []geminiContent{{Role: "user", Parts: []geminiPart{{Text: trimmed}}}}, nil
		}
		resultPart, err := geminiFunctionResponsePart(*msg.ToolResult, toolNames)
		if err != nil {
			return nil, err
		}
		return []geminiContent{{Role: "user", Parts: []geminiPart{resultPart}}}, nil
	case RoleAssistant:
		if trimmed != "" {
			parts = append(parts, geminiPart{Text: trimmed})
		}
		for _, toolCall := range msg.ToolCalls {
			args, err := decodeToolInput(toolCall.Input)
			if err != nil {
				return nil, err
			}
			parts = append(parts, geminiPart{
				ThoughtSignature: toolCall.ThoughtSignature,
				FunctionCall: &geminiFunctionCall{
					ID:   toolCall.ID,
					Name: toolCall.Name,
					Args: args,
				},
			})
		}
		if len(parts) == 0 {
			return nil, nil
		}
		return []geminiContent{{Role: "model", Parts: parts}}, nil
	default:
		if trimmed == "" {
			return nil, nil
		}
		return []geminiContent{{Role: string(msg.Role), Parts: []geminiPart{{Text: trimmed}}}}, nil
	}
}

func geminiFunctionResponsePart(result ToolResult, toolNames map[string]string) (geminiPart, error) {
	response := map[string]any{}
	text := strings.TrimSpace(result.Output)
	if text != "" {
		var decoded any
		if err := json.Unmarshal([]byte(result.Output), &decoded); err == nil {
			// Use "error" key for error results, "output" key for success — Gemini convention.
			if result.IsError {
				response["error"] = decoded
			} else {
				response["output"] = decoded
			}
		} else {
			if result.IsError {
				response["error"] = result.Output
			} else {
				response["output"] = result.Output
			}
		}
	}
	// Gemini requires the actual function name, not the opaque call ID.
	name := toolNames[result.ToolCallID]
	if name == "" {
		name = result.ToolCallID
	}
	return geminiPart{
		FunctionResponse: &geminiFunctionResponse{
			ID:       result.ToolCallID,
			Name:     name,
			Response: response,
		},
	}, nil
}

func buildGeminiTools(tools []ToolDefinition) []geminiTool {
	if len(tools) == 0 {
		return nil
	}

	decls := make([]geminiFunctionDeclaration, 0, len(tools))
	for _, tool := range tools {
		decls = append(decls, geminiFunctionDeclaration{
			Name:                 tool.Name,
			Description:          tool.Description,
			ParametersJsonSchema: sanitizeGeminiSchema(tool.InputSchema),
		})
	}

	return []geminiTool{{FunctionDeclarations: decls}}
}

func sanitizeGeminiSchema(schema any) any {
	sanitized := sanitizeGeminiSchemaValue(schema)
	if schemaMap, ok := sanitized.(map[string]any); ok {
		return schemaMap
	}
	return schema
}

func sanitizeGeminiSchemaValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		return sanitizeGeminiSchemaMap(typed)
	case []map[string]any:
		items := make([]any, 0, len(typed))
		for _, item := range typed {
			items = append(items, sanitizeGeminiSchemaMap(item))
		}
		return items
	case []any:
		items := make([]any, 0, len(typed))
		for _, item := range typed {
			items = append(items, sanitizeGeminiSchemaValue(item))
		}
		return items
	default:
		return value
	}
}

func sanitizeGeminiSchemaMap(schema map[string]any) map[string]any {
	sanitized := make(map[string]any, len(schema))
	for key, value := range schema {
		switch key {
		case "$schema", "additionalProperties", "default":
			continue
		default:
			sanitized[key] = sanitizeGeminiSchemaValue(value)
		}
	}

	properties, hasProperties := sanitized["properties"].(map[string]any)
	required := schemaStringValues(sanitized["required"])

	if hasProperties {
		if aliasFields, ok := geminiRequiredFieldsFromAnyOf(sanitized["anyOf"], properties); ok {
			required = appendUniqueStrings(required, aliasFields...)
			delete(sanitized, "anyOf")
		}
		if aliasFields, ok := geminiRequiredFieldsFromAllOf(sanitized["allOf"], properties); ok {
			required = appendUniqueStrings(required, aliasFields...)
			delete(sanitized, "allOf")
		}
		required = filterRequiredProperties(required, properties)
		if len(required) > 0 {
			sanitized["required"] = required
		} else {
			delete(sanitized, "required")
		}
	}

	if typeName, _ := sanitized["type"].(string); typeName != "" && typeName != "object" {
		delete(sanitized, "properties")
		delete(sanitized, "required")
	}

	return sanitized
}

func geminiRequiredFieldsFromAllOf(value any, properties map[string]any) ([]string, bool) {
	children := schemaMapValues(value)
	if len(children) == 0 {
		return nil, false
	}

	required := make([]string, 0, len(children))
	for _, child := range children {
		if field, ok := geminiSingleRequiredField(child, properties); ok {
			required = appendUniqueStrings(required, field)
			continue
		}
		if aliasFields, ok := geminiRequiredFieldsFromAnyOf(child["anyOf"], properties); ok {
			required = appendUniqueStrings(required, aliasFields...)
			continue
		}
		return nil, false
	}

	return required, len(required) > 0
}

func geminiRequiredFieldsFromAnyOf(value any, properties map[string]any) ([]string, bool) {
	children := schemaMapValues(value)
	if len(children) == 0 {
		return nil, false
	}

	candidates := make([]string, 0, len(children))
	for _, child := range children {
		field, ok := geminiSingleRequiredField(child, properties)
		if !ok {
			return nil, false
		}
		candidates = append(candidates, field)
	}

	chosen := pickGeminiPreferredField(candidates, properties)
	if chosen == "" {
		return nil, false
	}
	return []string{chosen}, true
}

func geminiSingleRequiredField(schema map[string]any, properties map[string]any) (string, bool) {
	for key, value := range schema {
		switch key {
		case "required":
			continue
		case "type":
			if typeName, _ := value.(string); typeName != "" && typeName != "object" {
				return "", false
			}
		default:
			return "", false
		}
	}

	required := schemaStringValues(schema["required"])
	if len(required) != 1 {
		return "", false
	}
	field := required[0]
	if _, ok := properties[field]; !ok {
		return "", false
	}
	return field, true
}

func filterRequiredProperties(required []string, properties map[string]any) []string {
	filtered := make([]string, 0, len(required))
	for _, name := range required {
		if _, ok := properties[name]; ok {
			filtered = appendUniqueStrings(filtered, name)
		}
	}
	return filtered
}

func schemaMapValues(value any) []map[string]any {
	switch typed := value.(type) {
	case []map[string]any:
		return typed
	case []any:
		children := make([]map[string]any, 0, len(typed))
		for _, item := range typed {
			child, ok := item.(map[string]any)
			if ok {
				children = append(children, child)
			}
		}
		return children
	default:
		return nil
	}
}

func schemaStringValues(value any) []string {
	switch typed := value.(type) {
	case []string:
		return typed
	case []any:
		values := make([]string, 0, len(typed))
		for _, item := range typed {
			text, ok := item.(string)
			if ok {
				values = append(values, text)
			}
		}
		return values
	default:
		return nil
	}
}

func appendUniqueStrings(existing []string, values ...string) []string {
	seen := make(map[string]struct{}, len(existing))
	for _, item := range existing {
		seen[item] = struct{}{}
	}
	for _, item := range values {
		if _, ok := seen[item]; ok || item == "" {
			continue
		}
		existing = append(existing, item)
		seen[item] = struct{}{}
	}
	return existing
}

func pickGeminiPreferredField(candidates []string, properties map[string]any) string {
	best := ""
	bestRank := 100
	for _, candidate := range candidates {
		if _, ok := properties[candidate]; !ok {
			continue
		}
		rank := geminiFieldRank(candidate)
		if best == "" || rank < bestRank || (rank == bestRank && len(candidate) < len(best)) || (rank == bestRank && len(candidate) == len(best) && candidate < best) {
			best = candidate
			bestRank = rank
		}
	}
	return best
}

func geminiFieldRank(name string) int {
	switch {
	case isLowerAlphaNumeric(name):
		return 0
	case isSnakeCase(name):
		return 1
	case isLowerCamelCase(name):
		return 2
	case isUpperCamelCase(name):
		return 3
	default:
		return 4
	}
}

func isLowerAlphaNumeric(value string) bool {
	if value == "" {
		return false
	}
	for _, ch := range value {
		if ch >= 'a' && ch <= 'z' {
			continue
		}
		if ch >= '0' && ch <= '9' {
			continue
		}
		return false
	}
	return true
}

func isSnakeCase(value string) bool {
	if value == "" {
		return false
	}
	hasUnderscore := false
	for _, ch := range value {
		switch {
		case ch == '_':
			hasUnderscore = true
		case ch >= 'a' && ch <= 'z':
		case ch >= '0' && ch <= '9':
		default:
			return false
		}
	}
	return hasUnderscore
}

func isLowerCamelCase(value string) bool {
	if value == "" {
		return false
	}
	first := rune(value[0])
	if first < 'a' || first > 'z' {
		return false
	}
	for _, ch := range value[1:] {
		switch {
		case ch >= 'a' && ch <= 'z':
		case ch >= 'A' && ch <= 'Z':
		case ch >= '0' && ch <= '9':
		default:
			return false
		}
	}
	return true
}

func isUpperCamelCase(value string) bool {
	if value == "" {
		return false
	}
	first := rune(value[0])
	if first < 'A' || first > 'Z' {
		return false
	}
	for _, ch := range value[1:] {
		switch {
		case ch >= 'a' && ch <= 'z':
		case ch >= 'A' && ch <= 'Z':
		case ch >= '0' && ch <= '9':
		default:
			return false
		}
	}
	return true
}

func geminiModelName(model string) string {
	return strings.TrimPrefix(model, "models/")
}

func classifyGeminiStatus(statusCode int, body []byte) error {
	var envelope geminiErrorEnvelope
	if err := json.Unmarshal(body, &envelope); err != nil || envelope.Error == nil {
		message := strings.TrimSpace(string(body))
		if message == "" {
			message = http.StatusText(statusCode)
		}
		return &APIError{
			Type:       classifyGeminiErrorType(statusCode, "", message),
			StatusCode: statusCode,
			Message:    message,
		}
	}

	message := envelope.Error.Message
	if message == "" {
		message = http.StatusText(statusCode)
	}
	return &APIError{
		Type:       classifyGeminiErrorType(statusCode, envelope.Error.Status, message),
		StatusCode: statusCode,
		Message:    message,
	}
}

func classifyGeminiErrorType(statusCode int, status, message string) APIErrorType {
	lowerStatus := strings.ToLower(status)
	lowerMessage := strings.ToLower(message)

	switch {
	case statusCode == http.StatusUnauthorized || statusCode == http.StatusForbidden || strings.Contains(lowerStatus, "permission"):
		return ErrAuth
	case statusCode == http.StatusTooManyRequests || strings.Contains(lowerStatus, "resource_exhausted"):
		return ErrRateLimit
	case statusCode >= http.StatusInternalServerError || strings.Contains(lowerStatus, "unavailable"):
		return ErrOverloaded
	case strings.Contains(lowerMessage, "token") && strings.Contains(lowerMessage, "limit"):
		return ErrPromptTooLong
	case strings.Contains(lowerMessage, "max output tokens"):
		return ErrMaxTokens
	default:
		return ErrUnknown
	}
}

func mapGeminiStopReason(reason string) string {
	upper := strings.ToUpper(reason)
	switch upper {
	case "STOP":
		return "end_turn"
	case "MAX_TOKENS":
		return "max_tokens"
	case "MALFORMED_FUNCTION_CALL", "UNEXPECTED_TOOL_CALL", "TOOL_CALL", "FUNCTION_CALL":
		return "tool_use"
	default:
		return strings.ToLower(reason)
	}
}

type geminiGenerateContentRequest struct {
	Contents          []geminiContent        `json:"contents"`
	SystemInstruction *geminiContent         `json:"systemInstruction,omitempty"`
	Tools             []geminiTool           `json:"tools,omitempty"`
	GenerationConfig  geminiGenerationConfig `json:"generationConfig,omitempty"`
}

type geminiGenerationConfig struct {
	Temperature     *float64 `json:"temperature,omitempty"`
	MaxOutputTokens int      `json:"maxOutputTokens,omitempty"`
	StopSequences   []string `json:"stopSequences,omitempty"`
}

type geminiContent struct {
	Role  string       `json:"role,omitempty"`
	Parts []geminiPart `json:"parts"`
}

type geminiPart struct {
	Text             string                  `json:"text,omitempty"`
	Thought          bool                    `json:"thought,omitempty"`
	ThoughtSignature string                  `json:"thoughtSignature,omitempty"`
	InlineData       *geminiInlineData       `json:"inlineData,omitempty"`
	FunctionCall     *geminiFunctionCall     `json:"functionCall,omitempty"`
	FunctionResponse *geminiFunctionResponse `json:"functionResponse,omitempty"`
}

type geminiInlineData struct {
	MimeType string `json:"mimeType"`
	Data     string `json:"data"`
}

type geminiFunctionCall struct {
	ID   string `json:"id,omitempty"`
	Name string `json:"name"`
	Args any    `json:"args,omitempty"`
}

type geminiFunctionResponse struct {
	ID       string         `json:"id,omitempty"`
	Name     string         `json:"name"`
	Response map[string]any `json:"response,omitempty"`
}

type geminiTool struct {
	FunctionDeclarations []geminiFunctionDeclaration `json:"functionDeclarations,omitempty"`
}

type geminiFunctionDeclaration struct {
	Name                 string `json:"name"`
	Description          string `json:"description,omitempty"`
	ParametersJsonSchema any    `json:"parametersJsonSchema,omitempty"`
}

type geminiGenerateContentResponse struct {
	Candidates     []geminiCandidate     `json:"candidates,omitempty"`
	PromptFeedback *geminiPromptFeedback `json:"promptFeedback,omitempty"`
	UsageMetadata  *geminiUsageMetadata  `json:"usageMetadata,omitempty"`
	Error          *geminiErrorBody      `json:"error,omitempty"`
}

type geminiCandidate struct {
	Content      geminiContent `json:"content"`
	FinishReason string        `json:"finishReason,omitempty"`
}

type geminiPromptFeedback struct {
	BlockReason string `json:"blockReason,omitempty"`
}

type geminiUsageMetadata struct {
	PromptTokenCount     int `json:"promptTokenCount,omitempty"`
	CandidatesTokenCount int `json:"candidatesTokenCount,omitempty"`
	TotalTokenCount      int `json:"totalTokenCount,omitempty"`
}

func (u *geminiUsageMetadata) merge(other *geminiUsageMetadata) {
	if other == nil {
		return
	}
	if other.PromptTokenCount > 0 {
		u.PromptTokenCount = other.PromptTokenCount
	}
	if other.CandidatesTokenCount > 0 {
		u.CandidatesTokenCount = other.CandidatesTokenCount
	}
	if other.TotalTokenCount > 0 {
		u.TotalTokenCount = other.TotalTokenCount
	}
}

func (u geminiUsageMetadata) toUsage() *Usage {
	return &Usage{
		InputTokens:  u.PromptTokenCount,
		OutputTokens: u.CandidatesTokenCount,
	}
}

type geminiErrorEnvelope struct {
	Error *geminiErrorBody `json:"error,omitempty"`
}

type geminiErrorBody struct {
	Status  string `json:"status,omitempty"`
	Message string `json:"message,omitempty"`
}

type geminiStreamState struct {
	usage      geminiUsageMetadata
	stopReason string
	sentStop   bool
}
