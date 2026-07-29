package cost

import (
	"math"
	"testing"

	"github.com/channyeintun/nami/internal/api"
)

func assertUSD(t *testing.T, got, want float64) {
	t.Helper()
	if math.Abs(got-want) > 1e-9 {
		t.Fatalf("cost = %v, want %v", got, want)
	}
}

func TestCalculateUSDCostSumsEveryTokenClass(t *testing.T) {
	usage := api.Usage{
		InputTokens:         1_000_000,
		OutputTokens:        1_000_000,
		CacheReadTokens:     1_000_000,
		CacheCreationTokens: 1_000_000,
	}
	// One million of each class bills exactly the per-MTok rate.
	assertUSD(t, CalculateUSDCost("claude-sonnet-4-20250514", usage), 3+15+0.3+3.75)
}

func TestCalculateUSDCostScalesLinearly(t *testing.T) {
	single := CalculateUSDCost("claude-sonnet-4", api.Usage{InputTokens: 1_000})
	double := CalculateUSDCost("claude-sonnet-4", api.Usage{InputTokens: 2_000})
	assertUSD(t, double, single*2)
}

func TestCalculateUSDCostIsZeroForUnknownModels(t *testing.T) {
	usage := api.Usage{InputTokens: 1_000_000, OutputTokens: 1_000_000}
	for _, model := range []string{"", "gpt-5.5", "llama-3", "unknown"} {
		if got := CalculateUSDCost(model, usage); got != 0 {
			t.Errorf("CalculateUSDCost(%q) = %v, want 0 for an unpriced model", model, got)
		}
	}
}

func TestCalculateUSDCostIsZeroForEmptyUsage(t *testing.T) {
	if got := CalculateUSDCost("claude-sonnet-4", api.Usage{}); got != 0 {
		t.Fatalf("CalculateUSDCost(empty usage) = %v, want 0", got)
	}
}

// Real Anthropic model IDs separate version parts with hyphens
// ("claude-3-5-haiku-20241022"), so the tier lookup has to accept the same
// separator variants the opus branch already handles.
func TestPriceTierForModelMatchesHaikuSeparatorVariants(t *testing.T) {
	for _, model := range []string{
		"claude-3-5-haiku-20241022",
		"claude-3-5-haiku-latest",
		"claude-3.5-haiku",
		"claude-3_5-haiku",
	} {
		tier, ok := priceTierForModel(model)
		if !ok {
			t.Fatalf("priceTierForModel(%q) found no tier", model)
		}
		if tier != haiku35Tier {
			t.Errorf("priceTierForModel(%q) = %+v, want the haiku 3.5 tier %+v", model, tier, haiku35Tier)
		}
	}
}

func TestPriceTierForModelMatchesOpusSeparatorVariants(t *testing.T) {
	for _, model := range []string{"claude-opus-4-5", "claude-opus-4.5", "claude-opus-4_5", "claude-opus-4-7"} {
		tier, ok := priceTierForModel(model)
		if !ok || tier != modernOpusTier {
			t.Errorf("priceTierForModel(%q) = %+v ok=%v, want the modern opus tier", model, tier, ok)
		}
	}
	// Older opus generations stay on the legacy tier.
	if tier, ok := priceTierForModel("claude-3-opus-20240229"); !ok || tier != legacyOpusTier {
		t.Errorf("legacy opus = %+v ok=%v", tier, ok)
	}
}

func TestPriceTierForModelDefaultsNewerHaikuToCurrentTier(t *testing.T) {
	if tier, ok := priceTierForModel("claude-haiku-4-5-20251001"); !ok || tier != haiku45Tier {
		t.Fatalf("haiku 4.5 = %+v ok=%v", tier, ok)
	}
}

func TestPriceTierForModelIgnoresCaseAndPadding(t *testing.T) {
	tier, ok := priceTierForModel("  CLAUDE-SONNET-4  ")
	if !ok || tier != sonnetTier {
		t.Fatalf("priceTierForModel = %+v ok=%v, want the sonnet tier", tier, ok)
	}
}
