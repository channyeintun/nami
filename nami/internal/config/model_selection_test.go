package config

import (
	"strings"
	"testing"
)

func TestParseModelSplitsOnFirstSlashOnly(t *testing.T) {
	cases := []struct {
		input           string
		provider, model string
	}{
		{"anthropic/claude-opus-5", "anthropic", "claude-opus-5"},
		{"claude-opus-5", "", "claude-opus-5"},
		// Model ids may themselves contain a slash, so only the first splits.
		{"openrouter/anthropic/claude-opus-5", "openrouter", "anthropic/claude-opus-5"},
		{"", "", ""},
	}
	for _, tc := range cases {
		provider, model := ParseModel(tc.input)
		if provider != tc.provider || model != tc.model {
			t.Errorf("ParseModel(%q) = (%q, %q), want (%q, %q)", tc.input, provider, model, tc.provider, tc.model)
		}
	}
}

func TestParseModelSelectionMarksExplicitProvider(t *testing.T) {
	selection := ParseModelSelection("anthropic/claude-opus-5", "flag")
	if selection.ProviderID != "anthropic" || selection.ModelID != "claude-opus-5" {
		t.Fatalf("selection = %+v", selection)
	}
	if !selection.ExplicitProvider {
		t.Fatal("a provider-qualified ref should be explicit")
	}
	if selection.Source != "flag" {
		t.Fatalf("source = %q", selection.Source)
	}
}

func TestParseModelSelectionBareModelIsNotExplicit(t *testing.T) {
	selection := ParseModelSelection("claude-opus-5", "config")
	if selection.ProviderID != "" || selection.ModelID != "claude-opus-5" {
		t.Fatalf("selection = %+v", selection)
	}
	if selection.ExplicitProvider {
		t.Fatal("a bare model must not claim an explicit provider")
	}
}

func TestParseModelSelectionTreatsTrailingSlashAsBareModel(t *testing.T) {
	// "anthropic/" names no model, so the token is taken as the model itself
	// rather than leaving an empty model id behind.
	selection := ParseModelSelection("anthropic/", "flag")
	if selection.ModelID != "anthropic" || selection.ProviderID != "" {
		t.Fatalf("selection = %+v", selection)
	}
	if selection.ExplicitProvider {
		t.Fatal("a trailing slash must not count as an explicit provider")
	}
}

func TestParseModelSelectionTrimsWhitespace(t *testing.T) {
	selection := ParseModelSelection("  anthropic/claude-opus-5  ", "  flag  ")
	if selection.ProviderID != "anthropic" || selection.ModelID != "claude-opus-5" {
		t.Fatalf("selection = %+v", selection)
	}
	if selection.Source != "flag" {
		t.Fatalf("source = %q, want trimmed", selection.Source)
	}
}

func TestNewModelSelectionClearsExplicitFlagWithoutProvider(t *testing.T) {
	selection := NewModelSelection("   ", "claude-opus-5", "test", true)
	if selection.ExplicitProvider {
		t.Fatal("explicit provider claimed with no provider id")
	}
}

func TestModelSelectionRefRoundTrips(t *testing.T) {
	for _, input := range []string{"anthropic/claude-opus-5", "claude-opus-5"} {
		if got := ParseModelSelection(input, "test").Ref(); got != input {
			t.Errorf("Ref() = %q, want %q", got, input)
		}
	}
}

func TestModelSelectionRefOmitsMissingHalves(t *testing.T) {
	if got := (ModelSelection{ProviderID: "anthropic"}).Ref(); got != "anthropic" {
		t.Errorf("Ref() = %q, want the provider alone", got)
	}
	if got := (ModelSelection{ModelID: "claude-opus-5"}).Ref(); got != "claude-opus-5" {
		t.Errorf("Ref() = %q, want the model alone", got)
	}
	if got := (ModelSelection{}).Ref(); got != "" {
		t.Errorf("Ref() = %q, want empty", got)
	}
}

func TestParseMCPScopeAcceptsKnownScopes(t *testing.T) {
	for _, input := range []string{"project", "PROJECT", "  project  "} {
		scope, err := ParseMCPScope(input)
		if err != nil || scope != MCPScopeProject {
			t.Errorf("ParseMCPScope(%q) = (%v, %v)", input, scope, err)
		}
	}
	if scope, err := ParseMCPScope("user"); err != nil || scope != MCPScopeUser {
		t.Errorf("ParseMCPScope(user) = (%v, %v)", scope, err)
	}
}

func TestParseMCPScopeRejectsUnknownScope(t *testing.T) {
	scope, err := ParseMCPScope("global")
	if err == nil {
		t.Fatal("expected an error for an unknown scope")
	}
	if scope != "" {
		t.Fatalf("scope = %q, want empty on error", scope)
	}
	// The message should tell the user what is valid.
	if got := err.Error(); !strings.Contains(got, "project") || !strings.Contains(got, "user") {
		t.Fatalf("error = %q, want the valid scopes listed", got)
	}
}
