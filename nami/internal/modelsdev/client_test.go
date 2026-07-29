package modelsdev

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const samplePayload = `{
  "anthropic": {
    "name": "Anthropic",
    "api": "https://api.anthropic.com",
    "env": ["ANTHROPIC_API_KEY"],
    "models": {
      "claude-opus-5": {
        "name": "Claude Opus 5",
        "family": "claude",
        "tool_call": true,
        "reasoning": true,
        "modalities": {"input": ["text", "image"], "output": ["text"]},
        "limit": {"context": 200000, "output": 64000},
        "cost": {"input": 5, "output": 25}
      }
    }
  }
}`

func testClient(t *testing.T, handler http.HandlerFunc) (*Client, *httptest.Server) {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	return &Client{
		SourceURL: server.URL,
		CachePath: filepath.Join(t.TempDir(), "cache", "api.json"),
	}, server
}

func TestLoadFetchesAndCaches(t *testing.T) {
	var requests atomic.Int32
	client, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if got := r.Header.Get("Accept"); got != "application/json" {
			t.Errorf("Accept header = %q", got)
		}
		if r.Header.Get("User-Agent") == "" {
			t.Error("request has no User-Agent")
		}
		fmt.Fprint(w, samplePayload)
	})

	snapshot, err := client.Load(context.Background())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	provider, ok := snapshot.Providers["anthropic"]
	if !ok {
		t.Fatalf("providers = %+v", snapshot.Providers)
	}
	if provider.ID != "anthropic" {
		t.Errorf("provider id = %q, want the map key as a fallback", provider.ID)
	}
	model, ok := provider.Models["claude-opus-5"]
	if !ok {
		t.Fatalf("models = %+v", provider.Models)
	}
	if model.ID != "claude-opus-5" {
		t.Errorf("model id = %q, want the map key as a fallback", model.ID)
	}
	if !model.ToolCall || !model.Reasoning {
		t.Errorf("model flags = %+v", model)
	}
	if model.Limit.Context != 200000 || model.Limit.Output != 64000 {
		t.Errorf("limits = %+v", model.Limit)
	}
	if snapshot.FetchedAt.IsZero() {
		t.Error("FetchedAt was not set")
	}
	if len(snapshot.RawJSON) == 0 {
		t.Error("RawJSON was not retained")
	}

	// A second Load is served from the fresh cache.
	if _, err := client.Load(context.Background()); err != nil {
		t.Fatalf("second Load: %v", err)
	}
	if got := requests.Load(); got != 1 {
		t.Fatalf("server saw %d requests, want 1", got)
	}
}

func TestLoadRefetchesAfterTTL(t *testing.T) {
	var requests atomic.Int32
	client, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		fmt.Fprint(w, samplePayload)
	})
	client.TTL = time.Nanosecond

	if _, err := client.Load(context.Background()); err != nil {
		t.Fatalf("Load: %v", err)
	}
	time.Sleep(2 * time.Millisecond)
	if _, err := client.Load(context.Background()); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := requests.Load(); got != 2 {
		t.Fatalf("server saw %d requests, want the stale cache to be refreshed", got)
	}
}

func TestLoadFallsBackToStaleCache(t *testing.T) {
	failing := int32(0)
	client, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if atomic.LoadInt32(&failing) == 1 {
			http.Error(w, "upstream down", http.StatusBadGateway)
			return
		}
		fmt.Fprint(w, samplePayload)
	})
	client.TTL = time.Nanosecond

	if _, err := client.Load(context.Background()); err != nil {
		t.Fatalf("Load: %v", err)
	}
	atomic.StoreInt32(&failing, 1)
	time.Sleep(2 * time.Millisecond)

	snapshot, err := client.Load(context.Background())
	if err != nil {
		t.Fatalf("Load should fall back to the stale cache: %v", err)
	}
	if _, ok := snapshot.Providers["anthropic"]; !ok {
		t.Fatalf("stale snapshot = %+v", snapshot.Providers)
	}
}

func TestLoadFailsWithoutCacheOrNetwork(t *testing.T) {
	client, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", http.StatusInternalServerError)
	})
	if _, err := client.Load(context.Background()); err == nil {
		t.Fatal("Load succeeded with no cache and a failing endpoint")
	}
}

func TestRefreshAlwaysFetches(t *testing.T) {
	var requests atomic.Int32
	client, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		fmt.Fprint(w, samplePayload)
	})

	for range 3 {
		if _, err := client.Refresh(context.Background()); err != nil {
			t.Fatalf("Refresh: %v", err)
		}
	}
	if got := requests.Load(); got != 3 {
		t.Fatalf("server saw %d requests, want 3", got)
	}
}

func TestFetchRejectsMalformedPayload(t *testing.T) {
	client, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "{not json")
	})
	if _, err := client.Refresh(context.Background()); err == nil {
		t.Fatal("Refresh accepted a malformed payload")
	}
	if _, err := os.Stat(client.cachePath()); err == nil {
		t.Fatal("a malformed payload must not be cached")
	}
}

func TestFetchReportsHTTPStatusAndBody(t *testing.T) {
	client, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "rate limited", http.StatusTooManyRequests)
	})
	_, err := client.Refresh(context.Background())
	if err == nil {
		t.Fatal("Refresh returned no error")
	}
	if !strings.Contains(err.Error(), "429") || !strings.Contains(err.Error(), "rate limited") {
		t.Fatalf("error = %v, want the status and body", err)
	}
}

func TestFetchRejectsOversizedPayload(t *testing.T) {
	client, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		chunk := strings.Repeat("a", 1<<20)
		for range (maxSnapshotBytes / (1 << 20)) + 1 {
			if _, err := w.Write([]byte(chunk)); err != nil {
				return
			}
		}
	})
	_, err := client.Refresh(context.Background())
	if err == nil || !strings.Contains(err.Error(), "exceeded") {
		t.Fatalf("error = %v, want a size-limit error", err)
	}
}

func TestFetchHonoursContextCancellation(t *testing.T) {
	client, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, samplePayload)
	})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := client.Refresh(ctx); err == nil {
		t.Fatal("Refresh ignored a cancelled context")
	}
}

func TestWriteCacheReplacesFileAtomically(t *testing.T) {
	client, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, samplePayload)
	})
	if err := client.writeCache([]byte(`{"a":{}}`)); err != nil {
		t.Fatalf("writeCache: %v", err)
	}
	if err := client.writeCache([]byte(samplePayload)); err != nil {
		t.Fatalf("writeCache: %v", err)
	}

	entries, err := os.ReadDir(filepath.Dir(client.cachePath()))
	if err != nil {
		t.Fatalf("read cache dir: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("cache dir holds %d entries, want only the cache file", len(entries))
	}
	data, err := os.ReadFile(client.cachePath())
	if err != nil {
		t.Fatalf("read cache: %v", err)
	}
	if string(data) != samplePayload {
		t.Fatal("cache does not hold the latest payload")
	}
}

func TestLoadReportsCorruptCache(t *testing.T) {
	client, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, samplePayload)
	})
	if err := os.MkdirAll(filepath.Dir(client.cachePath()), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(client.cachePath(), []byte("{corrupt"), 0o644); err != nil {
		t.Fatalf("write cache: %v", err)
	}
	if _, err := client.Load(context.Background()); err == nil {
		t.Fatal("Load returned no error for a corrupt fresh cache")
	}
}

func TestParseSnapshotFillsMissingIdentifiers(t *testing.T) {
	snapshot, err := parseSnapshot([]byte(`{"acme":{"models":{"m1":{}}}}`))
	if err != nil {
		t.Fatalf("parseSnapshot: %v", err)
	}
	provider := snapshot.Providers["acme"]
	if provider.ID != "acme" {
		t.Errorf("provider id = %q", provider.ID)
	}
	if provider.Models["m1"].ID != "m1" {
		t.Errorf("model id = %q", provider.Models["m1"].ID)
	}

	empty, err := parseSnapshot([]byte(`{"acme":{}}`))
	if err != nil {
		t.Fatalf("parseSnapshot: %v", err)
	}
	if empty.Providers["acme"].Models == nil {
		t.Error("a provider without models should still get an initialized map")
	}
}

func TestModelUnmarshalKeepsUnknownFieldsAsExtra(t *testing.T) {
	var model Model
	if err := json.Unmarshal([]byte(`{"id":"m","name":"M","future_flag":true}`), &model); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if model.ID != "m" || model.Name != "M" {
		t.Fatalf("model = %+v", model)
	}
	if model.Extra["future_flag"] != true {
		t.Fatalf("extra = %+v, want unknown fields preserved", model.Extra)
	}
}

func TestClientDefaults(t *testing.T) {
	client := NewClient()
	if client.ttl() != defaultCacheTTL {
		t.Errorf("ttl = %v, want the default", client.ttl())
	}
	if client.sourceURL() != defaultSourceURL {
		t.Errorf("source url = %q", client.sourceURL())
	}
	if client.userAgent() != defaultUserAgent {
		t.Errorf("user agent = %q", client.userAgent())
	}
	if client.httpClient() == nil {
		t.Error("httpClient returned nil")
	}
	if !strings.HasSuffix(client.cachePath(), filepath.Join("models.dev", cacheFileName)) {
		t.Errorf("cache path = %q", client.cachePath())
	}

	custom := &Client{SourceURL: " https://example.com ", CachePath: " /tmp/x ", TTL: time.Minute}
	if custom.sourceURL() != "https://example.com" {
		t.Errorf("source url = %q, want it trimmed", custom.sourceURL())
	}
	if custom.cachePath() != "/tmp/x" {
		t.Errorf("cache path = %q, want it trimmed", custom.cachePath())
	}
	if custom.ttl() != time.Minute {
		t.Errorf("ttl = %v", custom.ttl())
	}
}
