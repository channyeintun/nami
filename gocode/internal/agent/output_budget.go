package agent

const (
	defaultReservedOutputTokens = 8_000
	escalatedOutputTokens       = 64_000
)

func defaultOutputBudget(providerMax int) int {
	if providerMax <= 0 {
		return 0
	}
	return minInt(providerMax, defaultReservedOutputTokens)
}

func escalatedOutputBudget(providerMax int) int {
	if providerMax <= 0 {
		return 0
	}
	return minInt(providerMax, escalatedOutputTokens)
}

func nextOutputBudget(current, providerMax int) int {
	if providerMax <= 0 {
		return current
	}
	next := escalatedOutputBudget(providerMax)
	if next <= current {
		return current
	}
	return next
}
