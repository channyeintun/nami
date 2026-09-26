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

	// mu guards the settings below, which the engine can replace while a
	// request, such as the startup warmup, is reading them.
	mu               sync.RWMutex
	enterpriseDomain string
	apiKeyFunc       func() (string, error)
}

// SetAPIKeyFunc sets a callback that returns a fresh API key on each call.
// When set, the client calls this instead of using the static apiKey.
func (c *OpenAICompatClient) SetAPIKeyFunc(fn func() (string, error)) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.apiKeyFunc = fn
}

func (c *OpenAICompatClient) SetGitHubCopilotEnterpriseDomain(domain string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.enterpriseDomain = strings.TrimSpace(domain)
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
	if apiKey == "" && provider != "github-copilot" {
		return nil, &APIError{Type: ErrAuth, Message: fmt.Sprintf("missing API key for provider %q", provider)}
	}

	return &OpenAICompatClient{
		provider:     provider,
		model:        model,
		baseURL:      strings.TrimRight(baseURL, "/"),
		apiKey:       apiKey,
		httpClient:   newStreamingHTTPClient(),
		capabilities: ResolveModelCapabilities(provider, model),
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
	apiKey, err := c.resolveAPIKey()
	if err != nil {
		return err
	}
	baseURL := c.resolveBaseURL(apiKey)
	headers := map[string]string{
		"accept":        "application/json",
		"authorization": "Bearer " + apiKey,
	}
	if c.provider == "github-copilot" {
		for key, value := range GitHubCopilotStaticHeaders() {
			headers[strings.ToLower(key)] = value
		}
	}
	return issueWarmupRequest(ctx, c.httpClient, http.MethodHead, baseURL+"/models", headers)
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

		state := openAICompatStreamState{}

		sseBody := sseBodyWithDebug(resp.Body, c.provider)
		err := readSSE(ctx, sseBody, func(_ string, data string) error {
			return c.handleEvent(data, &state, yield)
		})
		if err == nil && state.stopReason != "" {
			// Some servers close the stream after the finish chunk without
			// sending [DONE]. The response is complete, so finish it here.
			err = state.emitStop(yield)
		}
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
		apiKey, err := c.resolveAPIKey()
		if err != nil {
			return err
		}
		baseURL := c.resolveBaseURL(apiKey)
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/chat/completions", bytes.NewReader(body))
		if err != nil {
			return fmt.Errorf("create OpenAI-compatible request: %w", err)
		}
		req.Header.Set("content-type", "application/json")
		req.Header.Set("accept", "text/event-stream")
		req.Header.Set("authorization", "Bearer "+apiKey)
		for key, value := range extraHeaders {
			req.Header.Set(key, value)
		}

		currentResp, err := c.httpClient.Do(req)
		if err != nil {
			return networkError("OpenAI-compatible request failed", err)
		}
		if currentResp.StatusCode >= http.StatusMultipleChoices {
			defer currentResp.Body.Close()
			bodyBytes, _ := io.ReadAll(io.LimitReader(currentResp.Body, 1<<20))
			return withRetryAfter(classifyOpenAICompatStatus(currentResp.StatusCode, bodyBytes), retryAfterHeaderDelay(currentResp.Header))
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
