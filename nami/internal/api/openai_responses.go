package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"iter"
	"maps"
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
	httpClient   *http.Client
	capabilities ModelCapabilities

	// mu guards the settings below, which the engine can replace while a
	// request, such as the startup warmup, is reading them.
	mu                 sync.RWMutex
	enterpriseDomain   string
	codexAccountID     string
	codexAccountIDFunc func() string
	apiKeyFunc         func() (string, error)
}

// SetAPIKeyFunc sets a callback that returns a fresh API key on each call.
// When set, the client calls this instead of using the static apiKey.
func (c *OpenAIResponsesClient) SetAPIKeyFunc(fn func() (string, error)) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.apiKeyFunc = fn
}

func (c *OpenAIResponsesClient) SetGitHubCopilotEnterpriseDomain(domain string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.enterpriseDomain = strings.TrimSpace(domain)
}

func (c *OpenAIResponsesClient) SetCodexAccountID(accountID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.codexAccountID = strings.TrimSpace(accountID)
}

func (c *OpenAIResponsesClient) SetCodexAccountIDFunc(fn func() string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.codexAccountIDFunc = fn
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
	if apiKey == "" && provider != "github-copilot" {
		return nil, &APIError{Type: ErrAuth, Message: fmt.Sprintf("missing API key for provider %q", provider)}
	}

	return &OpenAIResponsesClient{
		provider:     provider,
		model:        model,
		baseURL:      strings.TrimRight(baseURL, "/"),
		apiKey:       apiKey,
		httpClient:   newStreamingHTTPClient(),
		capabilities: ResolveModelCapabilities(provider, model),
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
	baseURL := c.resolveBaseURL(apiKey)
	headers := map[string]string{
		"accept":        "application/json",
		"authorization": "Bearer " + apiKey,
	}
	if c.provider == "github-copilot" {
		for key, value := range GitHubCopilotStaticHeaders() {
			headers[strings.ToLower(key)] = value
		}
	} else if c.provider == "codex" {
		maps.Copy(headers, CodexStaticHeaders(c.resolveCodexAccountID()))
	}
	return issueWarmupRequest(ctx, c.httpClient, http.MethodHead, baseURL+"/models", headers)
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
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/responses", bytes.NewReader(body))
		if err != nil {
			return fmt.Errorf("create OpenAI Responses request: %w", err)
		}
		req.Header.Set("content-type", "application/json")
		req.Header.Set("accept", "text/event-stream")
		req.Header.Set("authorization", "Bearer "+apiKey)
		for key, value := range extraHeaders {
			req.Header.Set(key, value)
		}
		if c.provider == "codex" {
			for key, value := range CodexStaticHeaders(c.resolveCodexAccountID()) {
				req.Header.Set(key, value)
			}
		}

		currentResp, err := c.httpClient.Do(req)
		if err != nil {
			return networkError("OpenAI Responses request failed", err)
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
