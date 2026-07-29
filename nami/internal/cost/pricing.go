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
	// Claude tiers. Sonnet 5 launched on an introductory discount ($2/$10) that
	// ends 2026-08-31; the standard rate is used here so estimates stay correct
	// once it lapses rather than silently under-reporting afterwards.
	sonnetTier     = priceTier{inputPerMTok: 3, outputPerMTok: 15, cacheReadPerMTok: 0.3, cacheWritePerMTok: 3.75}
	legacyOpusTier = priceTier{inputPerMTok: 15, outputPerMTok: 75, cacheReadPerMTok: 1.5, cacheWritePerMTok: 18.75}
	modernOpusTier = priceTier{inputPerMTok: 5, outputPerMTok: 25, cacheReadPerMTok: 0.5, cacheWritePerMTok: 6.25}
	fableTier      = priceTier{inputPerMTok: 10, outputPerMTok: 50, cacheReadPerMTok: 1, cacheWritePerMTok: 12.5}
	haiku35Tier    = priceTier{inputPerMTok: 0.8, outputPerMTok: 4, cacheReadPerMTok: 0.08, cacheWritePerMTok: 1}
	haiku45Tier    = priceTier{inputPerMTok: 1, outputPerMTok: 5, cacheReadPerMTok: 0.1, cacheWritePerMTok: 1.25}

	// GPT tiers. OpenAI charges roughly double once a request passes ~272K of
	// context; these are the standard rates, so a very long GPT context is
	// under-estimated rather than mispriced across the board.
	gptFlagshipTier = priceTier{inputPerMTok: 5, outputPerMTok: 30, cacheReadPerMTok: 0.5, cacheWritePerMTok: 6.25}
	gptBalancedTier = priceTier{inputPerMTok: 2.5, outputPerMTok: 15, cacheReadPerMTok: 0.25, cacheWritePerMTok: 3.125}
	gptLightTier    = priceTier{inputPerMTok: 1, outputPerMTok: 6, cacheReadPerMTok: 0.1, cacheWritePerMTok: 1.25}
	gptProTier      = priceTier{inputPerMTok: 30, outputPerMTok: 180, cacheReadPerMTok: 3, cacheWritePerMTok: 37.5}
	gptMiniTier     = priceTier{inputPerMTok: 0.75, outputPerMTok: 4.5, cacheReadPerMTok: 0.075, cacheWritePerMTok: 0.9375}
	gptNanoTier     = priceTier{inputPerMTok: 0.2, outputPerMTok: 1.25, cacheReadPerMTok: 0.02, cacheWritePerMTok: 0.25}
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
	case strings.Contains(lower, "fable"), strings.Contains(lower, "mythos"):
		return fableTier, true
	case strings.Contains(lower, "haiku") && containsVersion(lower, "3.5"):
		return haiku35Tier, true
	case strings.Contains(lower, "haiku"):
		return haiku45Tier, true
	// Only the pre-4.5 Opus generations were priced at the legacy rate. Matching
	// those explicitly and treating everything newer as the current tier means a
	// future Opus is estimated at the right order of magnitude instead of being
	// billed at 3x by default.
	case strings.Contains(lower, "opus") && isLegacyOpus(lower):
		return legacyOpusTier, true
	case strings.Contains(lower, "opus"):
		return modernOpusTier, true
	case strings.Contains(lower, "sonnet"):
		return sonnetTier, true
	case strings.Contains(lower, "gpt"):
		return gptPriceTier(lower)
	default:
		return priceTier{}, false
	}
}

// isLegacyOpus reports whether an Opus id names one of the generations priced at
// $15/$75: Opus 3, 4.0 and 4.1. The set is closed — those generations are
// retired or deprecated — so they are matched by name rather than by a numeric
// comparison that a date suffix like "20240229" would confuse.
func isLegacyOpus(lower string) bool {
	// Opus 3 spells the generation before the tier name.
	for _, separator := range []string{"-", ".", "_"} {
		if strings.Contains(lower, "3"+separator+"opus") {
			return true
		}
	}
	// Opus 4.0 and 4.1. The original Opus 4 carries a release date instead of a
	// point release ("claude-opus-4-20250514"), so that shape is matched too.
	return containsVersion(lower, "4.0", "4.1") || strings.Contains(lower, "opus-4-2025")
}

// gptPriceTier maps a GPT id onto its rate. Variant suffixes are checked before
// the base version because "gpt-5.4-mini" carries both.
func gptPriceTier(lower string) (priceTier, bool) {
	switch {
	case strings.Contains(lower, "nano"):
		return gptNanoTier, true
	case strings.Contains(lower, "mini"):
		return gptMiniTier, true
	case strings.Contains(lower, "pro"):
		return gptProTier, true
	case strings.Contains(lower, "luna"):
		return gptLightTier, true
	case strings.Contains(lower, "terra"):
		return gptBalancedTier, true
	case containsVersion(lower, "5.6", "5.5"):
		return gptFlagshipTier, true
	case containsVersion(lower, "5.4"):
		return gptBalancedTier, true
	default:
		// Older GPT generations have no rate recorded here; reporting nothing is
		// better than reporting a wrong number.
		return priceTier{}, false
	}
}
