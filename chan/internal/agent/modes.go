package agent

// ExecutionMode controls agent behavior profile.
type ExecutionMode string

const (
	ModePlan ExecutionMode = "plan"
	ModeFast ExecutionMode = "fast"
)

// ExecutionProfile defines behavior for a given mode.
type ExecutionProfile struct {
	RequirePlanBeforeWrite bool
	PreferFastModel        bool
	MaxParallelReadTools   int
	ToolSummaryVerbosity   string // "full", "terse"
	ShowPlanPanel          bool
}

// ProfileForMode returns the execution profile for the given mode.
func ProfileForMode(mode ExecutionMode) ExecutionProfile {
	switch mode {
	case ModePlan:
		return ExecutionProfile{
			RequirePlanBeforeWrite: false,
			PreferFastModel:        false,
			MaxParallelReadTools:   5,
			ToolSummaryVerbosity:   "full",
			ShowPlanPanel:          true,
		}
	case ModeFast:
		return ExecutionProfile{
			RequirePlanBeforeWrite: false,
			PreferFastModel:        true,
			MaxParallelReadTools:   10,
			ToolSummaryVerbosity:   "terse",
			ShowPlanPanel:          false,
		}
	default:
		return ProfileForMode(ModePlan)
	}
}
