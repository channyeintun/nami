package clientdebug

import (
	"context"
	"errors"
	"iter"
	"testing"

	"github.com/channyeintun/nami/internal/api"
)

// fakeClient records what the proxy forwards and replays a fixed event stream.
type fakeClient struct {
	modelID       string
	events        []api.ModelEvent
	streamErr     error
	streamCalls   int
	warmupCalls   int
	warmupErr     error
	apiKeyFuncSet bool
	copilotDomain string
	codexAccount  string
	codexFuncSet  bool
}

func (c *fakeClient) Stream(ctx context.Context, req api.ModelRequest) (iter.Seq2[api.ModelEvent, error], error) {
	c.streamCalls++
	if c.streamErr != nil {
		return nil, c.streamErr
	}
	return func(yield func(api.ModelEvent, error) bool) {
		for _, event := range c.events {
			if !yield(event, nil) {
				return
			}
		}
	}, nil
}

func (c *fakeClient) ModelID() string { return c.modelID }
func (c *fakeClient) Capabilities() api.ModelCapabilities {
	return api.ModelCapabilities{MaxOutputTokens: 4096}
}
func (c *fakeClient) SetAPIKeyFunc(func() (string, error)) {
	c.apiKeyFuncSet = true
}
func (c *fakeClient) SetGitHubCopilotEnterpriseDomain(domain string) { c.copilotDomain = domain }
func (c *fakeClient) SetCodexAccountID(accountID string)             { c.codexAccount = accountID }
func (c *fakeClient) SetCodexAccountIDFunc(func() string)            { c.codexFuncSet = true }
func (c *fakeClient) Warmup(context.Context) error {
	c.warmupCalls++
	return c.warmupErr
}

// minimalClient implements only the required interface, so the proxy has to
// tolerate the optional setters being absent.
type minimalClient struct{}

func (minimalClient) Stream(context.Context, api.ModelRequest) (iter.Seq2[api.ModelEvent, error], error) {
	return func(yield func(api.ModelEvent, error) bool) {}, nil
}
func (minimalClient) ModelID() string                     { return "minimal" }
func (minimalClient) Capabilities() api.ModelCapabilities { return api.ModelCapabilities{} }

func TestWrapClientHandlesNilAndDoubleWrapping(t *testing.T) {
	if got := WrapClient(nil); got != nil {
		t.Fatalf("WrapClient(nil) = %#v, want nil", got)
	}

	inner := &fakeClient{modelID: "test-model"}
	wrapped := WrapClient(inner)
	if wrapped == nil {
		t.Fatal("WrapClient returned nil for a real client")
	}
	if again := WrapClient(wrapped); again != wrapped {
		t.Fatal("WrapClient wrapped an already-wrapped client")
	}
}

func TestProxyForwardsMetadata(t *testing.T) {
	inner := &fakeClient{modelID: "test-model"}
	wrapped := WrapClient(inner)

	if wrapped.ModelID() != "test-model" {
		t.Errorf("ModelID = %q", wrapped.ModelID())
	}
	if wrapped.Capabilities().MaxOutputTokens != 4096 {
		t.Errorf("Capabilities = %+v", wrapped.Capabilities())
	}
}

func TestProxyStreamsEveryEvent(t *testing.T) {
	inner := &fakeClient{
		modelID: "test-model",
		events: []api.ModelEvent{
			{Type: api.ModelEventToken, Text: "hello"},
			{Type: api.ModelEventThinking, Text: "thinking"},
			{Type: api.ModelEventToolCall, ToolCall: &api.ToolCall{ID: "t1", Name: "bash", Input: `{}`}},
			{Type: api.ModelEventUsage, Usage: &api.Usage{InputTokens: 10, OutputTokens: 5}},
			{Type: api.ModelEventStop, StopReason: "end_turn"},
		},
	}
	wrapped := WrapClient(inner)

	stream, err := wrapped.Stream(context.Background(), api.ModelRequest{SystemPrompt: "sys"})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}

	seen := 0
	for event, eventErr := range stream {
		if eventErr != nil {
			t.Fatalf("event %d: %v", seen, eventErr)
		}
		if event.Type != inner.events[seen].Type {
			t.Fatalf("event %d type = %v, want %v", seen, event.Type, inner.events[seen].Type)
		}
		seen++
	}
	if seen != len(inner.events) {
		t.Fatalf("saw %d events, want %d", seen, len(inner.events))
	}
	if inner.streamCalls != 1 {
		t.Fatalf("inner Stream called %d times", inner.streamCalls)
	}
}

func TestProxyStopsWhenConsumerBreaks(t *testing.T) {
	inner := &fakeClient{
		events: []api.ModelEvent{
			{Type: api.ModelEventToken, Text: "one"},
			{Type: api.ModelEventToken, Text: "two"},
			{Type: api.ModelEventToken, Text: "three"},
		},
	}
	stream, err := WrapClient(inner).Stream(context.Background(), api.ModelRequest{})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}

	seen := 0
	for range stream {
		seen++
		if seen == 2 {
			break
		}
	}
	if seen != 2 {
		t.Fatalf("consumer saw %d events after breaking at 2", seen)
	}
}

func TestProxyPropagatesStreamError(t *testing.T) {
	inner := &fakeClient{streamErr: errors.New("upstream refused")}
	if _, err := WrapClient(inner).Stream(context.Background(), api.ModelRequest{}); err == nil {
		t.Fatal("Stream swallowed the inner error")
	}
}

func TestProxyForwardsOptionalSetters(t *testing.T) {
	inner := &fakeClient{}
	wrapped := WrapClient(inner)

	if setter, ok := wrapped.(api.APIKeyFuncSetter); ok {
		setter.SetAPIKeyFunc(func() (string, error) { return "key", nil })
	}
	if setter, ok := wrapped.(api.GitHubCopilotEnterpriseDomainSetter); ok {
		setter.SetGitHubCopilotEnterpriseDomain("corp.example")
	}
	if setter, ok := wrapped.(api.CodexAccountIDSetter); ok {
		setter.SetCodexAccountID("acct-1")
	}
	if setter, ok := wrapped.(api.CodexAccountIDFuncSetter); ok {
		setter.SetCodexAccountIDFunc(func() string { return "acct-2" })
	}

	if !inner.apiKeyFuncSet {
		t.Error("SetAPIKeyFunc was not forwarded")
	}
	if inner.copilotDomain != "corp.example" {
		t.Errorf("copilot domain = %q", inner.copilotDomain)
	}
	if inner.codexAccount != "acct-1" {
		t.Errorf("codex account = %q", inner.codexAccount)
	}
	if !inner.codexFuncSet {
		t.Error("SetCodexAccountIDFunc was not forwarded")
	}
}

func TestProxySettersTolerateMinimalClients(t *testing.T) {
	wrapped := WrapClient(minimalClient{})

	// None of these may panic when the inner client lacks the interface.
	if setter, ok := wrapped.(api.APIKeyFuncSetter); ok {
		setter.SetAPIKeyFunc(func() (string, error) { return "", nil })
	}
	if setter, ok := wrapped.(api.GitHubCopilotEnterpriseDomainSetter); ok {
		setter.SetGitHubCopilotEnterpriseDomain("x")
	}
	if setter, ok := wrapped.(api.CodexAccountIDSetter); ok {
		setter.SetCodexAccountID("x")
	}
	if warmable, ok := wrapped.(api.WarmupCapable); ok {
		if err := warmable.Warmup(context.Background()); err != nil {
			t.Fatalf("Warmup on a client without warmup support: %v", err)
		}
	}
}

func TestProxyForwardsWarmup(t *testing.T) {
	inner := &fakeClient{}
	warmable, ok := WrapClient(inner).(api.WarmupCapable)
	if !ok {
		t.Fatal("wrapped client does not expose Warmup")
	}
	if err := warmable.Warmup(context.Background()); err != nil {
		t.Fatalf("Warmup: %v", err)
	}
	if inner.warmupCalls != 1 {
		t.Fatalf("inner Warmup called %d times", inner.warmupCalls)
	}

	failing := &fakeClient{warmupErr: errors.New("warmup failed")}
	warmable, _ = WrapClient(failing).(api.WarmupCapable)
	if err := warmable.Warmup(context.Background()); err == nil {
		t.Fatal("Warmup swallowed the inner error")
	}
}

func TestEventTypeName(t *testing.T) {
	cases := map[api.ModelEventType]string{
		api.ModelEventToken:      "token",
		api.ModelEventThinking:   "thinking",
		api.ModelEventToolCall:   "tool_call",
		api.ModelEventStop:       "stop",
		api.ModelEventUsage:      "usage",
		api.ModelEventRateLimits: "rate_limits",
	}
	for eventType, want := range cases {
		if got := eventTypeName(eventType); got != want {
			t.Errorf("eventTypeName(%v) = %q, want %q", eventType, got, want)
		}
	}
	if got := eventTypeName(api.ModelEventType(99)); got != "unknown(99)" {
		t.Errorf("eventTypeName(99) = %q", got)
	}
}
