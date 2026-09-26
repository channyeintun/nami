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
		httpClient:   newStreamingHTTPClient(),
		capabilities: ResolveModelCapabilities("gemini", model),
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

const geminiMaxEmptyRetries = 2
const geminiMaxRetryAfter = 60 * time.Second

var geminiRetryDelayBodyRe = regexp.MustCompile(`(?i)(?:please retry in\s+(\d+(?:\.\d+)?)\s*s|"retryDelay"\s*:\s*"(\d+(?:\.\d+)?)s")`)

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

func geminiRetryAfterDelay(resp *http.Response, body []byte) time.Duration {
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
	if v := resp.Header.Get("X-RateLimit-Reset"); v != "" {
		if ts, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64); err == nil && ts > 0 {
			if d := time.Until(time.Unix(ts, 0)); d > 0 {
				return d
			}
		}
	}
	if v := resp.Header.Get("X-RateLimit-Reset-After"); v != "" {
		if secs, err := strconv.ParseFloat(strings.TrimSpace(v), 64); err == nil && secs > 0 {
			return time.Duration(secs * float64(time.Second))
		}
	}
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
			if d := geminiRetryAfterDelay(currentResp, bodyBytes); d > 0 {
				if ae, ok := errors.AsType[*APIError](apiErr); ok {
					if d > geminiMaxRetryAfter {
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

func (c *GeminiClient) handleEvent(data string, state *geminiStreamState, yield func(ModelEvent, error) bool) error {
	trimmed := strings.TrimSpace(data)
	if trimmed == "" {
		return nil
	}

	var response geminiGenerateContentResponse
	if err := json.Unmarshal([]byte(trimmed), &response); err != nil {
		return fmt.Errorf("decode Gemini stream chunk: %w", err)
	}
	if response.Error != nil {
		return &APIError{Type: classifyGeminiErrorType(0, response.Error.Status, response.Error.Message), Message: response.Error.Message}
	}
	// Every chunk repeats the running usage, so it is only merged here and
	// reported once, with the stop event.
	state.usage.merge(response.UsageMetadata)

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
		if state.usage != (geminiUsageMetadata{}) {
			if !yield(ModelEvent{Type: ModelEventUsage, Usage: state.usage.toUsage()}, nil) {
				return errStopStream
			}
		}
		if !yield(ModelEvent{Type: ModelEventStop, StopReason: state.stopReason}, nil) {
			return errStopStream
		}
		return errStopStream
	}

	return nil
}

const geminiSyntheticThoughtSignature = "skip_thought_signature_validator"
