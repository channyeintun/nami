package cost

import (
	"strings"

	"github.com/channyeintun/nami/internal/api"
)

type priceTier struct {
	inputPerMTok      float64
	outputPerMTok     float64
	cacheReadPerMTok  float64
	cacheWritePerMTok float64
}

var (
	sonnetTier     = priceTier{inputPerMTok: 3, outputPerMTok: 15, cacheReadPerMTok: 0.3, cacheWritePerMTok: 3.75}
	legacyOpusTier = priceTier{inputPerMTok: 15, outputPerMTok: 75, cacheReadPerMTok: 1.5, cacheWritePerMTok: 18.75}
	modernOpusTier = priceTier{inputPerMTok: 5, outputPerMTok: 25, cacheReadPerMTok: 0.5, cacheWritePerMTok: 6.25}
	haiku35Tier    = priceTier{inputPerMTok: 0.8, outputPerMTok: 4, cacheReadPerMTok: 0.08, cacheWritePerMTok: 1}
	haiku45Tier    = priceTier{inputPerMTok: 1, outputPerMTok: 5, cacheReadPerMTok: 0.1, cacheWritePerMTok: 1.25}
)

// CalculateUSDCost estimates the USD cost for a model call from token usage.
// Pricing currently follows the Claude-family pricing model from the source implementation.
func CalculateUSDCost(model string, usage api.Usage) float64 {
	tier, ok := priceTierForModel(model)
	if !ok {
		return 0
	}

	return (float64(usage.InputTokens)/1_000_000)*tier.inputPerMTok +
		(float64(usage.OutputTokens)/1_000_000)*tier.outputPerMTok +
		(float64(usage.CacheReadTokens)/1_000_000)*tier.cacheReadPerMTok +
		(float64(usage.CacheCreationTokens)/1_000_000)*tier.cacheWritePerMTok
}

// containsVersion reports whether a model id mentions any of the given
// versions. Model ids spell the separator inconsistently — the canonical
// Anthropic ids use "-" ("claude-3-5-haiku-20241022") while documentation uses
// "." — so every variant has to be accepted or the tier silently falls through
// to the wrong price.
func containsVersion(model string, versions ...string) bool {
	for _, version := range versions {
		for _, separator := range []string{".", "-", "_"} {
			if strings.Contains(model, strings.ReplaceAll(version, ".", separator)) {
				return true
			}
		}
	}
	return false
}

func priceTierForModel(model string) (priceTier, bool) {
	lower := strings.ToLower(strings.TrimSpace(model))
	switch {
	case strings.Contains(lower, "haiku") && containsVersion(lower, "3.5"):
		return haiku35Tier, true
	case strings.Contains(lower, "haiku"):
		return haiku45Tier, true
	case strings.Contains(lower, "opus") && containsVersion(lower, "4.5", "4.6", "4.7"):
		return modernOpusTier, true
	case strings.Contains(lower, "opus"):
		return legacyOpusTier, true
	case strings.Contains(lower, "sonnet"):
		return sonnetTier, true
	default:
		return priceTier{}, false
	}
}
