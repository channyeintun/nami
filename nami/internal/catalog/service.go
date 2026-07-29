package catalog

import (
	"cmp"
	"context"
	"fmt"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/channyeintun/nami/internal/api"
	"github.com/channyeintun/nami/internal/config"
	"github.com/channyeintun/nami/internal/modelsdev"
	"github.com/channyeintun/nami/internal/modelselection"
)

type SnapshotLoader interface {
	Load(ctx context.Context) (modelsdev.Snapshot, error)
}

type Service struct {
	Loader SnapshotLoader
}

type Snapshot struct {
	Providers []Provider
	Defaults  map[string]string
	Active    ModelRef
}

type Provider struct {
	ID           string
	Name         string
	BaseURL      string
	EnvKeys      []string
	Protocol     api.ClientType
	Source       ProviderSource
	Auth         AuthStatus
	DefaultModel string
	Models       []Model
}

type AuthStatus struct {
	Source     string
	Configured bool
	Usable     bool
	SetupHint  string
	LastError  string
}

type ProviderSource string

const (
	ProviderSourceModelsDev ProviderSource = "models.dev"
	ProviderSourceConfig    ProviderSource = "config"
)

type Model struct {
	ID           string
	Name         string
	Family       string
	Status       string
	Capabilities api.ModelCapabilities
	Limits       ModelLimits
	Cost         ModelCost
	API          ModelAPI
}

type ModelLimits struct {
	ContextWindow int
	PromptTokens  int
	OutputTokens  int
}

type ModelCost struct {
	Input      float64
	Output     float64
	CacheRead  float64
	CacheWrite float64
}

type ModelAPI struct {
	Attachment       bool
	Reasoning        bool
	ToolCall         bool
	Temperature      bool
	StructuredOutput bool
	OpenWeights      bool
	InputModalities  []string
	OutputModalities []string
}

type ModelRef struct {
	ProviderID string
	ModelID    string
}

type runtimeProvider struct {
	CatalogID string
	SourceID  string
	Spec      api.ProviderSpec
	Source    ProviderSource
}

var runtimeProviders = []runtimeProvider{
	{CatalogID: "github-copilot", SourceID: "github-copilot", Source: ProviderSourceModelsDev, Spec: mustProviderSpec("github-copilot")},
	{CatalogID: "openai", SourceID: "openai", Source: ProviderSourceModelsDev, Spec: mustProviderSpec("openai")},
	{CatalogID: "anthropic", SourceID: "anthropic", Source: ProviderSourceModelsDev, Spec: mustProviderSpec("anthropic")},
	{CatalogID: "gemini", SourceID: "google", Source: ProviderSourceModelsDev, Spec: mustProviderSpec("gemini")},
	{CatalogID: "deepseek", SourceID: "deepseek", Source: ProviderSourceModelsDev, Spec: mustProviderSpec("deepseek")},
	{CatalogID: "mistral", SourceID: "mistral", Source: ProviderSourceModelsDev, Spec: mustProviderSpec("mistral")},
	{CatalogID: "groq", SourceID: "groq", Source: ProviderSourceModelsDev, Spec: mustProviderSpec("groq")},
	{CatalogID: "qwen", SourceID: "alibaba", Source: ProviderSourceModelsDev, Spec: mustProviderSpec("qwen")},
	{CatalogID: "glm", SourceID: "zhipuai", Source: ProviderSourceModelsDev, Spec: mustProviderSpec("glm")},
	{CatalogID: "ollama", SourceID: "", Source: ProviderSourceConfig, Spec: mustProviderSpec("ollama")},
}

func NewService(loader SnapshotLoader) *Service {
	return &Service{Loader: loader}
}

func (s *Service) Snapshot(ctx context.Context, cfg config.Config) (Snapshot, error) {
	if s == nil || s.Loader == nil {
		return Snapshot{}, nil
	}

	base, err := s.Loader.Load(ctx)
	if err != nil {
		return Snapshot{}, err
	}

	providers := make(map[string]Provider, len(runtimeProviders))
	for _, runtime := range runtimeProviders {
		provider := buildProvider(base, cfg, runtime)
		if provider.ID == "" {
			continue
		}
		providers[provider.ID] = provider
	}
	for providerID, override := range cfg.Providers {
		providerID = normalizeProviderID(providerID)
		if providerID == "" {
			continue
		}
		if _, exists := providers[providerID]; exists {
			continue
		}
		provider := buildConfigProvider(cfg, providerID, override)
		if provider.ID == "" {
			continue
		}
		providers[provider.ID] = provider
	}

	ordered := make([]Provider, 0, len(providers))
	defaults := make(map[string]string, len(providers))
	for _, provider := range providers {
		ordered = append(ordered, provider)
		defaults[provider.ID] = provider.DefaultModel
	}

	slices.SortFunc(ordered, func(a, b Provider) int {
		return cmp.Or(providerPriority(a.ID)-providerPriority(b.ID), strings.Compare(a.ID, b.ID))
	})

	return Snapshot{
		Providers: ordered,
		Defaults:  defaults,
		Active:    resolveActive(cfg, defaults),
	}, nil
}

func (s *Service) Provider(ctx context.Context, cfg config.Config, providerID string) (Provider, bool, error) {
	snapshot, err := s.Snapshot(ctx, cfg)
	if err != nil {
		return Provider{}, false, err
	}
	providerID = normalizeProviderID(providerID)
	for _, provider := range snapshot.Providers {
		if provider.ID == providerID {
			return provider, true, nil
		}
	}
	return Provider{}, false, nil
}

func (s *Service) Route(ctx context.Context, cfg config.Config, providerID string, modelID string) (api.ProviderRoute, error) {
	if normalizeProviderID(providerID) == "codex" {
		return s.codexRoute(ctx, cfg, modelID)
	}

	provider, ok, err := s.Provider(ctx, cfg, providerID)
	if err != nil {
		return api.ProviderRoute{}, err
	}
	if !ok {
		return api.ProviderRoute{}, fmt.Errorf("provider %q not found in catalog", providerID)
	}

	modelID = strings.TrimSpace(modelID)
	if modelID == "" {
		modelID = provider.DefaultModel
	}

	model, ok := modelForProvider(provider, modelID)
	if !ok {
		model = fallbackModel(provider.ID, modelID)
	}

	return api.ProviderRoute{
		ProviderID:   provider.ID,
		ModelID:      model.ID,
		Protocol:     provider.Protocol,
		BaseURL:      provider.BaseURL,
		Capabilities: model.Capabilities,
	}, nil
}

func (s *Service) codexRoute(ctx context.Context, cfg config.Config, modelID string) (api.ProviderRoute, error) {
	snapshot, err := s.Snapshot(ctx, cfg)
	if err != nil {
		return api.ProviderRoute{}, err
	}

	provider, ok := providerForSnapshot(snapshot, "openai")
	if !ok {
		return api.ProviderRoute{}, fmt.Errorf("provider %q not found in catalog", "openai")
	}

	spec, ok := api.ProviderSpecFor("codex")
	if !ok {
		return api.ProviderRoute{}, fmt.Errorf("provider %q not found in runtime registry", "codex")
	}

	modelID = strings.TrimSpace(modelID)
	if modelID == "" {
		modelID = spec.DefaultModel
	}

	model, ok := modelForProvider(provider, modelID)
	if !ok {
		model = fallbackModel("openai", modelID)
	}

	baseURL := providerBaseURL(cfg, "codex", spec.BaseURL)
	return api.ProviderRoute{
		ProviderID:   "codex",
		ModelID:      model.ID,
		Protocol:     spec.Protocol,
		BaseURL:      baseURL,
		Capabilities: model.Capabilities,
	}, nil
}

func buildProvider(base modelsdev.Snapshot, cfg config.Config, runtime runtimeProvider) Provider {
	provider := Provider{
		ID:       runtime.CatalogID,
		Name:     runtime.Spec.DisplayName,
		BaseURL:  runtime.Spec.BaseURL,
		Protocol: runtime.Spec.Protocol,
		Source:   runtime.Source,
	}

	if sourceProvider, ok := base.Providers[runtime.SourceID]; ok {
		provider.Name = firstNonEmpty(sourceProvider.Name, provider.Name)
		provider.BaseURL = firstNonEmpty(sourceProvider.API, provider.BaseURL)
		provider.EnvKeys = append([]string(nil), sourceProvider.Env...)
		provider.Models = buildModels(runtime.CatalogID, sourceProvider.Models)
		provider.Source = ProviderSourceModelsDev
	}

	provider.BaseURL = providerBaseURL(cfg, runtime.CatalogID, provider.BaseURL)

	if envKey := strings.TrimSpace(cfg.ProviderAPIKeyEnv(runtime.CatalogID, runtime.Spec.EnvKeyVar)); envKey != "" {
		provider.EnvKeys = appendUnique(provider.EnvKeys, envKey)
	}

	provider.Models = ensureModel(provider.Models, runtime.Spec.DefaultModel, fallbackModel(runtime.CatalogID, runtime.Spec.DefaultModel))
	if overrideModel := strings.TrimSpace(cfg.ProviderDefaultModel(runtime.CatalogID, runtime.Spec.DefaultModel)); overrideModel != "" {
		provider.Models = ensureModel(provider.Models, overrideModel, fallbackModel(runtime.CatalogID, overrideModel))
		provider.DefaultModel = overrideModel
	} else {
		provider.DefaultModel = chooseDefaultModel(provider.Models, runtime.Spec.DefaultModel)
	}

	if provider.DefaultModel == "" {
		provider.DefaultModel = chooseDefaultModel(provider.Models, "")
	}
	provider.Auth = resolveAuthStatus(cfg, provider, runtime.Spec.EnvKeyVar)

	if provider.Name == "" || (len(provider.Models) == 0 && provider.DefaultModel == "") {
		return Provider{}
	}

	return provider
}

func buildConfigProvider(cfg config.Config, providerID string, override config.ProviderOverride) Provider {
	providerID = normalizeProviderID(providerID)
	if providerID == "" || providerID == "codex" {
		return Provider{}
	}

	baseURL := strings.TrimSpace(override.BaseURL)
	envKey := strings.TrimSpace(override.APIKeyEnv)
	defaultModel := strings.TrimSpace(override.DefaultModel)
	if normalizeProviderID(cfg.Provider) == providerID && strings.TrimSpace(cfg.Model) != "" {
		defaultModel = strings.TrimSpace(cfg.Model)
	}
	if baseURL == "" && envKey == "" && defaultModel == "" {
		return Provider{}
	}

	models := []Model(nil)
	if defaultModel != "" {
		models = ensureModel(models, defaultModel, fallbackModel(providerID, defaultModel))
	}

	provider := Provider{
		ID:           providerID,
		Name:         providerID,
		BaseURL:      providerBaseURL(cfg, providerID, baseURL),
		Protocol:     api.OpenAICompatAPI,
		Source:       ProviderSourceConfig,
		DefaultModel: defaultModel,
		Models:       models,
	}
	if envKey != "" {
		provider.EnvKeys = []string{envKey}
	}
	provider.Auth = resolveAuthStatus(cfg, provider, envKey)
	return provider
}

func buildModels(providerID string, source map[string]modelsdev.Model) []Model {
	models := make([]Model, 0, len(source))
	for modelID, sourceModel := range source {
		if !includeModelStatus(sourceModel.Status) {
			continue
		}
		models = append(models, normalizeModel(providerID, modelID, sourceModel))
	}

	slices.SortFunc(models, func(a, b Model) int {
		return cmp.Or(strings.Compare(a.Name, b.Name), strings.Compare(a.ID, b.ID))
	})
	return models
}

func includeModelStatus(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "deprecated", "alpha":
		return false
	default:
		return true
	}
}

func normalizeModel(providerID string, modelID string, source modelsdev.Model) Model {
	capabilities := api.ModelCapabilities{
		SupportsToolUse:          source.ToolCall,
		SupportsExtendedThinking: source.Reasoning,
		SupportsVision:           supportsVision(source.Modalities.Input),
		SupportsJsonMode:         source.StructuredOutput,
		SupportsCaching:          source.Cost.CacheRead > 0 || source.Cost.CacheWrite > 0,
		MaxContextWindow:         source.Limit.Context,
		MaxPromptTokens:          source.Limit.Input,
		MaxOutputTokens:          source.Limit.Output,
	}
	if capabilities == (api.ModelCapabilities{}) {
		capabilities = api.ResolveModelCapabilities(providerID, modelID)
	}

	return Model{
		ID:           firstNonEmpty(source.ID, modelID),
		Name:         firstNonEmpty(source.Name, modelID),
		Family:       source.Family,
		Status:       source.Status,
		Capabilities: capabilities,
		Limits: ModelLimits{
			ContextWindow: source.Limit.Context,
			PromptTokens:  source.Limit.Input,
			OutputTokens:  source.Limit.Output,
		},
		Cost: ModelCost{
			Input:      source.Cost.Input,
			Output:     source.Cost.Output,
			CacheRead:  source.Cost.CacheRead,
			CacheWrite: source.Cost.CacheWrite,
		},
		API: ModelAPI{
			Attachment:       source.Attachment,
			Reasoning:        source.Reasoning,
			ToolCall:         source.ToolCall,
			Temperature:      source.Temperature,
			StructuredOutput: source.StructuredOutput,
			OpenWeights:      source.OpenWeights,
			InputModalities:  append([]string(nil), source.Modalities.Input...),
			OutputModalities: append([]string(nil), source.Modalities.Output...),
		},
	}
}

func fallbackModel(providerID string, modelID string) Model {
	capabilities := api.ResolveModelCapabilities(providerID, modelID)
	return Model{
		ID:           modelID,
		Name:         modelID,
		Family:       inferFamily(modelID),
		Capabilities: capabilities,
		Limits: ModelLimits{
			ContextWindow: capabilities.MaxContextWindow,
			PromptTokens:  capabilities.MaxPromptTokens,
			OutputTokens:  capabilities.MaxOutputTokens,
		},
	}
}

func modelForProvider(provider Provider, modelID string) (Model, bool) {
	modelID = strings.TrimSpace(modelID)
	for _, model := range provider.Models {
		if model.ID == modelID {
			return model, true
		}
	}
	return Model{}, false
}

func providerForSnapshot(snapshot Snapshot, providerID string) (Provider, bool) {
	providerID = normalizeProviderID(providerID)
	for _, provider := range snapshot.Providers {
		if provider.ID == providerID {
			return provider, true
		}
	}
	return Provider{}, false
}

func providerBaseURL(cfg config.Config, providerID string, fallback string) string {
	providerID = normalizeProviderID(providerID)
	if providerID != "" && cfg.Providers != nil {
		if baseURL := strings.TrimSpace(cfg.Providers[providerID].BaseURL); baseURL != "" {
			return baseURL
		}
	}
	if normalizeProviderID(cfg.Provider) == providerID {
		if baseURL := strings.TrimSpace(cfg.BaseURL); baseURL != "" {
			return baseURL
		}
	}
	return strings.TrimSpace(fallback)
}

func ensureModel(models []Model, modelID string, fallback Model) []Model {
	modelID = strings.TrimSpace(modelID)
	if modelID == "" {
		return models
	}
	for _, model := range models {
		if model.ID == modelID {
			return models
		}
	}
	models = append(models, fallback)
	slices.SortFunc(models, func(a, b Model) int {
		return cmp.Or(strings.Compare(a.Name, b.Name), strings.Compare(a.ID, b.ID))
	})
	return models
}

func chooseDefaultModel(models []Model, preferred string) string {
	preferred = strings.TrimSpace(preferred)
	if preferred != "" {
		for _, model := range models {
			if model.ID == preferred {
				return preferred
			}
		}
	}
	if len(models) == 0 {
		return ""
	}
	return models[0].ID
}

func resolveActive(cfg config.Config, defaults map[string]string) ModelRef {
	if providerID := normalizeProviderID(cfg.Provider); providerID != "" {
		modelID := strings.TrimSpace(cfg.Model)
		if modelID == "" {
			modelID = defaults[providerID]
		}
		return ModelRef{ProviderID: providerID, ModelID: modelID}
	}

	resolved := modelselection.Resolve(strings.TrimSpace(cfg.Model), "", cfg.ModelSource)
	providerID := normalizeProviderID(resolved.Resolved.ProviderID)
	modelID := strings.TrimSpace(resolved.Resolved.ModelID)
	if providerID != "" && modelID == "" {
		modelID = defaults[providerID]
	}
	return ModelRef{ProviderID: providerID, ModelID: modelID}
}

func appendUnique(values []string, value string) []string {
	value = strings.TrimSpace(value)
	if value == "" {
		return values
	}
	for _, existing := range values {
		if strings.EqualFold(existing, value) {
			return values
		}
	}
	return append(values, value)
}

func supportsVision(modalities []string) bool {
	for _, modality := range modalities {
		switch strings.ToLower(strings.TrimSpace(modality)) {
		case "image", "video":
			return true
		}
	}
	return false
}

func inferFamily(modelID string) string {
	if spec, ok := api.ModelSpecFor(modelID); ok {
		return spec.Family
	}
	return ""
}

func providerPriority(providerID string) int {
	if spec, ok := api.ProviderSpecFor(providerID); ok {
		return spec.Priority
	}
	return 1_000_000
}

func normalizeProviderID(providerID string) string {
	return strings.ToLower(strings.TrimSpace(providerID))
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			return value
		}
	}
	return ""
}

func mustProviderSpec(providerID string) api.ProviderSpec {
	spec, ok := api.ProviderSpecFor(providerID)
	if !ok {
		panic("missing provider spec: " + providerID)
	}
	return spec
}

func resolveAuthStatus(cfg config.Config, provider Provider, defaultEnvKey string) AuthStatus {
	status := AuthStatus{
		Source:    "none",
		SetupHint: providerSetupHint(provider.ID, cfg.ProviderAPIKeyEnv(provider.ID, defaultEnvKey)),
	}

	switch provider.ID {
	case "github-copilot":
		return resolveGitHubCopilotAuthStatus(cfg, provider.ID, status)
	case "codex":
		return resolveCodexAuthStatus(cfg, provider.ID, cfg.ProviderAPIKeyEnv(provider.ID, defaultEnvKey), status)
	case "ollama":
		return resolveOllamaAuthStatus(cfg, provider.ID)
	default:
		return resolveAPIKeyAuthStatus(cfg, provider.ID, cfg.ProviderAPIKeyEnv(provider.ID, defaultEnvKey), status)
	}
}

func resolveCodexAuthStatus(cfg config.Config, providerID string, envKey string, status AuthStatus) AuthStatus {
	if hasActiveOverrideAPIKey(cfg, providerID) {
		status.Source = "env:NAMI_API_KEY"
		status.Configured = true
		status.Usable = true
		status.SetupHint = ""
		return status
	}

	if envKey != "" && strings.TrimSpace(os.Getenv(envKey)) != "" {
		status.Source = "env:" + envKey
		status.Configured = true
		status.Usable = true
		status.SetupHint = ""
		return status
	}

	creds := cfg.Codex
	if strings.TrimSpace(creds.RefreshToken) != "" {
		status.Source = "stored OAuth"
		status.Configured = true
		status.Usable = true
		status.SetupHint = ""
		return status
	}

	if strings.TrimSpace(creds.AccessToken) == "" {
		return status
	}

	status.Source = "stored access token"
	status.Configured = true
	if creds.ExpiresAtUnixMS > 0 && time.Now().UnixMilli() > creds.ExpiresAtUnixMS {
		status.LastError = "saved access token expired"
		status.SetupHint = "Run /connect codex to refresh credentials."
		return status
	}
	status.Usable = true
	status.SetupHint = ""
	return status
}

func resolveGitHubCopilotAuthStatus(cfg config.Config, providerID string, status AuthStatus) AuthStatus {
	if hasActiveOverrideAPIKey(cfg, providerID) {
		status.Source = "env:NAMI_API_KEY"
		status.Configured = true
		status.Usable = true
		status.SetupHint = ""
		return status
	}

	creds := cfg.GitHubCopilot
	if strings.TrimSpace(creds.GitHubToken) != "" {
		status.Source = "stored device auth"
		status.Configured = true
		status.Usable = true
		status.SetupHint = ""
		return status
	}

	if strings.TrimSpace(creds.AccessToken) == "" {
		return status
	}

	status.Source = "stored access token"
	status.Configured = true
	if creds.ExpiresAtUnixMS > 0 && time.Now().UnixMilli() > creds.ExpiresAtUnixMS {
		status.LastError = "saved access token expired"
		status.SetupHint = "Run /connect github-copilot to refresh credentials."
		return status
	}
	status.Usable = true
	status.SetupHint = ""
	return status
}

func resolveAPIKeyAuthStatus(cfg config.Config, providerID string, envKey string, status AuthStatus) AuthStatus {
	if hasActiveOverrideAPIKey(cfg, providerID) {
		status.Source = "env:NAMI_API_KEY"
		status.Configured = true
		status.Usable = true
		status.SetupHint = ""
		return status
	}

	if envKey != "" && strings.TrimSpace(os.Getenv(envKey)) != "" {
		status.Source = "env:" + envKey
		status.Configured = true
		status.Usable = true
		status.SetupHint = ""
	}
	return status
}

func resolveOllamaAuthStatus(cfg config.Config, providerID string) AuthStatus {
	status := AuthStatus{
		Configured: true,
		Usable:     true,
		SetupHint:  "Ensure Ollama is running on http://localhost:11434.",
	}
	if hasActiveOverrideAPIKey(cfg, providerID) {
		status.Source = "env:NAMI_API_KEY"
		return status
	}
	if strings.TrimSpace(os.Getenv("OLLAMA_API_KEY")) != "" {
		status.Source = "env:OLLAMA_API_KEY"
		return status
	}
	status.Source = "local"
	return status
}

func hasActiveOverrideAPIKey(cfg config.Config, providerID string) bool {
	return normalizeProviderID(cfg.Provider) == providerID && strings.TrimSpace(cfg.APIKey) != ""
}

func providerSetupHint(providerID string, envKey string) string {
	switch providerID {
	case "github-copilot":
		return "Run /connect github-copilot."
	case "codex":
		return "Run /connect codex or set CODEX_ACCESS_TOKEN."
	case "ollama":
		return "Ensure Ollama is running on http://localhost:11434."
	default:
		if envKey == "" {
			return "Provider setup is required."
		}
		return fmt.Sprintf("Set %s.", envKey)
	}
}
