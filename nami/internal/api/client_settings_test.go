package api

import (
	"context"
	"sync"
	"testing"
)

// The engine starts a warmup request in the background and then, on the main
// goroutine, installs a token refresher and the enterprise domain on the same
// client, so the setters have to be safe against a request reading them.
func TestClientSettingsCanChangeWhileARequestReadsThem(t *testing.T) {
	t.Setenv("NAMI_ALLOW_CUSTOM_BASE_URL", "1")
	anthropic, err := NewAnthropicClientForProvider("github-copilot", "claude-sonnet-5", "key", "")
	if err != nil {
		t.Fatalf("anthropic client: %v", err)
	}
	compat, err := NewOpenAICompatClient("github-copilot", "gemini-2.5-pro", "key", "")
	if err != nil {
		t.Fatalf("compatible client: %v", err)
	}
	responses, err := NewOpenAIResponsesClient("codex", "gpt-5.5", "key", "")
	if err != nil {
		t.Fatalf("responses client: %v", err)
	}

	// A warmup reads the key, domain and account before it sends anything, so
	// a cancelled context exercises those reads without touching the network.
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()

	var wg sync.WaitGroup
	for _, client := range []LLMClient{anthropic, compat, responses} {
		wg.Go(func() {
			for range 20 {
				_ = client.(WarmupCapable).Warmup(cancelled)
			}
		})
		wg.Go(func() {
			for range 20 {
				SetAPIKeyFunc(client, func() (string, error) { return "key", nil })
				SetGitHubCopilotEnterpriseDomain(client, "example.com")
				SetCodexAccountID(client, "account")
				SetCodexAccountIDFunc(client, func() string { return "account" })
			}
		})
	}
	wg.Wait()
}
