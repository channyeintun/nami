package engine

import (
	"fmt"
	"strings"

	"github.com/channyeintun/nami/internal/ipc"
	workflowpkg "github.com/channyeintun/nami/internal/workflow"
)

// handleWorkflowsSlashCommand opens the workflows dialog. The TUI already
// holds the runs from workflow_updated events, so the engine sends only the
// saved workflows, which live on disk.
func handleWorkflowsSlashCommand(cmd *slashCommandContext) error {
	if strings.TrimSpace(cmd.args) != "" {
		return emitTextResponse(cmd.bridge, "usage: /workflows")
	}
	saved, err := loadSavedWorkflows(cmd.state.CWD)
	if err != nil {
		// The workflows that did load still open in the dialog; the notice
		// names the files that need fixing.
		if emitErr := cmd.bridge.EmitNotice(fmt.Sprintf("Some saved workflows could not be loaded: %v", err)); emitErr != nil {
			return emitErr
		}
	}
	if err := cmd.bridge.Emit(ipc.EventWorkflowsRequested, ipc.WorkflowsRequestedPayload{
		Saved: savedWorkflowPayloads(saved),
	}); err != nil {
		return err
	}
	return emitTextResponse(cmd.bridge, "")
}

func savedWorkflowPayloads(saved []workflowpkg.Saved) []ipc.SavedWorkflowPayload {
	payloads := make([]ipc.SavedWorkflowPayload, 0, len(saved))
	for _, entry := range saved {
		payloads = append(payloads, ipc.SavedWorkflowPayload{
			Name:        entry.Name,
			Description: entry.Meta.Description,
			WhenToUse:   entry.Meta.WhenToUse,
			Scope:       entry.Scope,
			Path:        entry.Path,
		})
	}
	return payloads
}
