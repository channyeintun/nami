package agent

const (
	defaultReservedOutputTokens = 8_000
	escalatedOutputTokens       = 64_000
)

func defaultOutputBudget(providerMax int) int {
	if providerMax <= 0 {
		return 0
	}
	return min(providerMax, defaultReservedOutputTokens)
}

func escalatedOutputBudget(providerMax int) int {
	if providerMax <= 0 {
		return 0
	}
	return min(providerMax, escalatedOutputTokens)
}

func nextOutputBudget(current, providerMax int, pressure ContextPressureDecision) int {
	if providerMax <= 0 {
		return current
	}
	if pressure.DelayOutputEscalation {
		return current
	}
	next := escalatedOutputBudget(providerMax)
	if next <= current {
		return current
	}
	return next
}
