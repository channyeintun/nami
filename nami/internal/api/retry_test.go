package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

func TestRetryAfterHeaderDelay(t *testing.T) {
	cases := []struct {
		name   string
		header http.Header
		want   time.Duration
	}{
		{"none", http.Header{}, 0},
		{"seconds", http.Header{"Retry-After": {"3"}}, 3 * time.Second},
		{"fractional seconds", http.Header{"Retry-After": {"1.5"}}, 1500 * time.Millisecond},
		{"milliseconds", http.Header{"Retry-After-Ms": {"250"}}, 250 * time.Millisecond},
		{"milliseconds win", http.Header{"Retry-After-Ms": {"250"}, "Retry-After": {"1"}}, 250 * time.Millisecond},
		{"past date", http.Header{"Retry-After": {"Mon, 02 Jan 2006 15:04:05 GMT"}}, 0},
		{"garbage", http.Header{"Retry-After": {"soon"}}, 0},
		{"negative", http.Header{"Retry-After": {"-4"}}, 0},
	}
	for _, tc := range cases {
		if got := retryAfterHeaderDelay(tc.header); got != tc.want {
			t.Errorf("%s: delay = %v, want %v", tc.name, got, tc.want)
		}
	}

	// An HTTP date in the future is a delay until then.
	future := http.Header{"Retry-After": {time.Now().Add(30 * time.Second).UTC().Format(http.TimeFormat)}}
	if got := retryAfterHeaderDelay(future); got < 25*time.Second || got > 30*time.Second {
		t.Errorf("future date: delay = %v, want about 30s", got)
	}
}

func TestWithRetryAfterKeepsBackoffForLongDelays(t *testing.T) {
	short := withRetryAfter(&APIError{Type: ErrRateLimit}, 5*time.Second).(*APIError)
	if short.RetryAfter != 5*time.Second {
		t.Fatalf("RetryAfter = %v, want the server's 5s", short.RetryAfter)
	}
	// A delay past the cap would stall the turn; the normal backoff applies.
	long := withRetryAfter(&APIError{Type: ErrRateLimit}, 10*time.Minute).(*APIError)
	if long.RetryAfter != 0 {
		t.Fatalf("RetryAfter = %v, want none past the cap", long.RetryAfter)
	}
}

func TestAnthropicRetryWaitsAsLongAsTheServerAsks(t *testing.T) {
	const serverDelay = 1300 * time.Millisecond
	var (
		mu       sync.Mutex
		attempts []time.Time
	)
	t.Setenv("NAMI_ALLOW_CUSTOM_BASE_URL", "1")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		attempts = append(attempts, time.Now())
		first := len(attempts) == 1
		mu.Unlock()
		if first {
			w.Header().Set("Retry-After-Ms", "1300")
			w.WriteHeader(http.StatusTooManyRequests)
			w.Write([]byte(`{"type":"error","error":{"type":"rate_limit_error","message":"slow down"}}`))
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Write([]byte(anthropicUsageStream))
	}))
	defer server.Close()

	client, err := NewAnthropicClientForProvider("anthropic", "claude-sonnet-5", "key", server.URL)
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	stream, err := client.Stream(context.Background(), ModelRequest{Messages: []Message{{Role: RoleUser, Content: "hi"}}})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	for _, err := range stream {
		if err != nil {
			t.Fatalf("stream error: %v", err)
		}
	}

	mu.Lock()
	defer mu.Unlock()
	if len(attempts) != 2 {
		t.Fatalf("attempts = %d, want a retry after the 429", len(attempts))
	}
	// The default backoff would retry after one second.
	if gap := attempts[1].Sub(attempts[0]); gap < serverDelay {
		t.Fatalf("retried after %v, want at least the requested %v", gap, serverDelay)
	}
}
