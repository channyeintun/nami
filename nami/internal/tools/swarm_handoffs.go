package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"

	artifactspkg "github.com/channyeintun/nami/internal/artifacts"
	"github.com/channyeintun/nami/internal/session"
	"github.com/channyeintun/nami/internal/swarm"
)

const swarmHandoffArtifactSource = "swarm-handoff-tool"

type swarmRuntimeState struct {
	mu        sync.RWMutex
	sessionID string
	manager   *artifactspkg.Manager
	store     *session.Store
	cwd       string
}

var globalSwarmRuntime swarmRuntimeState

func SetGlobalSwarmRuntime(sessionID string, manager *artifactspkg.Manager, store *session.Store, cwd string) {
	globalSwarmRuntime.mu.Lock()
	defer globalSwarmRuntime.mu.Unlock()
	globalSwarmRuntime.sessionID = strings.TrimSpace(sessionID)
	globalSwarmRuntime.manager = manager
	globalSwarmRuntime.store = store
	globalSwarmRuntime.cwd = strings.TrimSpace(cwd)
}

func getSwarmRuntime() (string, *artifactspkg.Manager, *session.Store, string, error) {
	globalSwarmRuntime.mu.RLock()
	defer globalSwarmRuntime.mu.RUnlock()
	if strings.TrimSpace(globalSwarmRuntime.sessionID) == "" || globalSwarmRuntime.manager == nil || globalSwarmRuntime.store == nil {
		return "", nil, nil, "", fmt.Errorf("swarm runtime is unavailable")
	}
	return globalSwarmRuntime.sessionID, globalSwarmRuntime.manager, globalSwarmRuntime.store, globalSwarmRuntime.cwd, nil
}

func CurrentSwarmRuntimeSessionID() string {
	globalSwarmRuntime.mu.RLock()
	defer globalSwarmRuntime.mu.RUnlock()
	return strings.TrimSpace(globalSwarmRuntime.sessionID)
}

type SwarmSubmitHandoffTool struct{}

type SwarmListInboxTool struct{}

type SwarmUpdateHandoffTool struct{}

func NewSwarmSubmitHandoffTool() *SwarmSubmitHandoffTool { return &SwarmSubmitHandoffTool{} }

func NewSwarmListInboxTool() *SwarmListInboxTool { return &SwarmListInboxTool{} }

func NewSwarmUpdateHandoffTool() *SwarmUpdateHandoffTool { return &SwarmUpdateHandoffTool{} }

func (t *SwarmSubmitHandoffTool) Name() string { return "swarm_submit_handoff" }

func (t *SwarmSubmitHandoffTool) Description() string {
	return "Create a structured swarm handoff artifact and queue it in the target role inbox for later acknowledgement or resolution."
}

func (t *SwarmSubmitHandoffTool) InputSchema() any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"source_role": map[string]any{"type": "string", "description": "Role handing work off."},
			"target_role": map[string]any{"type": "string", "description": "Role that should receive the handoff."},
			"summary":     map[string]any{"type": "string", "description": "Concise task summary for the receiving role."},
			"changed_files": map[string]any{
				"type":        "array",
				"items":       map[string]any{"type": "string"},
				"description": "Files changed or inspected for this handoff.",
			},
			"commands_run": map[string]any{
				"type":        "array",
				"items":       map[string]any{"type": "string"},
				"description": "Important commands that were run.",
			},
			"verification": map[string]any{"type": "string", "description": "What was verified, or what remains unverified."},
			"risks": map[string]any{
				"type":        "array",
				"items":       map[string]any{"type": "string"},
				"description": "Known risks or follow-up concerns.",
			},
			"next_action": map[string]any{"type": "string", "description": "Requested next step for the receiving role."},
		},
		"required": []string{"source_role", "target_role", "summary"},
	}
}

func (t *SwarmSubmitHandoffTool) Permission() PermissionLevel { return PermissionReadOnly }

func (t *SwarmSubmitHandoffTool) Concurrency(input ToolInput) ConcurrencyDecision {
	return ConcurrencySerial
}

func (t *SwarmSubmitHandoffTool) Execute(ctx context.Context, input ToolInput) (ToolOutput, error) {
	sessionID, manager, store, cwd, err := getSwarmRuntime()
	if err != nil {
		return ToolOutput{}, err
	}

	handoff, err := swarm.PrepareHandoff(swarm.Handoff{
		ID:           swarm.NewHandoffID(),
		SourceRole:   firstStringOrEmpty(input.Params, "source_role"),
		TargetRole:   firstStringOrEmpty(input.Params, "target_role"),
		Summary:      firstStringOrEmpty(input.Params, "summary"),
		ChangedFiles: stringSliceParam(input.Params, "changed_files"),
		CommandsRun:  stringSliceParam(input.Params, "commands_run"),
		Verification: firstStringOrEmpty(input.Params, "verification"),
		Risks:        stringSliceParam(input.Params, "risks"),
		NextAction:   firstStringOrEmpty(input.Params, "next_action"),
	})
	if err != nil {
		return ToolOutput{}, err
	}
	if err := validateHandoffSpec(cwd, handoff); err != nil {
		return ToolOutput{}, err
	}

	artifact, _, created, err := saveHandoffArtifact(ctx, manager, sessionID, handoff)
	if err != nil {
		return ToolOutput{}, err
	}
	persisted, err := swarm.UpsertHandoff(store, sessionID, withHandoffArtifact(handoff, artifact.ID))
	if err != nil {
		return ToolOutput{}, err
	}

	return ToolOutput{
		Output: fmt.Sprintf("Queued swarm handoff %s from %s to %s.", persisted.ID, persisted.SourceRole, persisted.TargetRole),
		Artifacts: []ArtifactMutation{{
			Artifact: artifact,
			Content:  swarm.RenderHandoffMarkdown(persisted),
			Created:  created,
			Focused:  false,
		}},
	}, nil
}

func (t *SwarmListInboxTool) Name() string { return "swarm_list_inbox" }

func (t *SwarmListInboxTool) Description() string {
	return "List or dequeue swarm handoffs for a role. When dequeue is true the role's queue policy (fifo, batch-review, latest-wins) is applied."
}

func (t *SwarmListInboxTool) InputSchema() any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"role": map[string]any{"type": "string", "description": "Optional role filter. Matches either source or target role."},
			"statuses": map[string]any{
				"type":        "array",
				"items":       map[string]any{"type": "string", "enum": []string{"pending", "acked", "in_progress", "completed", "blocked", "superseded"}},
				"description": "Optional status filters.",
			},
			"status":  map[string]any{"type": "string", "description": "Optional single status filter."},
			"dequeue": map[string]any{"type": "boolean", "description": "When true, apply the role's queue policy and return only the next actionable handoffs. Requires role."},
		},
	}
}

func (t *SwarmListInboxTool) Permission() PermissionLevel { return PermissionReadOnly }

func (t *SwarmListInboxTool) Concurrency(input ToolInput) ConcurrencyDecision {
	return ConcurrencyParallel
}

func (t *SwarmListInboxTool) Execute(ctx context.Context, input ToolInput) (ToolOutput, error) {
	sessionID, _, store, cwd, err := getSwarmRuntime()
	if err != nil {
		return ToolOutput{}, err
	}
	role := firstStringOrEmpty(input.Params, "role")
	if boolParam(input.Params, "dequeue") {
		return dequeueInbox(store, sessionID, cwd, role)
	}
	statuses := collectHandoffStatuses(input.Params)
	handoffs, err := swarm.ListHandoffs(store, sessionID, role, statuses)
	if err != nil {
		return ToolOutput{}, err
	}
	return marshalInboxResult(handoffs)
}

func (t *SwarmUpdateHandoffTool) Name() string { return "swarm_update_handoff" }

func (t *SwarmUpdateHandoffTool) Description() string {
	return "Update a swarm handoff status to acknowledge work, mark it in progress, complete it, or block it with a note."
}

func (t *SwarmUpdateHandoffTool) InputSchema() any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"handoff_id": map[string]any{"type": "string", "description": "The handoff identifier returned by swarm_submit_handoff."},
			"status":     map[string]any{"type": "string", "enum": []string{"pending", "acked", "in_progress", "completed", "blocked"}},
			"note":       map[string]any{"type": "string", "description": "Optional status note or resolution detail."},
		},
		"required": []string{"handoff_id", "status"},
	}
}

func (t *SwarmUpdateHandoffTool) Permission() PermissionLevel { return PermissionReadOnly }

func (t *SwarmUpdateHandoffTool) Concurrency(input ToolInput) ConcurrencyDecision {
	return ConcurrencySerial
}

func (t *SwarmUpdateHandoffTool) Execute(ctx context.Context, input ToolInput) (ToolOutput, error) {
	sessionID, manager, store, _, err := getSwarmRuntime()
	if err != nil {
		return ToolOutput{}, err
	}
	requestedStatus := swarm.NormalizeHandoffStatus(firstStringOrEmpty(input.Params, "status"))
	if requestedStatus == swarm.HandoffStatusSuperseded {
		return ToolOutput{}, fmt.Errorf("status %q is managed by queue policy and cannot be set manually", requestedStatus)
	}
	updated, err := swarm.UpdateHandoffStatus(store, sessionID, firstStringOrEmpty(input.Params, "handoff_id"), requestedStatus, firstStringOrEmpty(input.Params, "note"))
	if err != nil {
		return ToolOutput{}, err
	}
	artifact, _, created, err := saveHandoffArtifact(ctx, manager, sessionID, updated)
	if err != nil {
		return ToolOutput{}, err
	}
	return ToolOutput{
		Output: fmt.Sprintf("Updated swarm handoff %s to %s.", updated.ID, updated.Status),
		Artifacts: []ArtifactMutation{{
			Artifact: artifact,
			Content:  swarm.RenderHandoffMarkdown(updated),
			Created:  created,
			Focused:  false,
		}},
	}, nil
}

func saveHandoffArtifact(ctx context.Context, manager *artifactspkg.Manager, sessionID string, handoff swarm.Handoff) (artifactspkg.Artifact, artifactspkg.ArtifactVersion, bool, error) {
	return manager.UpsertSessionMarkdown(ctx, artifactspkg.MarkdownRequest{
		ID:      strings.TrimSpace(handoff.ArtifactID),
		Kind:    artifactspkg.KindHandoff,
		Scope:   artifactspkg.ScopeSession,
		Title:   fmt.Sprintf("Handoff: %s -> %s", handoff.SourceRole, handoff.TargetRole),
		Source:  swarmHandoffArtifactSource,
		Content: swarm.RenderHandoffMarkdown(handoff),
		Metadata: map[string]any{
			"handoff_id":    handoff.ID,
			"source_role":   handoff.SourceRole,
			"target_role":   handoff.TargetRole,
			"status":        string(handoff.Status),
			"queue_state":   string(handoff.Status),
			"summary":       handoff.Summary,
			"changed_files": append([]string(nil), handoff.ChangedFiles...),
			"commands_run":  append([]string(nil), handoff.CommandsRun...),
			"risks":         append([]string(nil), handoff.Risks...),
			"next_action":   handoff.NextAction,
		},
	}, sessionID, handoffArtifactSlot(handoff.ID))
}

func validateHandoffSpec(cwd string, handoff swarm.Handoff) error {
	spec, err := swarm.LoadProjectSpec(cwd)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	return swarm.ValidateHandoffAgainstSpec(spec, handoff)
}

func withHandoffArtifact(handoff swarm.Handoff, artifactID string) swarm.Handoff {
	handoff.ArtifactID = strings.TrimSpace(artifactID)
	if handoff.ArtifactID == "" {
		handoff.ArtifactID = handoff.ID
	}
	return handoff
}

func handoffArtifactSlot(id string) string {
	return "handoff:" + strings.TrimSpace(id)
}

func collectHandoffStatuses(params map[string]any) []swarm.HandoffStatus {
	values := make([]string, 0, 4)
	values = append(values, stringSliceParam(params, "statuses")...)
	if status := strings.TrimSpace(firstStringOrEmpty(params, "status")); status != "" {
		values = append(values, status)
	}
	statuses := make([]swarm.HandoffStatus, 0, len(values))
	seen := make(map[swarm.HandoffStatus]struct{}, len(values))
	for _, value := range values {
		normalized := swarm.NormalizeHandoffStatus(value)
		if !swarm.IsValidHandoffStatus(normalized) {
			continue
		}
		if _, ok := seen[normalized]; ok {
			continue
		}
		seen[normalized] = struct{}{}
		statuses = append(statuses, normalized)
	}
	return statuses
}

func dequeueInbox(store *session.Store, sessionID string, cwd string, role string) (ToolOutput, error) {
	if strings.TrimSpace(role) == "" {
		return ToolOutput{}, fmt.Errorf("dequeue requires a role parameter")
	}
	policy, err := resolveRoleQueuePolicy(cwd, role)
	if err != nil {
		return ToolOutput{}, err
	}
	handoffs, err := swarm.DequeueHandoffs(store, sessionID, role, policy)
	if err != nil {
		return ToolOutput{}, err
	}
	return marshalInboxResult(handoffs)
}

func resolveRoleQueuePolicy(cwd string, role string) (swarm.QueuePolicy, error) {
	spec, err := swarm.LoadProjectSpec(cwd)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return swarm.QueueFIFO, nil
		}
		return "", err
	}
	resolved, ok := spec.Role(role)
	if !ok {
		return swarm.QueueFIFO, nil
	}
	return resolved.QueuePolicy, nil
}

func marshalInboxResult(handoffs []swarm.Handoff) (ToolOutput, error) {
	payload := map[string]any{
		"count":    len(handoffs),
		"handoffs": handoffs,
	}
	encoded, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return ToolOutput{}, fmt.Errorf("marshal swarm inbox: %w", err)
	}
	return ToolOutput{Output: string(encoded)}, nil
}
