package api

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	githubCopilotDeviceFlowScope         = "read:user"
	githubCopilotDefaultBaseURL          = "https://api.individual.githubcopilot.com"
	githubCopilotDefaultEnterpriseDomain = "github.com"
	githubCopilotRefreshSkew             = 5 * time.Minute
	githubCopilotInitialPollMultiplier   = 1.2
	githubCopilotSlowDownPollMultiplier  = 1.4
	githubCopilotModelsCacheTTL          = 24 * time.Hour
	githubCopilotModelsRequestTimeout    = 5 * time.Second
	GitHubCopilotDefaultMainModel        = "gpt-5.4"
	GitHubCopilotDefaultSubagentModel    = "claude-haiku-4.5"
)

var githubCopilotClientID = mustDecodeBase64("SXYxLmI1MDdhMDhjODdlY2ZlOTg=")

type GitHubCopilotCredentials struct {
	GitHubToken      string
	AccessToken      string
	EnterpriseDomain string
	ExpiresAt        time.Time
}

type GitHubCopilotDeviceCode struct {
	DeviceCode      string
	UserCode        string
	VerificationURI string
	IntervalSeconds int
	ExpiresIn       int
}

type gitHubCopilotTokenResponse struct {
	Token     string `json:"token"`
	ExpiresAt int64  `json:"expires_at"`
}

type gitHubCopilotDeviceCodeResponse struct {
	DeviceCode      string `json:"device_code"`
	UserCode        string `json:"user_code"`
	VerificationURI string `json:"verification_uri"`
	Interval        int    `json:"interval"`
	ExpiresIn       int    `json:"expires_in"`
}

type gitHubCopilotDeviceTokenResponse struct {
	AccessToken string `json:"access_token"`
	Error       string `json:"error"`
	Description string `json:"error_description"`
	Interval    int    `json:"interval"`
}

type gitHubCopilotModelsResponse struct {
	Data []gitHubCopilotRemoteModel `json:"data"`
}

type gitHubCopilotRemoteModel struct {
	ModelPickerEnabled bool                            `json:"model_picker_enabled"`
	ID                 string                          `json:"id"`
	Name               string                          `json:"name"`
	Version            string                          `json:"version"`
	SupportedEndpoints []string                        `json:"supported_endpoints,omitempty"`
	Capabilities       gitHubCopilotRemoteCapabilities `json:"capabilities"`
}

type gitHubCopilotRemoteCapabilities struct {
	Family   string                      `json:"family"`
	Limits   gitHubCopilotRemoteLimits   `json:"limits"`
	Supports gitHubCopilotRemoteSupports `json:"supports"`
}

type gitHubCopilotRemoteLimits struct {
	MaxContextWindowTokens int                              `json:"max_context_window_tokens"`
	MaxOutputTokens        int                              `json:"max_output_tokens"`
	MaxPromptTokens        int                              `json:"max_prompt_tokens"`
	Vision                 *gitHubCopilotRemoteVisionLimits `json:"vision,omitempty"`
}

type gitHubCopilotRemoteVisionLimits struct {
	MaxPromptImageSize  int      `json:"max_prompt_image_size"`
	MaxPromptImages     int      `json:"max_prompt_images"`
	SupportedMediaTypes []string `json:"supported_media_types,omitempty"`
}

type gitHubCopilotRemoteSupports struct {
	AdaptiveThinking  *bool    `json:"adaptive_thinking,omitempty"`
	MaxThinkingBudget int      `json:"max_thinking_budget,omitempty"`
	MinThinkingBudget int      `json:"min_thinking_budget,omitempty"`
	ReasoningEffort   []string `json:"reasoning_effort,omitempty"`
	Streaming         bool     `json:"streaming"`
	StructuredOutputs *bool    `json:"structured_outputs,omitempty"`
	ToolCalls         bool     `json:"tool_calls"`
	Vision            *bool    `json:"vision,omitempty"`
}

type gitHubCopilotModelsCacheEntry struct {
	fetchedAt time.Time
	models    map[string]gitHubCopilotRemoteModel
}

var gitHubCopilotModelsCache = struct {
	mu      sync.Mutex
	entries map[string]gitHubCopilotModelsCacheEntry
}{
	entries: make(map[string]gitHubCopilotModelsCacheEntry),
}

func mustDecodeBase64(value string) string {
	decoded, err := base64.StdEncoding.DecodeString(value)
	if err != nil {
		panic(err)
	}
	return string(decoded)
}

func GitHubCopilotStaticHeaders() map[string]string {
	return map[string]string{
		"User-Agent":             "GitHubCopilotChat/0.35.0",
		"Editor-Version":         "vscode/1.107.0",
		"Editor-Plugin-Version":  "copilot-chat/0.35.0",
		"Copilot-Integration-Id": "vscode-chat",
	}
}

func BuildGitHubCopilotDynamicHeaders(messages []Message) map[string]string {
	headers := map[string]string{
		"X-Initiator":   gitHubCopilotInitiator(messages),
		"Openai-Intent": "conversation-edits",
	}

	if gitHubCopilotHasVisionInput(messages) {
		headers["Copilot-Vision-Request"] = "true"
	}

	return headers
}

func GitHubCopilotUsesAnthropicMessages(model string) bool {
	lower := strings.ToLower(strings.TrimSpace(model))
	return strings.Contains(lower, "claude") || strings.Contains(lower, "haiku") || strings.Contains(lower, "sonnet") || strings.Contains(lower, "opus")
}

func GitHubCopilotUsesOpenAIResponses(model string) bool {
	lower := strings.ToLower(strings.TrimSpace(model))
	return strings.HasPrefix(lower, "gpt-5") || strings.HasPrefix(lower, "o1") || strings.HasPrefix(lower, "o3") || strings.HasPrefix(lower, "o4")
}

func gitHubCopilotInitiator(messages []Message) string {
	if len(messages) == 0 {
		return "user"
	}
	last := messages[len(messages)-1]
	if last.Role == RoleUser {
		return "user"
	}
	return "agent"
}

func gitHubCopilotHasVisionInput(messages []Message) bool {
	for _, message := range messages {
		if len(message.Images) > 0 {
			return true
		}
	}
	return false
}

func NormalizeGitHubCopilotDomain(input string) (string, error) {
	trimmed := strings.TrimSpace(input)
	if trimmed == "" {
		return "", nil
	}

	if !strings.Contains(trimmed, "://") {
		trimmed = "https://" + trimmed
	}

	parsed, err := url.Parse(trimmed)
	if err != nil || parsed.Hostname() == "" {
		return "", fmt.Errorf("invalid GitHub Enterprise URL/domain")
	}

	return parsed.Hostname(), nil
}

func GetGitHubCopilotBaseURL(token, enterpriseDomain string) string {
	if host := gitHubCopilotProxyHost(token); host != "" {
		return "https://" + strings.Replace(host, "proxy.", "api.", 1)
	}
	if strings.TrimSpace(enterpriseDomain) != "" {
		return "https://copilot-api." + strings.TrimSpace(enterpriseDomain)
	}
	return githubCopilotDefaultBaseURL
}

func gitHubCopilotProxyHost(token string) string {
	for _, part := range strings.Split(token, ";") {
		part = strings.TrimSpace(part)
		if !strings.HasPrefix(part, "proxy-ep=") {
			continue
		}
		return strings.TrimSpace(strings.TrimPrefix(part, "proxy-ep="))
	}
	return ""
}

func StartGitHubCopilotDeviceFlow(ctx context.Context, enterpriseDomain string) (GitHubCopilotDeviceCode, error) {
	domain := enterpriseDomain
	if strings.TrimSpace(domain) == "" {
		domain = githubCopilotDefaultEnterpriseDomain
	}

	request, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		fmt.Sprintf("https://%s/login/device/code", domain),
		strings.NewReader(url.Values{
			"client_id": {githubCopilotClientID},
			"scope":     {githubCopilotDeviceFlowScope},
		}.Encode()),
	)
	if err != nil {
		return GitHubCopilotDeviceCode{}, err
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("User-Agent", GitHubCopilotStaticHeaders()["User-Agent"])

	response, err := newHTTPClient().Do(request)
	if err != nil {
		return GitHubCopilotDeviceCode{}, err
	}
	defer response.Body.Close()

	if response.StatusCode >= http.StatusMultipleChoices {
		return GitHubCopilotDeviceCode{}, classifyOpenAICompatStatus(response.StatusCode, mustReadHTTPBody(response))
	}

	var payload gitHubCopilotDeviceCodeResponse
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		return GitHubCopilotDeviceCode{}, fmt.Errorf("decode GitHub Copilot device code response: %w", err)
	}
	if payload.DeviceCode == "" || payload.UserCode == "" || payload.VerificationURI == "" {
		return GitHubCopilotDeviceCode{}, errors.New("invalid GitHub Copilot device code response")
	}
	if payload.Interval <= 0 {
		payload.Interval = 5
	}

	return GitHubCopilotDeviceCode{
		DeviceCode:      payload.DeviceCode,
		UserCode:        payload.UserCode,
		VerificationURI: payload.VerificationURI,
		IntervalSeconds: payload.Interval,
		ExpiresIn:       payload.ExpiresIn,
	}, nil
}

func PollGitHubCopilotGitHubToken(
	ctx context.Context,
	enterpriseDomain string,
	deviceCode string,
	intervalSeconds int,
	expiresIn int,
) (string, error) {
	domain := enterpriseDomain
	if strings.TrimSpace(domain) == "" {
		domain = githubCopilotDefaultEnterpriseDomain
	}
	if intervalSeconds <= 0 {
		intervalSeconds = 5
	}

	deadline := time.Now().Add(time.Duration(expiresIn) * time.Second)
	pollInterval := time.Duration(intervalSeconds) * time.Second
	pollMultiplier := githubCopilotInitialPollMultiplier
	slowDownResponses := 0

	for time.Now().Before(deadline) {
		wait := time.Duration(float64(pollInterval) * pollMultiplier)
		if remaining := time.Until(deadline); remaining > 0 && wait > remaining {
			wait = remaining
		}
		if err := sleepContext(ctx, wait); err != nil {
			return "", err
		}

		request, err := http.NewRequestWithContext(
			ctx,
			http.MethodPost,
			fmt.Sprintf("https://%s/login/oauth/access_token", domain),
			strings.NewReader(url.Values{
				"client_id":   {githubCopilotClientID},
				"device_code": {deviceCode},
				"grant_type":  {"urn:ietf:params:oauth:grant-type:device_code"},
			}.Encode()),
		)
		if err != nil {
			return "", err
		}
		request.Header.Set("Accept", "application/json")
		request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		request.Header.Set("User-Agent", GitHubCopilotStaticHeaders()["User-Agent"])

		response, err := newHTTPClient().Do(request)
		if err != nil {
			return "", err
		}

		var payload gitHubCopilotDeviceTokenResponse
		decodeErr := json.NewDecoder(response.Body).Decode(&payload)
		response.Body.Close()
		if decodeErr != nil {
			return "", fmt.Errorf("decode GitHub Copilot device token response: %w", decodeErr)
		}

		if payload.AccessToken != "" {
			return payload.AccessToken, nil
		}

		switch payload.Error {
		case "authorization_pending", "":
			continue
		case "slow_down":
			slowDownResponses++
			if payload.Interval > 0 {
				pollInterval = time.Duration(payload.Interval) * time.Second
			}
			pollMultiplier = githubCopilotSlowDownPollMultiplier
			continue
		default:
			suffix := ""
			if strings.TrimSpace(payload.Description) != "" {
				suffix = ": " + strings.TrimSpace(payload.Description)
			}
			return "", fmt.Errorf("GitHub Copilot device login failed: %s%s", payload.Error, suffix)
		}
	}

	if slowDownResponses > 0 {
		return "", errors.New("GitHub Copilot device login timed out after slow_down responses; this is often caused by clock drift in WSL or VM environments, so sync the system clock and try again")
	}

	return "", errors.New("GitHub Copilot device login timed out")
}

func RefreshGitHubCopilotToken(
	ctx context.Context,
	githubToken string,
	enterpriseDomain string,
) (GitHubCopilotCredentials, error) {
	if strings.TrimSpace(githubToken) == "" {
		return GitHubCopilotCredentials{}, errors.New("missing GitHub Copilot GitHub token")
	}

	domain := enterpriseDomain
	if strings.TrimSpace(domain) == "" {
		domain = githubCopilotDefaultEnterpriseDomain
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("https://api.%s/copilot_internal/v2/token", domain), nil)
	if err != nil {
		return GitHubCopilotCredentials{}, err
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Authorization", "Bearer "+githubToken)
	for key, value := range GitHubCopilotStaticHeaders() {
		request.Header.Set(key, value)
	}

	response, err := newHTTPClient().Do(request)
	if err != nil {
		return GitHubCopilotCredentials{}, err
	}
	defer response.Body.Close()

	if response.StatusCode >= http.StatusMultipleChoices {
		return GitHubCopilotCredentials{}, classifyOpenAICompatStatus(response.StatusCode, mustReadHTTPBody(response))
	}

	var payload gitHubCopilotTokenResponse
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		return GitHubCopilotCredentials{}, fmt.Errorf("decode GitHub Copilot token response: %w", err)
	}
	if payload.Token == "" || payload.ExpiresAt <= 0 {
		return GitHubCopilotCredentials{}, errors.New("invalid GitHub Copilot token response")
	}

	expiresAt := time.Unix(payload.ExpiresAt, 0).Add(-githubCopilotRefreshSkew)
	return GitHubCopilotCredentials{
		GitHubToken:      githubToken,
		AccessToken:      payload.Token,
		EnterpriseDomain: strings.TrimSpace(enterpriseDomain),
		ExpiresAt:        expiresAt,
	}, nil
}

func EnableGitHubCopilotModel(ctx context.Context, accessToken, model, enterpriseDomain string) error {
	if strings.TrimSpace(accessToken) == "" {
		return errors.New("missing GitHub Copilot access token")
	}
	model = strings.TrimSpace(model)
	if model == "" {
		return errors.New("missing GitHub Copilot model id")
	}

	request, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		GetGitHubCopilotBaseURL(accessToken, enterpriseDomain)+"/models/"+url.PathEscape(model)+"/policy",
		strings.NewReader(`{"state":"enabled"}`),
	)
	if err != nil {
		return err
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+accessToken)
	request.Header.Set("Openai-Intent", "chat-policy")
	request.Header.Set("X-Interaction-Type", "chat-policy")
	for key, value := range GitHubCopilotStaticHeaders() {
		request.Header.Set(key, value)
	}

	response, err := newHTTPClient().Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode >= http.StatusMultipleChoices {
		return classifyOpenAICompatStatus(response.StatusCode, mustReadHTTPBody(response))
	}
	return nil
}

func EnableGitHubCopilotModels(ctx context.Context, accessToken, enterpriseDomain string, models []string) map[string]error {
	unique := make(map[string]struct{}, len(models))
	failures := make(map[string]error)
	var (
		mu sync.Mutex
		wg sync.WaitGroup
	)

	for _, model := range models {
		model = strings.TrimSpace(model)
		if model == "" {
			continue
		}
		if _, exists := unique[model]; exists {
			continue
		}
		unique[model] = struct{}{}

		wg.Add(1)
		go func(model string) {
			defer wg.Done()
			if err := EnableGitHubCopilotModel(ctx, accessToken, model, enterpriseDomain); err != nil {
				mu.Lock()
				failures[model] = err
				mu.Unlock()
			}
		}(model)
	}

	wg.Wait()
	return failures
}

func ListGitHubCopilotModelIDs(ctx context.Context, accessToken, enterpriseDomain string) ([]string, error) {
	models, err := FetchGitHubCopilotModels(ctx, accessToken, enterpriseDomain)
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(models))
	for id := range models {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids, nil
}

func FetchGitHubCopilotModels(ctx context.Context, accessToken, enterpriseDomain string) (map[string]gitHubCopilotRemoteModel, error) {
	if strings.TrimSpace(accessToken) == "" {
		return nil, errors.New("missing GitHub Copilot access token")
	}

	baseURL := GetGitHubCopilotBaseURL(accessToken, enterpriseDomain)
	if cached, ok := cachedGitHubCopilotModels(baseURL, false); ok {
		return cached, nil
	}
	stale, hasStale := cachedGitHubCopilotModels(baseURL, true)

	requestCtx, cancel := context.WithTimeout(ctx, githubCopilotModelsRequestTimeout)
	defer cancel()

	request, err := http.NewRequestWithContext(requestCtx, http.MethodGet, baseURL+"/models", nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Authorization", "Bearer "+accessToken)
	for key, value := range GitHubCopilotStaticHeaders() {
		request.Header.Set(key, value)
	}

	response, err := newHTTPClient().Do(request)
	if err != nil {
		if hasStale {
			return stale, nil
		}
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode >= http.StatusMultipleChoices {
		if hasStale {
			return stale, nil
		}
		return nil, classifyOpenAICompatStatus(response.StatusCode, mustReadHTTPBody(response))
	}

	var payload gitHubCopilotModelsResponse
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		if hasStale {
			return stale, nil
		}
		return nil, fmt.Errorf("decode GitHub Copilot models response: %w", err)
	}

	models := make(map[string]gitHubCopilotRemoteModel, len(payload.Data))
	for _, model := range payload.Data {
		model.ID = strings.TrimSpace(model.ID)
		if model.ID == "" || !model.ModelPickerEnabled {
			continue
		}
		models[model.ID] = model
	}

	storeGitHubCopilotModels(baseURL, models)
	return cloneGitHubCopilotModels(models), nil
}

func ResolveGitHubCopilotModelCapabilities(ctx context.Context, accessToken, enterpriseDomain, model string) (ModelCapabilities, bool, error) {
	capabilities := Presets["github-copilot"].Capabilities
	model = strings.TrimSpace(model)
	if model == "" {
		return capabilities, false, nil
	}

	models, err := FetchGitHubCopilotModels(ctx, accessToken, enterpriseDomain)
	if err != nil {
		return capabilities, false, err
	}
	remote, ok := models[model]
	if !ok {
		return capabilities, false, nil
	}

	return mergeGitHubCopilotCapabilities(capabilities, remote), true, nil
}

func mergeGitHubCopilotCapabilities(base ModelCapabilities, remote gitHubCopilotRemoteModel) ModelCapabilities {
	capabilities := base
	capabilities.SupportsToolUse = remote.Capabilities.Supports.ToolCalls
	if remote.Capabilities.Limits.MaxContextWindowTokens > 0 {
		capabilities.MaxContextWindow = remote.Capabilities.Limits.MaxContextWindowTokens
	}
	if remote.Capabilities.Limits.MaxOutputTokens > 0 {
		capabilities.MaxOutputTokens = remote.Capabilities.Limits.MaxOutputTokens
	}
	if remote.Capabilities.Supports.StructuredOutputs != nil {
		capabilities.SupportsJsonMode = *remote.Capabilities.Supports.StructuredOutputs
	}

	supportsVision := false
	if remote.Capabilities.Supports.Vision != nil {
		supportsVision = *remote.Capabilities.Supports.Vision
	}
	if !supportsVision && remote.Capabilities.Limits.Vision != nil {
		supportsVision = len(remote.Capabilities.Limits.Vision.SupportedMediaTypes) > 0
	}
	capabilities.SupportsVision = supportsVision

	supportsReasoning := len(remote.Capabilities.Supports.ReasoningEffort) > 0 ||
		(remote.Capabilities.Supports.AdaptiveThinking != nil && *remote.Capabilities.Supports.AdaptiveThinking) ||
		remote.Capabilities.Supports.MaxThinkingBudget > 0 ||
		remote.Capabilities.Supports.MinThinkingBudget > 0
	capabilities.SupportsExtendedThinking = supportsReasoning

	return capabilities
}

func cachedGitHubCopilotModels(baseURL string, allowStale bool) (map[string]gitHubCopilotRemoteModel, bool) {
	gitHubCopilotModelsCache.mu.Lock()
	defer gitHubCopilotModelsCache.mu.Unlock()

	entry, ok := gitHubCopilotModelsCache.entries[baseURL]
	if !ok {
		return nil, false
	}
	if !allowStale && time.Since(entry.fetchedAt) > githubCopilotModelsCacheTTL {
		return nil, false
	}
	return cloneGitHubCopilotModels(entry.models), true
}

func storeGitHubCopilotModels(baseURL string, models map[string]gitHubCopilotRemoteModel) {
	gitHubCopilotModelsCache.mu.Lock()
	defer gitHubCopilotModelsCache.mu.Unlock()

	gitHubCopilotModelsCache.entries[baseURL] = gitHubCopilotModelsCacheEntry{
		fetchedAt: time.Now(),
		models:    cloneGitHubCopilotModels(models),
	}
}

func cloneGitHubCopilotModels(models map[string]gitHubCopilotRemoteModel) map[string]gitHubCopilotRemoteModel {
	cloned := make(map[string]gitHubCopilotRemoteModel, len(models))
	for id, model := range models {
		copyModel := model
		copyModel.SupportedEndpoints = append([]string(nil), model.SupportedEndpoints...)
		copyModel.Capabilities.Supports.ReasoningEffort = append([]string(nil), model.Capabilities.Supports.ReasoningEffort...)
		if model.Capabilities.Limits.Vision != nil {
			vision := *model.Capabilities.Limits.Vision
			vision.SupportedMediaTypes = append([]string(nil), model.Capabilities.Limits.Vision.SupportedMediaTypes...)
			copyModel.Capabilities.Limits.Vision = &vision
		}
		cloned[id] = copyModel
	}
	return cloned
}

func sleepContext(ctx context.Context, delay time.Duration) error {
	if delay <= 0 {
		return nil
	}

	timer := time.NewTimer(delay)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func mustReadHTTPBody(response *http.Response) []byte {
	if response == nil || response.Body == nil {
		return nil
	}
	body, _ := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	return body
}
