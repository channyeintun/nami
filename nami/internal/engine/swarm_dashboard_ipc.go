package engine

import (
	"context"

	"github.com/channyeintun/nami/internal/ipc"
	"github.com/channyeintun/nami/internal/session"
	"github.com/channyeintun/nami/internal/swarm"
)

func handleSwarmDashboardInspectMessage(ctx context.Context, bridge *ipc.Bridge, store *session.Store, sessionID string) error {
	handoffs, err := swarm.ListHandoffs(store, sessionID, "", nil)
	if err != nil {
		return err
	}
	payload := ipc.SwarmDashboardSnapshotPayload{Handoffs: make([]ipc.SwarmHandoffPayload, 0, len(handoffs))}
	for _, handoff := range handoffs {
		payload.Handoffs = append(payload.Handoffs, ipc.SwarmHandoffPayload{
			ID:           handoff.ID,
			ArtifactID:   handoff.ArtifactID,
			SourceRole:   handoff.SourceRole,
			TargetRole:   handoff.TargetRole,
			Summary:      handoff.Summary,
			ChangedFiles: append([]string(nil), handoff.ChangedFiles...),
			CommandsRun:  append([]string(nil), handoff.CommandsRun...),
			Verification: handoff.Verification,
			Risks:        append([]string(nil), handoff.Risks...),
			NextAction:   handoff.NextAction,
			Status:       string(handoff.Status),
			StatusNote:   handoff.StatusNote,
			CreatedAt:    handoff.CreatedAt,
			UpdatedAt:    handoff.UpdatedAt,
		})
	}
	return bridge.Emit(ipc.EventSwarmDashboardSnapshot, payload)
}
