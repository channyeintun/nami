package tools

import (
	"context"
	"fmt"
	"strings"
)

// FileHistoryTool exposes read-only inspection and snapshot operations for the current session's file history.
type FileHistoryTool struct{}

// NewFileHistoryTool constructs the file history tool.
func NewFileHistoryTool() *FileHistoryTool {
	return &FileHistoryTool{}
}

func (t *FileHistoryTool) Name() string {
	return "file_history"
}

func (t *FileHistoryTool) Description() string {
	return "Inspect the current session's tracked file history, create a named snapshot, or compare the current workspace against a recorded snapshot."
}

func (t *FileHistoryTool) InputSchema() any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"action": map[string]any{
				"type":        "string",
				"description": "File history action to run.",
				"enum":        []string{"status", "snapshot", "latest_snapshot", "diff_stats"},
			},
			"label": map[string]any{
				"type":        "string",
				"description": "Optional snapshot label for action=snapshot.",
			},
			"snapshot_id": map[string]any{
				"type":        "string",
				"description": "Snapshot id for action=diff_stats. Defaults to the latest snapshot when omitted.",
			},
		},
		"required": []string{"action"},
	}
}

func (t *FileHistoryTool) Permission() PermissionLevel {
	return PermissionReadOnly
}

func (t *FileHistoryTool) Concurrency(input ToolInput) ConcurrencyDecision {
	return ConcurrencySerial
}

func (t *FileHistoryTool) Execute(ctx context.Context, input ToolInput) (ToolOutput, error) {
	select {
	case <-ctx.Done():
		return ToolOutput{}, ctx.Err()
	default:
	}

	history := GetGlobalFileHistory()
	if history == nil {
		return ToolOutput{}, fmt.Errorf("file history is unavailable")
	}

	action, ok := stringParam(input.Params, "action")
	if !ok || strings.TrimSpace(action) == "" {
		return ToolOutput{}, fmt.Errorf("file_history requires action")
	}

	switch strings.TrimSpace(action) {
	case "status":
		latest := history.LatestSnapshotID()
		if latest == "" {
			latest = "none"
		}
		return ToolOutput{Output: fmt.Sprintf("Tracked files: %d\nSnapshots: %d\nLatest snapshot: %s", history.TrackedFileCount(), history.SnapshotCount(), latest)}, nil
	case "snapshot":
		label, _ := stringParam(input.Params, "label")
		label = strings.TrimSpace(label)
		if label == "" {
			label = "snapshot"
		}
		return ToolOutput{Output: fmt.Sprintf("Created snapshot: %s", history.MakeSnapshot(label))}, nil
	case "latest_snapshot":
		snapshotID := history.LatestSnapshotID()
		if snapshotID == "" {
			return ToolOutput{Output: "No snapshots recorded."}, nil
		}
		return ToolOutput{Output: snapshotID}, nil
	case "diff_stats":
		snapshotID, _ := stringParam(input.Params, "snapshot_id")
		snapshotID = strings.TrimSpace(snapshotID)
		if snapshotID == "" {
			snapshotID = history.LatestSnapshotID()
		}
		if snapshotID == "" {
			return ToolOutput{}, fmt.Errorf("no snapshot available for diff_stats")
		}
		insertions, deletions := history.DiffStats(snapshotID)
		return ToolOutput{Output: fmt.Sprintf("Snapshot: %s\nInsertions: %d\nDeletions: %d", snapshotID, insertions, deletions)}, nil
	default:
		return ToolOutput{}, fmt.Errorf("unsupported file_history action %q", action)
	}
}

// FileHistoryRewindTool restores tracked files to a previously recorded snapshot.
type FileHistoryRewindTool struct{}

// NewFileHistoryRewindTool constructs the file history rewind tool.
func NewFileHistoryRewindTool() *FileHistoryRewindTool {
	return &FileHistoryRewindTool{}
}

func (t *FileHistoryRewindTool) Name() string {
	return "file_history_rewind"
}

func (t *FileHistoryRewindTool) Description() string {
	return "Restore tracked files to the state captured in a prior file_history snapshot."
}

func (t *FileHistoryRewindTool) InputSchema() any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"snapshot_id": map[string]any{
				"type":        "string",
				"description": "Snapshot id to restore. Use file_history latest_snapshot to inspect the latest id.",
			},
		},
		"required": []string{"snapshot_id"},
	}
}

func (t *FileHistoryRewindTool) Permission() PermissionLevel {
	return PermissionWrite
}

func (t *FileHistoryRewindTool) Concurrency(input ToolInput) ConcurrencyDecision {
	return ConcurrencySerial
}

func (t *FileHistoryRewindTool) Execute(ctx context.Context, input ToolInput) (ToolOutput, error) {
	select {
	case <-ctx.Done():
		return ToolOutput{}, ctx.Err()
	default:
	}

	history := GetGlobalFileHistory()
	if history == nil {
		return ToolOutput{}, fmt.Errorf("file history is unavailable")
	}

	snapshotID, ok := stringParam(input.Params, "snapshot_id")
	if !ok || strings.TrimSpace(snapshotID) == "" {
		return ToolOutput{}, fmt.Errorf("file_history_rewind requires snapshot_id")
	}

	result, err := history.Rewind(strings.TrimSpace(snapshotID))
	if err != nil {
		return ToolOutput{}, err
	}

	if len(result.Failed) > 0 {
		lines := []string{
			fmt.Sprintf(
				"Restored %d file%s from snapshot %s with %d failure%s.",
				result.Restored,
				pluralSuffix(result.Restored),
				strings.TrimSpace(snapshotID),
				len(result.Failed),
				pluralSuffix(len(result.Failed)),
			),
		}
		for _, failure := range result.Failed {
			lines = append(lines, "- "+failure)
		}
		return ToolOutput{Output: strings.Join(lines, "\n"), IsError: true}, nil
	}

	return ToolOutput{Output: fmt.Sprintf("Restored %d file%s from snapshot %s", result.Restored, pluralSuffix(result.Restored), strings.TrimSpace(snapshotID))}, nil
}
