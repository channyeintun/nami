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
	"strings"
	"sync"
)

// OllamaClient implements the native Ollama chat streaming API.
type OllamaClient struct {
	model        string
	baseURL      string
	apiKey       string
	httpClient   *http.Client
	capabilities ModelCapabilities
}

// NewOllamaClient constructs an Ollama chat client.
func NewOllamaClient(model, apiKey, baseURL string) (*OllamaClient, error) {
	preset := Presets["ollama"]
	if model == "" {
		model = preset.DefaultModel
	}
	if baseURL == "" {
		baseURL = preset.BaseURL
	}
	warnCustomBaseURL("ollama", preset.BaseURL, baseURL)
	if apiKey == "" {
		apiKey = os.Getenv("OLLAMA_API_KEY")
	}
	return &OllamaClient{
		model:        model,
		baseURL:      strings.TrimRight(baseURL, "/"),
		apiKey:       apiKey,
		httpClient:   newHTTPClient(),
		capabilities: preset.Capabilities,
	}, nil
}

// ModelID returns the active model identifier.
func (c *OllamaClient) ModelID() string {
	return c.model
}

// Capabilities reports model capabilities.
func (c *OllamaClient) Capabilities() ModelCapabilities {
	return c.capabilities
}

// Warmup preconnects the Ollama transport and checks that the local API accepts requests.
func (c *OllamaClient) Warmup(ctx context.Context) error {
	headers := map[string]string{"accept": "application/json"}
	if c.apiKey != "" {
		headers["authorization"] = "Bearer " + c.apiKey
	}
	return issueWarmupRequest(ctx, c.httpClient, http.MethodGet, c.baseURL+"/api/tags", headers)
}

// Stream opens a streaming Ollama chat request and yields model events.
func (c *OllamaClient) Stream(ctx context.Context, req ModelRequest) (iter.Seq2[ModelEvent, error], error) {
	payload, err := c.buildRequest(req)
	if err != nil {
		return nil, err
	}

	resp, err := c.openStream(ctx, payload)
	if err != nil {
		return nil, err
	}

	return func(yield func(ModelEvent, error) bool) {
		defer resp.Body.Close()
		stopCancelCloser := closeReadCloserOnCancel(ctx, resp.Body)
		defer stopCancelCloser()

		scanner := bufio.NewScanner(resp.Body)
		scanner.Buffer(make([]byte, 0, 64*1024), 2*1024*1024)
		state := ollamaStreamState{}

		for scanner.Scan() {
			select {
			case <-ctx.Done():
				yield(ModelEvent{}, ctx.Err())
				return
			default:
			}

			line := strings.TrimSpace(scanner.Text())
			if line == "" {
				continue
			}
			if err := c.handleChunk(line, &state, yield); err != nil {
				if errors.Is(err, errStopStream) {
					return
				}
				yield(ModelEvent{}, err)
				return
			}
		}
		if err := ctx.Err(); err != nil {
			yield(ModelEvent{}, err)
			return
		}
		if err := scanner.Err(); err != nil {
			yield(ModelEvent{}, &APIError{Type: ErrNetwork, Message: "read Ollama stream", Err: err})
		}
	}, nil
}

func (c *OllamaClient) openStream(ctx context.Context, payload ollamaChatRequest) (*http.Response, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal Ollama request: %w", err)
	}

	var (
		resp *http.Response
		mu   sync.Mutex
	)

	err = RetryWithBackoff(ctx, DefaultRetryPolicy(), func() error {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/api/chat", bytes.NewReader(body))
		if err != nil {
			return fmt.Errorf("create Ollama request: %w", err)
		}
		req.Header.Set("content-type", "application/json")
		if c.apiKey != "" {
			req.Header.Set("authorization", "Bearer "+c.apiKey)
		}

		currentResp, err := c.httpClient.Do(req)
		if err != nil {
			return &APIError{Type: ErrNetwork, Message: "Ollama request failed", Err: err}
		}
		if currentResp.StatusCode >= http.StatusMultipleChoices {
			defer currentResp.Body.Close()
			bodyBytes, _ := io.ReadAll(io.LimitReader(currentResp.Body, 1<<20))
			return classifyOllamaStatus(currentResp.StatusCode, bodyBytes)
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

func (c *OllamaClient) buildRequest(req ModelRequest) (ollamaChatRequest, error) {
	messages, err := buildOllamaMessages(req.SystemPrompt, req.Messages)
	if err != nil {
		return ollamaChatRequest{}, err
	}

	maxTokens := req.MaxTokens
	if maxTokens <= 0 {
		maxTokens = c.capabilities.MaxOutputTokens
	}

	payload := ollamaChatRequest{
		Model:    c.model,
		Messages: messages,
		Stream:   true,
		Tools:    buildOllamaTools(req.Tools),
		Options: ollamaOptions{
			NumCtx:      c.capabilities.MaxContextWindow,
			NumPredict:  maxTokens,
			Temperature: req.Temperature,
			Stop:        req.StopSequences,
		},
	}

	return payload, nil
}

func (c *OllamaClient) handleChunk(
	line string,
	state *ollamaStreamState,
	yield func(ModelEvent, error) bool,
) error {
	var chunk ollamaChatResponse
	if err := json.Unmarshal([]byte(line), &chunk); err != nil {
		return fmt.Errorf("decode Ollama stream chunk: %w", err)
	}
	if chunk.Error != "" {
		return &APIError{Type: classifyOllamaErrorType(0, chunk.Error), Message: chunk.Error}
	}
	if chunk.PromptEvalCount > 0 || chunk.EvalCount > 0 {
		state.usage = &Usage{InputTokens: chunk.PromptEvalCount, OutputTokens: chunk.EvalCount}
		if !yield(ModelEvent{Type: ModelEventUsage, Usage: state.usage}, nil) {
			return errStopStream
		}
	}
	if chunk.Message.Thinking != "" {
		if !yield(ModelEvent{Type: ModelEventThinking, Text: chunk.Message.Thinking}, nil) {
			return errStopStream
		}
	}
	if chunk.Message.Content != "" {
		if !yield(ModelEvent{Type: ModelEventToken, Text: chunk.Message.Content}, nil) {
			return errStopStream
		}
	}
	for _, toolCall := range chunk.Message.ToolCalls {
		input, err := json.Marshal(toolCall.Function.Arguments)
		if err != nil {
			return fmt.Errorf("encode Ollama tool call args: %w", err)
		}
		state.hasToolCall = true
		if !yield(ModelEvent{
			Type: ModelEventToolCall,
			ToolCall: &ToolCall{
				ID:    firstNonEmpty(toolCall.Function.Name, "tool_call"),
				Name:  toolCall.Function.Name,
				Input: string(input),
			},
		}, nil) {
			return errStopStream
		}
	}
	if chunk.Done && !state.sentStop {
		state.sentStop = true
		stopReason := mapOllamaStopReason(chunk.DoneReason)
		if state.hasToolCall && stopReason != "tool_use" {
			stopReason = "tool_use"
		}
		if !yield(ModelEvent{Type: ModelEventStop, StopReason: stopReason}, nil) {
			return errStopStream
		}
		return errStopStream
	}
	return nil
}

func buildOllamaMessages(systemPrompt string, messages []Message) ([]ollamaMessage, error) {
	built := make([]ollamaMessage, 0, len(messages)+1)
	if trimmed := strings.TrimSpace(systemPrompt); trimmed != "" {
		built = append(built, ollamaMessage{Role: "system", Content: trimmed})
	}

	for _, msg := range messages {
		converted, err := convertOllamaMessage(msg)
		if err != nil {
			return nil, err
		}
		built = append(built, converted...)
	}
	return built, nil
}

func convertOllamaMessage(msg Message) ([]ollamaMessage, error) {
	trimmed := strings.TrimSpace(msg.Content)
	converted := make([]ollamaMessage, 0, 2)

	switch msg.Role {
	case RoleSystem:
		if trimmed != "" {
			converted = append(converted, ollamaMessage{Role: "system", Content: trimmed})
		}
	case RoleUser:
		if trimmed != "" {
			converted = append(converted, ollamaMessage{Role: "user", Content: trimmed})
		}
		if msg.ToolResult != nil {
			converted = append(converted, ollamaToolResultMessage(*msg.ToolResult))
		}
	case RoleTool:
		if msg.ToolResult != nil {
			converted = append(converted, ollamaToolResultMessage(*msg.ToolResult))
		}
	case RoleAssistant:
		assistant := ollamaMessage{Role: "assistant"}
		if trimmed != "" {
			assistant.Content = trimmed
		}
		if len(msg.ToolCalls) > 0 {
			assistant.ToolCalls = make([]ollamaToolCall, 0, len(msg.ToolCalls))
			for _, toolCall := range msg.ToolCalls {
				input, err := decodeToolInput(toolCall.Input)
				if err != nil {
					return nil, err
				}
				assistant.ToolCalls = append(assistant.ToolCalls, ollamaToolCall{
					Function: ollamaFunctionCall{Name: toolCall.Name, Arguments: input},
				})
			}
		}
		if assistant.Content != "" || len(assistant.ToolCalls) > 0 {
			converted = append(converted, assistant)
		}
	}

	return converted, nil
}

func ollamaToolResultMessage(result ToolResult) ollamaMessage {
	content := result.Output
	if result.IsError {
		content = "ERROR: " + content
	}
	return ollamaMessage{Role: "tool", Content: content}
}

func buildOllamaTools(tools []ToolDefinition) []ollamaToolDefinition {
	if len(tools) == 0 {
		return nil
	}

	built := make([]ollamaToolDefinition, 0, len(tools))
	for _, tool := range tools {
		built = append(built, ollamaToolDefinition{
			Type: "function",
			Function: ollamaFunctionDefinition{
				Name:        tool.Name,
				Description: tool.Description,
				Parameters:  tool.InputSchema,
			},
		})
	}
	return built
}

func classifyOllamaStatus(statusCode int, body []byte) error {
	message := strings.TrimSpace(string(body))
	if message == "" {
		message = http.StatusText(statusCode)
	}
	return &APIError{
		Type:       classifyOllamaErrorType(statusCode, message),
		StatusCode: statusCode,
		Message:    message,
	}
}

func classifyOllamaErrorType(statusCode int, message string) APIErrorType {
	lowerMessage := strings.ToLower(message)
	switch {
	case statusCode == http.StatusUnauthorized || statusCode == http.StatusForbidden:
		return ErrAuth
	case statusCode == http.StatusTooManyRequests:
		return ErrRateLimit
	case statusCode >= http.StatusInternalServerError:
		return ErrOverloaded
	case strings.Contains(lowerMessage, "context") && strings.Contains(lowerMessage, "limit"):
		return ErrPromptTooLong
	case strings.Contains(lowerMessage, "ggml_assert"):
		return ErrOverloaded
	default:
		return ErrUnknown
	}
}

func mapOllamaStopReason(reason string) string {
	switch strings.ToLower(reason) {
	case "stop", "":
		return "end_turn"
	case "length":
		return "max_tokens"
	case "tool_calls", "function_call":
		return "tool_use"
	default:
		return strings.ToLower(reason)
	}
}

type ollamaChatRequest struct {
	Model    string                 `json:"model"`
	Messages []ollamaMessage        `json:"messages"`
	Tools    []ollamaToolDefinition `json:"tools,omitempty"`
	Options  ollamaOptions          `json:"options,omitempty"`
	Stream   bool                   `json:"stream"`
}

type ollamaOptions struct {
	NumCtx      int      `json:"num_ctx,omitempty"`
	NumPredict  int      `json:"num_predict,omitempty"`
	Temperature *float64 `json:"temperature,omitempty"`
	Stop        []string `json:"stop,omitempty"`
}

type ollamaMessage struct {
	Role      string           `json:"role"`
	Content   string           `json:"content,omitempty"`
	Thinking  string           `json:"thinking,omitempty"`
	ToolCalls []ollamaToolCall `json:"tool_calls,omitempty"`
}

type ollamaToolDefinition struct {
	Type     string                   `json:"type"`
	Function ollamaFunctionDefinition `json:"function"`
}

type ollamaFunctionDefinition struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Parameters  any    `json:"parameters,omitempty"`
}

type ollamaToolCall struct {
	Function ollamaFunctionCall `json:"function"`
}

type ollamaFunctionCall struct {
	Name      string `json:"name,omitempty"`
	Arguments any    `json:"arguments,omitempty"`
}

type ollamaChatResponse struct {
	Message         ollamaMessage `json:"message"`
	Done            bool          `json:"done"`
	DoneReason      string        `json:"done_reason,omitempty"`
	PromptEvalCount int           `json:"prompt_eval_count,omitempty"`
	EvalCount       int           `json:"eval_count,omitempty"`
	Error           string        `json:"error,omitempty"`
}

type ollamaStreamState struct {
	usage       *Usage
	sentStop    bool
	hasToolCall bool
}
