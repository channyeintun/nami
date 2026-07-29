package api

import "testing"

func TestPromptTokenBudgetPrefersReportedPromptLimit(t *testing.T) {
	caps := ModelCapabilities{MaxPromptTokens: 272000, MaxContextWindow: 400000, MaxOutputTokens: 128000}
	if got := caps.PromptTokenBudget(); got != 272000 {
		t.Fatalf("PromptTokenBudget = %d, want the reported prompt limit", got)
	}
}

func TestPromptTokenBudgetReservesOutputRoom(t *testing.T) {
	caps := ModelCapabilities{MaxContextWindow: 200000, MaxOutputTokens: 8192}
	if got := caps.PromptTokenBudget(); got != 200000-8192 {
		t.Fatalf("PromptTokenBudget = %d, want context minus output", got)
	}
}

func TestPromptTokenBudgetCapsTheReservation(t *testing.T) {
	// A model advertising a huge output ceiling must not reserve all of it, or
	// the prompt budget collapses.
	caps := ModelCapabilities{MaxContextWindow: 1000000, MaxOutputTokens: 384000}
	if got := caps.PromptTokenBudget(); got != 1000000-maxPromptBudgetReservedOutputTokens {
		t.Fatalf("PromptTokenBudget = %d, want the reservation clamped", got)
	}
}

func TestPromptTokenBudgetNeverGoesNegative(t *testing.T) {
	for name, caps := range map[string]ModelCapabilities{
		"no context":     {},
		"tiny context":   {MaxContextWindow: 1000, MaxOutputTokens: 64000},
		"negative width": {MaxContextWindow: -1},
	} {
		if got := caps.PromptTokenBudget(); got < 0 {
			t.Errorf("%s: PromptTokenBudget = %d, want >= 0", name, got)
		}
	}
}

func TestResolveModelCapabilitiesPrefersExactCatalogEntry(t *testing.T) {
	want, ok := ModelSpecFor("gpt-5.5")
	if !ok {
		t.Skip("gpt-5.5 not in catalog")
	}
	got := ResolveModelCapabilities("openai", "gpt-5.5")
	if got != want.Capabilities {
		t.Fatalf("ResolveModelCapabilities = %+v, want the catalog entry %+v", got, want.Capabilities)
	}
}

func TestResolveModelCapabilitiesFallsBackForUnknownModel(t *testing.T) {
	// No catalog entry, no family keyword and no known provider: the generic
	// conservative default applies.
	got := ResolveModelCapabilities("not-a-provider", "totally-unknown-xyz")
	want := ModelCapabilities{SupportsToolUse: true, MaxContextWindow: 32000, MaxOutputTokens: 4096}
	if got != want {
		t.Fatalf("ResolveModelCapabilities = %+v, want %+v", got, want)
	}
}

func TestFamilyCapabilitiesIsDeterministic(t *testing.T) {
	// ModelCatalog is a map, so a family lookup that returns the first match it
	// happens across resolves differently between runs. Several families have
	// members with very different context windows, which would hand an
	// unrecognized model a randomly sized budget.
	for _, model := range []string{"gpt-6-preview", "claude-9-sonnet", "gemini-9-pro"} {
		first, ok := familyCapabilities(model)
		if !ok {
			t.Fatalf("familyCapabilities(%q) found no family", model)
		}
		for i := range 200 {
			got, _ := familyCapabilities(model)
			if got != first {
				t.Fatalf("familyCapabilities(%q) is unstable: iteration %d gave %+v, want %+v", model, i, got, first)
			}
		}
	}
}

func TestFamilyCapabilitiesIgnoresUnknownFamilies(t *testing.T) {
	if _, ok := familyCapabilities("totally-unknown-xyz"); ok {
		t.Fatal("expected no family match for an unrecognized model")
	}
}
