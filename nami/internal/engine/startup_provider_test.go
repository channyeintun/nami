package engine

import (
	"strings"
	"testing"

	commandspkg "github.com/channyeintun/nami/internal/commands"
)

// Ollama counts as usable without being checked, so the startup notice must
// not claim that no provider is usable: /status would list Ollama as one.
func TestNoUsableProviderNoticeNamesTheProviderThatNeedsSetup(t *testing.T) {
	got := formatNoUsableProviderNotice(commandspkg.ProviderStatus{ID: "anthropic", SetupHint: "Set ANTHROPIC_API_KEY."})
	if got != "anthropic is not set up: Set ANTHROPIC_API_KEY. Run /providers for more details." {
		t.Fatalf("notice = %q", got)
	}
	if got := formatNoUsableProviderNotice(commandspkg.ProviderStatus{ID: "anthropic"}); strings.Contains(got, "No usable providers") || !strings.HasPrefix(got, "anthropic is not set up.") {
		t.Fatalf("notice without a hint = %q", got)
	}
}
