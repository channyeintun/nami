package tools

import (
	"fmt"
	"os"
	"path/filepath"
)

// ResultBudget defines limits for tool output size.
type ResultBudget struct {
	MaxChars     int
	PreviewLen   int
	SpillDir     string
}

// DefaultResultBudget returns the standard budget.
func DefaultResultBudget(sessionDir string) ResultBudget {
	return ResultBudget{
		MaxChars:   MaxResultSizeChars,
		PreviewLen: PreviewChars,
		SpillDir:   filepath.Join(sessionDir, "artifacts", "tool-log"),
	}
}

// ApplyBudget truncates output if it exceeds the budget, spilling to disk.
// Returns the (possibly truncated) output and the spill path if any.
func ApplyBudget(budget ResultBudget, toolID string, output string) (string, string, error) {
	if len(output) <= budget.MaxChars {
		return output, "", nil
	}

	if err := os.MkdirAll(budget.SpillDir, 0o755); err != nil {
		return output[:budget.MaxChars], "", fmt.Errorf("create spill dir: %w", err)
	}

	spillPath := filepath.Join(budget.SpillDir, toolID+".log")
	if err := os.WriteFile(spillPath, []byte(output), 0o644); err != nil {
		return output[:budget.MaxChars], "", fmt.Errorf("write spill file: %w", err)
	}

	preview := output[:budget.PreviewLen]
	truncated := fmt.Sprintf(
		"%s\n\n[Output truncated. Full result saved to %s (%d chars)]",
		preview, spillPath, len(output),
	)
	return truncated, spillPath, nil
}
