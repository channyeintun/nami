package api

import "strings"

const (
	ReasoningEffortLow    = "low"
	ReasoningEffortMedium = "medium"
	ReasoningEffortHigh   = "high"
	ReasoningEffortXHigh  = "xhigh"
)

func NormalizeReasoningEffort(input string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(input)) {
	case ReasoningEffortLow:
		return ReasoningEffortLow, true
	case ReasoningEffortMedium:
		return ReasoningEffortMedium, true
	case ReasoningEffortHigh:
		return ReasoningEffortHigh, true
	case ReasoningEffortXHigh:
		return ReasoningEffortXHigh, true
	default:
		return "", false
	}
}

func SupportsOpenAIReasoningEffort(model string) bool {
	lower := normalizeReasoningModelID(model)
	prefixes := []string{"gpt-5", "o1", "o3", "o4"}
	for _, p := range prefixes {
		if strings.HasPrefix(lower, p) {
			return true
		}
	}
	return false
}

func SupportsXHighReasoningEffort(model string) bool {
	lower := normalizeReasoningModelID(model)
	versions := []string{"gpt-5.2", "gpt-5.3", "gpt-5.4", "gpt-5.5", "gpt-5.6"}
	for _, v := range versions {
		if strings.Contains(lower, v) {
			return true
		}
	}
	return false
}

func ClampReasoningEffort(model, effort string) string {
	normalized, ok := NormalizeReasoningEffort(effort)
	if !ok || !SupportsOpenAIReasoningEffort(model) {
		return ""
	}
	if normalized == ReasoningEffortXHigh && !SupportsXHighReasoningEffort(model) {
		return ReasoningEffortHigh
	}
	return normalized
}

func DefaultReasoningEffort(model string) string {
	if SupportsOpenAIReasoningEffort(model) {
		return ReasoningEffortMedium
	}
	return ""
}

func MaxReasoningEffort(model string, efforts ...string) string {
	best := ""
	bestRank := -1
	for _, effort := range efforts {
		clamped := ClampReasoningEffort(model, effort)
		if clamped == "" {
			continue
		}
		rank := reasoningEffortRank(clamped)
		if rank > bestRank {
			best = clamped
			bestRank = rank
		}
	}
	return best
}

func reasoningEffortRank(effort string) int {
	switch effort {
	case ReasoningEffortLow:
		return 1
	case ReasoningEffortMedium:
		return 2
	case ReasoningEffortHigh:
		return 3
	case ReasoningEffortXHigh:
		return 4
	default:
		return 0
	}
}

func normalizeReasoningModelID(model string) string {
	trimmed := strings.ToLower(strings.TrimSpace(model))
	if trimmed == "" {
		return ""
	}
	if slash := strings.LastIndex(trimmed, "/"); slash >= 0 && slash < len(trimmed)-1 {
		return trimmed[slash+1:]
	}
	return trimmed
}
