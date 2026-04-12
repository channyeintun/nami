package cost

import (
	"strings"

	"github.com/channyeintun/gocode/internal/api"
)

type priceTier struct {
	inputPerMTok      float64
	outputPerMTok     float64
	cacheReadPerMTok  float64
	cacheWritePerMTok float64
}

var (
	sonnetTier  = priceTier{inputPerMTok: 3, outputPerMTok: 15, cacheReadPerMTok: 0.3, cacheWritePerMTok: 3.75}
	opusTier    = priceTier{inputPerMTok: 15, outputPerMTok: 75, cacheReadPerMTok: 1.5, cacheWritePerMTok: 18.75}
	opus45Tier  = priceTier{inputPerMTok: 5, outputPerMTok: 25, cacheReadPerMTok: 0.5, cacheWritePerMTok: 6.25}
	haiku35Tier = priceTier{inputPerMTok: 0.8, outputPerMTok: 4, cacheReadPerMTok: 0.08, cacheWritePerMTok: 1}
	haiku45Tier = priceTier{inputPerMTok: 1, outputPerMTok: 5, cacheReadPerMTok: 0.1, cacheWritePerMTok: 1.25}
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

func priceTierForModel(model string) (priceTier, bool) {
	lower := strings.ToLower(strings.TrimSpace(model))
	switch {
	case strings.Contains(lower, "haiku") && strings.Contains(lower, "3.5"):
		return haiku35Tier, true
	case strings.Contains(lower, "haiku"):
		return haiku45Tier, true
	case strings.Contains(lower, "opus") && (strings.Contains(lower, "4.5") || strings.Contains(lower, "4-5") || strings.Contains(lower, "4_5")):
		return opus45Tier, true
	case strings.Contains(lower, "opus"):
		return opusTier, true
	case strings.Contains(lower, "sonnet"):
		return sonnetTier, true
	default:
		return priceTier{}, false
	}
}
