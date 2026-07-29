package modelsdev

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/channyeintun/nami/internal/config"
)

const (
	defaultSourceURL   = "https://models.dev/api.json"
	defaultUserAgent   = "nami/0.1 (+https://github.com/channyeintun/nami)"
	defaultCacheTTL    = 24 * time.Hour
	defaultHTTPTimeout = 30 * time.Second
	cacheFileName      = "api.json"
	// maxSnapshotBytes bounds the downloaded catalog so a wrong or hostile
	// endpoint cannot stream unbounded data into memory.
	maxSnapshotBytes int64 = 32 * 1024 * 1024
)

type Client struct {
	CachePath  string
	SourceURL  string
	HTTPClient *http.Client
	TTL        time.Duration
	UserAgent  string
}

type Snapshot struct {
	Providers map[string]Provider
	FetchedAt time.Time
	RawJSON   []byte
}

type Provider struct {
	ID     string           `json:"id"`
	Name   string           `json:"name"`
	API    string           `json:"api,omitempty"`
	Doc    string           `json:"doc,omitempty"`
	NPM    string           `json:"npm,omitempty"`
	Env    []string         `json:"env,omitempty"`
	Models map[string]Model `json:"models"`
}

type Model struct {
	ID               string         `json:"id"`
	Name             string         `json:"name"`
	Family           string         `json:"family,omitempty"`
	Knowledge        string         `json:"knowledge,omitempty"`
	ReleaseDate      string         `json:"release_date,omitempty"`
	LastUpdated      string         `json:"last_updated,omitempty"`
	Status           string         `json:"status,omitempty"`
	Attachment       bool           `json:"attachment,omitempty"`
	Reasoning        bool           `json:"reasoning,omitempty"`
	ToolCall         bool           `json:"tool_call,omitempty"`
	Temperature      bool           `json:"temperature,omitempty"`
	StructuredOutput bool           `json:"structured_output,omitempty"`
	OpenWeights      bool           `json:"open_weights,omitempty"`
	Modalities       Modalities     `json:"modalities"`
	Limit            Limit          `json:"limit"`
	Cost             Cost           `json:"cost"`
	Experimental     Experimental   `json:"experimental"`
	Extra            map[string]any `json:"-"`
}

type Modalities struct {
	Input  []string `json:"input,omitempty"`
	Output []string `json:"output,omitempty"`
}

type Limit struct {
	Context int `json:"context,omitempty"`
	Input   int `json:"input,omitempty"`
	Output  int `json:"output,omitempty"`
}

type Cost struct {
	Input      float64        `json:"input,omitempty"`
	Output     float64        `json:"output,omitempty"`
	CacheRead  float64        `json:"cache_read,omitempty"`
	CacheWrite float64        `json:"cache_write,omitempty"`
	InputAudio float64        `json:"input_audio,omitempty"`
	Tiers      []CostTier     `json:"tiers,omitempty"`
	Extra      map[string]any `json:"-"`
}

type CostTier struct {
	Input      float64        `json:"input,omitempty"`
	Output     float64        `json:"output,omitempty"`
	CacheRead  float64        `json:"cache_read,omitempty"`
	CacheWrite float64        `json:"cache_write,omitempty"`
	Tier       map[string]any `json:"tier,omitempty"`
}

type Experimental struct {
	Modes map[string]ExperimentalMode `json:"modes,omitempty"`
	Extra map[string]any              `json:"-"`
}

type ExperimentalMode struct {
	Cost     Cost           `json:"cost"`
	Provider map[string]any `json:"provider,omitempty"`
	Extra    map[string]any `json:"-"`
}

func NewClient() *Client {
	return &Client{}
}

func (c *Client) Load(ctx context.Context) (Snapshot, error) {
	if cached, ok, err := c.loadFreshCache(); err != nil {
		return Snapshot{}, err
	} else if ok {
		return cached, nil
	}

	snapshot, err := c.fetchAndCache(ctx)
	if err == nil {
		return snapshot, nil
	}

	stale, staleErr := c.loadAnyCache()
	if staleErr == nil {
		return stale, nil
	}

	return Snapshot{}, fmt.Errorf("fetch models.dev snapshot: %w", err)
}

func (c *Client) Refresh(ctx context.Context) (Snapshot, error) {
	snapshot, err := c.fetchAndCache(ctx)
	if err == nil {
		return snapshot, nil
	}

	stale, staleErr := c.loadAnyCache()
	if staleErr == nil {
		return stale, nil
	}

	return Snapshot{}, fmt.Errorf("refresh models.dev snapshot: %w", err)
}

func (c *Client) loadFreshCache() (Snapshot, bool, error) {
	cachePath := c.cachePath()
	info, err := os.Stat(cachePath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Snapshot{}, false, nil
		}
		return Snapshot{}, false, fmt.Errorf("stat models.dev cache: %w", err)
	}

	if time.Since(info.ModTime()) > c.ttl() {
		return Snapshot{}, false, nil
	}

	snapshot, err := c.loadSnapshotFromPath(cachePath)
	if err != nil {
		return Snapshot{}, false, err
	}
	return snapshot, true, nil
}

func (c *Client) loadAnyCache() (Snapshot, error) {
	return c.loadSnapshotFromPath(c.cachePath())
}

func (c *Client) loadSnapshotFromPath(path string) (Snapshot, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Snapshot{}, fmt.Errorf("read models.dev cache: %w", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		return Snapshot{}, fmt.Errorf("stat models.dev cache: %w", err)
	}

	snapshot, err := parseSnapshot(data)
	if err != nil {
		return Snapshot{}, fmt.Errorf("parse models.dev cache: %w", err)
	}
	snapshot.FetchedAt = info.ModTime()
	snapshot.RawJSON = data
	return snapshot, nil
}

func (c *Client) fetchAndCache(ctx context.Context) (Snapshot, error) {
	data, err := c.fetch(ctx)
	if err != nil {
		return Snapshot{}, err
	}

	snapshot, err := parseSnapshot(data)
	if err != nil {
		return Snapshot{}, fmt.Errorf("parse models.dev response: %w", err)
	}

	if err := c.writeCache(data); err != nil {
		return Snapshot{}, err
	}

	info, err := os.Stat(c.cachePath())
	if err != nil {
		return Snapshot{}, fmt.Errorf("stat models.dev cache: %w", err)
	}

	snapshot.FetchedAt = info.ModTime()
	snapshot.RawJSON = data
	return snapshot, nil
}

func (c *Client) fetch(ctx context.Context) ([]byte, error) {
	requestCtx := ctx
	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		var cancel context.CancelFunc
		requestCtx, cancel = context.WithTimeout(ctx, defaultHTTPTimeout)
		defer cancel()
	}

	request, err := http.NewRequestWithContext(requestCtx, http.MethodGet, c.sourceURL(), nil)
	if err != nil {
		return nil, fmt.Errorf("create models.dev request: %w", err)
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("User-Agent", c.userAgent())

	response, err := c.httpClient().Do(request)
	if err != nil {
		return nil, fmt.Errorf("execute models.dev request: %w", err)
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusOK {
		body, readErr := io.ReadAll(io.LimitReader(response.Body, 4096))
		if readErr != nil {
			return nil, fmt.Errorf("models.dev request failed with status %s", response.Status)
		}
		message := strings.TrimSpace(string(body))
		if message == "" {
			return nil, fmt.Errorf("models.dev request failed with status %s", response.Status)
		}
		return nil, fmt.Errorf("models.dev request failed with status %s: %s", response.Status, message)
	}

	data, err := io.ReadAll(io.LimitReader(response.Body, maxSnapshotBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read models.dev response: %w", err)
	}
	if int64(len(data)) > maxSnapshotBytes {
		return nil, fmt.Errorf("models.dev response exceeded %d bytes", maxSnapshotBytes)
	}
	return data, nil
}

// writeCache replaces the cache file in one step. Writing in place would leave
// a truncated file behind if the process stopped mid-write, and the next start
// would fail to parse its own cache.
func (c *Client) writeCache(data []byte) error {
	cachePath := c.cachePath()
	if err := os.MkdirAll(filepath.Dir(cachePath), 0o755); err != nil {
		return fmt.Errorf("create models.dev cache directory: %w", err)
	}

	tmp, err := os.CreateTemp(filepath.Dir(cachePath), "."+filepath.Base(cachePath)+"-*.tmp")
	if err != nil {
		return fmt.Errorf("create models.dev cache temp file: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write models.dev cache: %w", err)
	}
	if err := tmp.Chmod(0o644); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("set models.dev cache permissions: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close models.dev cache temp file: %w", err)
	}
	if err := os.Rename(tmpName, cachePath); err != nil {
		return fmt.Errorf("replace models.dev cache: %w", err)
	}
	return nil
}

func parseSnapshot(data []byte) (Snapshot, error) {
	providers := make(map[string]Provider)
	if err := json.Unmarshal(data, &providers); err != nil {
		return Snapshot{}, err
	}

	for providerID, provider := range providers {
		if strings.TrimSpace(provider.ID) == "" {
			provider.ID = providerID
		}
		if provider.Models == nil {
			provider.Models = make(map[string]Model)
		}
		for modelID, model := range provider.Models {
			if strings.TrimSpace(model.ID) == "" {
				model.ID = modelID
			}
			provider.Models[modelID] = model
		}
		providers[providerID] = provider
	}

	return Snapshot{Providers: providers}, nil
}

func (c *Client) cachePath() string {
	if path := strings.TrimSpace(c.CachePath); path != "" {
		return path
	}
	return filepath.Join(config.CacheDir(), "models.dev", cacheFileName)
}

func (c *Client) sourceURL() string {
	if url := strings.TrimSpace(c.SourceURL); url != "" {
		return url
	}
	return defaultSourceURL
}

func (c *Client) ttl() time.Duration {
	if c.TTL > 0 {
		return c.TTL
	}
	return defaultCacheTTL
}

func (c *Client) userAgent() string {
	if userAgent := strings.TrimSpace(c.UserAgent); userAgent != "" {
		return userAgent
	}
	return defaultUserAgent
}

func (c *Client) httpClient() *http.Client {
	if c.HTTPClient != nil {
		return c.HTTPClient
	}
	return &http.Client{Timeout: defaultHTTPTimeout}
}

func (m *Model) UnmarshalJSON(data []byte) error {
	type alias Model
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}

	var decoded alias
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}

	delete(raw, "id")
	delete(raw, "name")
	delete(raw, "family")
	delete(raw, "knowledge")
	delete(raw, "release_date")
	delete(raw, "last_updated")
	delete(raw, "status")
	delete(raw, "attachment")
	delete(raw, "reasoning")
	delete(raw, "tool_call")
	delete(raw, "temperature")
	delete(raw, "structured_output")
	delete(raw, "open_weights")
	delete(raw, "modalities")
	delete(raw, "limit")
	delete(raw, "cost")
	delete(raw, "experimental")

	extra := make(map[string]any, len(raw))
	for key, value := range raw {
		var decodedValue any
		if err := json.Unmarshal(value, &decodedValue); err != nil {
			return fmt.Errorf("decode models.dev model field %q: %w", key, err)
		}
		extra[key] = decodedValue
	}

	*m = Model(decoded)
	if len(extra) > 0 {
		m.Extra = extra
	}
	return nil
}

func (c *Cost) UnmarshalJSON(data []byte) error {
	type alias Cost
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}

	var decoded alias
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}

	delete(raw, "input")
	delete(raw, "output")
	delete(raw, "cache_read")
	delete(raw, "cache_write")
	delete(raw, "input_audio")
	delete(raw, "tiers")

	extra := make(map[string]any, len(raw))
	for key, value := range raw {
		var decodedValue any
		if err := json.Unmarshal(value, &decodedValue); err != nil {
			return fmt.Errorf("decode models.dev cost field %q: %w", key, err)
		}
		extra[key] = decodedValue
	}

	*c = Cost(decoded)
	if len(extra) > 0 {
		c.Extra = extra
	}
	return nil
}

func (e *Experimental) UnmarshalJSON(data []byte) error {
	type alias Experimental
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}

	var decoded alias
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}

	delete(raw, "modes")

	extra := make(map[string]any, len(raw))
	for key, value := range raw {
		var decodedValue any
		if err := json.Unmarshal(value, &decodedValue); err != nil {
			return fmt.Errorf("decode models.dev experimental field %q: %w", key, err)
		}
		extra[key] = decodedValue
	}

	*e = Experimental(decoded)
	if len(extra) > 0 {
		e.Extra = extra
	}
	return nil
}

func (m *ExperimentalMode) UnmarshalJSON(data []byte) error {
	type alias ExperimentalMode
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}

	var decoded alias
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}

	delete(raw, "cost")
	delete(raw, "provider")

	extra := make(map[string]any, len(raw))
	for key, value := range raw {
		var decodedValue any
		if err := json.Unmarshal(value, &decodedValue); err != nil {
			return fmt.Errorf("decode models.dev experimental mode field %q: %w", key, err)
		}
		extra[key] = decodedValue
	}

	*m = ExperimentalMode(decoded)
	if len(extra) > 0 {
		m.Extra = extra
	}
	return nil
}
