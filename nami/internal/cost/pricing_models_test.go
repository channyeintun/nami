package cost

import "testing"

// Each entry is the published per-MTok rate for the model, so a tier that
// silently stops matching shows up as a concrete price change rather than as a
// quietly wrong total.
func TestPriceTierForCurrentModels(t *testing.T) {
	cases := map[string]priceTier{
		// Claude 5 family.
		"claude-fable-5":  fableTier,
		"claude-mythos-5": fableTier,
		"claude-opus-5":   modernOpusTier,
		"claude-sonnet-5": sonnetTier,
		// Claude 4 family.
		"claude-opus-4-8":           modernOpusTier,
		"claude-opus-4-7":           modernOpusTier,
		"claude-opus-4-6":           modernOpusTier,
		"claude-opus-4-5":           modernOpusTier,
		"claude-sonnet-4-6":         sonnetTier,
		"claude-haiku-4-5":          haiku45Tier,
		"claude-haiku-4-5-20251001": haiku45Tier,
		// GPT 5.6 family.
		"gpt-5.6":       gptFlagshipTier,
		"gpt-5.6-sol":   gptFlagshipTier,
		"gpt-5.6-terra": gptBalancedTier,
		"gpt-5.6-luna":  gptLightTier,
		// Earlier GPT 5 releases.
		"gpt-5.5":      gptFlagshipTier,
		"gpt-5.5-pro":  gptProTier,
		"gpt-5.4":      gptBalancedTier,
		"gpt-5.4-pro":  gptProTier,
		"gpt-5.4-mini": gptMiniTier,
		"gpt-5.4-nano": gptNanoTier,
	}
	for model, want := range cases {
		got, ok := priceTierForModel(model)
		if !ok {
			t.Errorf("priceTierForModel(%q) found no tier", model)
			continue
		}
		if got != want {
			t.Errorf("priceTierForModel(%q) = %+v, want %+v", model, got, want)
		}
	}
}

// The Opus rate dropped from $15/$75 to $5/$25 at 4.5. Only the older
// generations stay on the legacy tier, and a release date in the id must not
// push a model onto the wrong side of that line.
func TestPriceTierSeparatesOpusGenerations(t *testing.T) {
	legacy := []string{
		"claude-3-opus-20240229",
		"claude-3-opus-latest",
		"claude-opus-4-0",
		"claude-opus-4.0",
		"claude-opus-4-20250514",
		"claude-opus-4-1",
		"claude-opus-4-1-20250805",
	}
	for _, model := range legacy {
		if tier, ok := priceTierForModel(model); !ok || tier != legacyOpusTier {
			t.Errorf("priceTierForModel(%q) = %+v ok=%v, want the legacy opus tier", model, tier, ok)
		}
	}

	modern := []string{"claude-opus-4-5", "claude-opus-4-8", "claude-opus-5", "claude-opus-6-preview"}
	for _, model := range modern {
		if tier, ok := priceTierForModel(model); !ok || tier != modernOpusTier {
			t.Errorf("priceTierForModel(%q) = %+v ok=%v, want the modern opus tier", model, tier, ok)
		}
	}
}

// Fable is the most expensive Claude tier; matching it before the generic
// family checks is what keeps it off the Opus rate.
func TestFableIsPricedAboveOpus(t *testing.T) {
	if fableTier.inputPerMTok <= modernOpusTier.inputPerMTok {
		t.Fatalf("fable input rate %v should exceed the opus rate %v", fableTier.inputPerMTok, modernOpusTier.inputPerMTok)
	}
	if tier, _ := priceTierForModel("claude-fable-5"); tier != fableTier {
		t.Fatalf("claude-fable-5 = %+v, want the fable tier", tier)
	}
}

// A GPT variant suffix has to win over the base version, or "gpt-5.4-mini"
// bills at six times its real rate.
func TestGPTVariantSuffixWinsOverBaseVersion(t *testing.T) {
	if tier, _ := priceTierForModel("gpt-5.4-mini"); tier != gptMiniTier {
		t.Fatalf("gpt-5.4-mini = %+v, want the mini tier", tier)
	}
	if tier, _ := priceTierForModel("gpt-5.4-nano"); tier != gptNanoTier {
		t.Fatalf("gpt-5.4-nano = %+v, want the nano tier", tier)
	}
	if tier, _ := priceTierForModel("gpt-5.6-luna"); tier != gptLightTier {
		t.Fatalf("gpt-5.6-luna = %+v, want the light tier", tier)
	}
}

func TestGPTTiersAreOrdered(t *testing.T) {
	ordered := []priceTier{gptNanoTier, gptMiniTier, gptLightTier, gptBalancedTier, gptFlagshipTier, gptProTier}
	for i := 1; i < len(ordered); i++ {
		if ordered[i].inputPerMTok < ordered[i-1].inputPerMTok {
			t.Fatalf("tier %d input rate %v is below tier %d rate %v", i, ordered[i].inputPerMTok, i-1, ordered[i-1].inputPerMTok)
		}
	}
}

// Provider-prefixed ids reach the cost tracker as-is from the router.
func TestPriceTierAcceptsProviderPrefixedIDs(t *testing.T) {
	if tier, ok := priceTierForModel("anthropic/claude-opus-5"); !ok || tier != modernOpusTier {
		t.Errorf("prefixed claude id = %+v ok=%v", tier, ok)
	}
	if tier, ok := priceTierForModel("github-copilot/gpt-5.6"); !ok || tier != gptFlagshipTier {
		t.Errorf("prefixed gpt id = %+v ok=%v", tier, ok)
	}
}
