package commands

import (
	"strings"
	"testing"
)

func TestParseConnectArgsKeywords(t *testing.T) {
	cases := map[string]ConnectAction{
		"":        ConnectActionOverview,
		"   ":     ConnectActionOverview,
		"help":    ConnectActionHelp,
		" HELP  ": ConnectActionHelp,
		"status":  ConnectActionStatus,
	}
	for args, want := range cases {
		got, err := ParseConnectArgs(args)
		if err != nil {
			t.Errorf("ParseConnectArgs(%q): %v", args, err)
			continue
		}
		if got.Action != want {
			t.Errorf("ParseConnectArgs(%q) action = %q, want %q", args, got.Action, want)
		}
	}
}

func TestParseConnectArgsCodexModes(t *testing.T) {
	for _, mode := range []string{"browser", "headless"} {
		got, err := ParseConnectArgs("codex " + mode)
		if err != nil {
			t.Fatalf("ParseConnectArgs(codex %s): %v", mode, err)
		}
		if got.Action != ConnectActionProvider || got.Provider != "codex" || got.Extra != mode {
			t.Fatalf("request = %+v", got)
		}
	}

	got, err := ParseConnectArgs("github-copilot device")
	if err != nil {
		t.Fatalf("ParseConnectArgs: %v", err)
	}
	if got.Provider != "github-copilot" || got.Extra != "device" {
		t.Fatalf("request = %+v", got)
	}
}

func TestParseConnectArgsRejectsUnusableInput(t *testing.T) {
	for _, args := range []string{
		"not-a-provider",
		"anthropic extra-argument",
		"one two three",
	} {
		if got, err := ParseConnectArgs(args); err == nil {
			t.Errorf("ParseConnectArgs(%q) = %+v, want a usage error", args, got)
		}
	}
}

func TestConnectMethodsForProvider(t *testing.T) {
	copilot := connectMethodsForProvider("github-copilot", "")
	if len(copilot) != 1 || copilot[0].Type != "device" {
		t.Fatalf("copilot methods = %+v, want a device flow", copilot)
	}

	codex := connectMethodsForProvider("codex", "CODEX_TOKEN")
	if len(codex) != 3 {
		t.Fatalf("codex methods = %+v, want browser, headless, and token", codex)
	}
	if codex[2].EnvVar != "CODEX_TOKEN" {
		t.Errorf("codex api key method = %+v", codex[2])
	}

	ollama := connectMethodsForProvider("ollama", "")
	if len(ollama) != 1 || ollama[0].Type != "local" {
		t.Fatalf("ollama methods = %+v, want a local runtime method", ollama)
	}

	generic := connectMethodsForProvider("anthropic", "ANTHROPIC_API_KEY")
	if len(generic) != 1 || generic[0].Type != "api_key" || generic[0].EnvVar != "ANTHROPIC_API_KEY" {
		t.Fatalf("generic methods = %+v", generic)
	}
}

func TestConnectMethodSummary(t *testing.T) {
	if got := connectMethodSummary(nil); got != "setup required" {
		t.Errorf("summary = %q", got)
	}
	got := connectMethodSummary([]ConnectAuthMethod{{Label: "Browser OAuth"}, {Label: " API key "}})
	if got != "browser oauth, api key" {
		t.Errorf("summary = %q", got)
	}
}

func TestFormatConnectMethod(t *testing.T) {
	cases := []struct {
		name     string
		provider string
		method   ConnectAuthMethod
		contains string
	}{
		{"api key", "anthropic", ConnectAuthMethod{Type: "api_key", Label: "API key", EnvVar: "ANTHROPIC_API_KEY"}, "set ANTHROPIC_API_KEY"},
		{"device", "github-copilot", ConnectAuthMethod{Type: "device", Label: "Device login"}, "start browser login"},
		{"oauth browser", "codex", ConnectAuthMethod{Type: "oauth_browser", Label: "Browser OAuth"}, "/connect codex browser"},
		{"oauth headless", "codex", ConnectAuthMethod{Type: "oauth_headless", Label: "Headless OAuth"}, "/connect codex headless"},
		{"local with env", "ollama", ConnectAuthMethod{Type: "local", Label: "Local runtime", EnvVar: "OLLAMA_API_KEY"}, "OLLAMA_API_KEY"},
		{"local without env", "ollama", ConnectAuthMethod{Type: "local", Label: "Local runtime"}, "ensure Ollama is running"},
		{"description fallback", "x", ConnectAuthMethod{Type: "other", Label: "Custom", Description: "do the thing"}, "Custom: do the thing"},
		{"label only", "x", ConnectAuthMethod{Type: "other", Label: "Custom"}, "Custom"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := formatConnectMethod(tc.provider, tc.method)
			if !strings.Contains(got, tc.contains) {
				t.Fatalf("formatConnectMethod = %q, want it to contain %q", got, tc.contains)
			}
		})
	}
}

func TestFormatConnectProviderGuidance(t *testing.T) {
	spec := ConnectProviderSpec{
		ID:           "anthropic",
		Label:        "Anthropic",
		DefaultModel: "claude-opus-5",
		Methods:      []ConnectAuthMethod{{Type: "api_key", Label: "API key", EnvVar: "ANTHROPIC_API_KEY"}},
	}

	needsSetup := FormatConnectProviderGuidance(spec, ProviderSnapshot{
		Providers: []ProviderStatus{{
			ID:        "anthropic",
			SetupHint: "export ANTHROPIC_API_KEY",
			LastError: "no credentials found",
		}},
	})
	for _, want := range []string{
		"Anthropic setup",
		"Default model: anthropic/claude-opus-5",
		"Current issue: no credentials found",
		"Auth methods:",
		"Next: export ANTHROPIC_API_KEY",
	} {
		if !strings.Contains(needsSetup, want) {
			t.Errorf("guidance missing %q:\n%s", want, needsSetup)
		}
	}

	ready := FormatConnectProviderGuidance(spec, ProviderSnapshot{
		Providers: []ProviderStatus{{ID: "anthropic", Usable: true}},
	})
	if !strings.Contains(ready, "Provider is ready") {
		t.Errorf("guidance for a ready provider:\n%s", ready)
	}
	if strings.Contains(ready, "Current issue") {
		t.Errorf("ready provider should not report an issue:\n%s", ready)
	}
}

func TestEnsureConnectProviderSpecIsIdempotent(t *testing.T) {
	existing := []ConnectProviderSpec{{ID: "codex", Label: "Codex"}}
	if got := ensureConnectProviderSpec(existing, "codex"); len(got) != 1 {
		t.Fatalf("ensureConnectProviderSpec added a duplicate: %+v", got)
	}
	if got := ensureConnectProviderSpec(existing, "not-a-real-provider"); len(got) != 1 {
		t.Fatalf("ensureConnectProviderSpec added an unknown provider: %+v", got)
	}
}
