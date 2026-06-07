package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/channyeintun/nami/internal/agent"
	"github.com/channyeintun/nami/internal/api"
	artifactspkg "github.com/channyeintun/nami/internal/artifacts"
	"github.com/channyeintun/nami/internal/ipc"
)

func planRevisionFeedbackMessage(feedback string) string {
	feedback = strings.TrimSpace(feedback)
	if feedback == "" {
		return "Please revise the implementation plan, update the existing implementation-plan artifact, and resubmit it for review."
	}

	return "Please revise the implementation plan, update the existing implementation-plan artifact, and address this feedback:\n\n" + feedback
}

type planReviewGateResult struct {
	Decision string
	Feedback string
}

func handlePlanReviewGate(
	ctx context.Context,
	bridge ipc.EventSink,
	router *ipc.MessageRouter,
	mode *agent.ExecutionMode,
	artifactManager *artifactspkg.Manager,
	sessionID string,
	messages []api.Message,
	fromIndex int,
	stopReason string,
) (planReviewGateResult, error) {
	if *mode != agent.ModePlan {
		return planReviewGateResult{}, nil
	}
	if !turnUsedToolName(messages, fromIndex, "save_implementation_plan") && stopReason != "plan_review_required" {
		return planReviewGateResult{}, nil
	}

	artifact, found, err := artifactManager.FindSessionArtifact(ctx,
		artifactspkg.KindImplementationPlan, artifactspkg.ScopeSession, sessionID, "active")
	if err != nil || !found {
		return planReviewGateResult{}, err
	}
	if artifactMetadataString(artifact, "status") != "final" {
		return planReviewGateResult{}, nil
	}

	requestID := fmt.Sprintf("review-%d", time.Now().UnixNano())
	if err := bridge.Emit(ipc.EventArtifactReviewRequested, ipc.ArtifactReviewRequestedPayload{
		RequestID: requestID,
		ID:        artifact.ID,
		Kind:      string(artifact.Kind),
		Title:     artifact.Title,
		Version:   artifact.Version,
	}); err != nil {
		return planReviewGateResult{}, err
	}

	deferred := make([]ipc.ClientMessage, 0, 4)
	defer func() {
		router.Requeue(deferred...)
	}()

	for {
		msg, err := router.Next(ctx)
		if err != nil {
			return planReviewGateResult{}, err
		}

		switch msg.Type {
		case ipc.MsgArtifactReviewResponse:
			result, handled, err := resolvePlanReviewResponse(msg, requestID)
			if err != nil {
				return planReviewGateResult{}, err
			}
			if !handled {
				deferred = append(deferred, msg)
				continue
			}
			if err := emitPlanReviewResolution(bridge, requestID, result.Decision, mode); err != nil {
				return planReviewGateResult{}, err
			}
			return result, nil

		case ipc.MsgShutdown:
			return planReviewGateResult{}, context.Canceled
		default:
			deferred = append(deferred, msg)
		}
	}
}

func resolvePlanReviewResponse(msg ipc.ClientMessage, requestID string) (planReviewGateResult, bool, error) {
	var payload ipc.ArtifactReviewResponsePayload
	if err := json.Unmarshal(msg.Payload, &payload); err != nil {
		return planReviewGateResult{}, false, fmt.Errorf("decode artifact review response: %w", err)
	}
	if payload.RequestID != requestID {
		return planReviewGateResult{}, false, nil
	}

	decision := strings.TrimSpace(payload.Decision)
	feedback := strings.TrimSpace(payload.Feedback)

	resolvedDecision := "cancelled"
	switch decision {
	case "approve":
		resolvedDecision = "approved"
	case "revise":
		resolvedDecision = "revised"
	}

	return planReviewGateResult{Decision: resolvedDecision, Feedback: feedback}, true, nil
}

func emitPlanReviewResolution(bridge ipc.EventSink, requestID string, decision string, mode *agent.ExecutionMode) error {
	if err := bridge.Emit(ipc.EventArtifactReviewResolved, ipc.ArtifactReviewResolvedPayload{
		RequestID: requestID,
		Decision:  decision,
	}); err != nil {
		return err
	}

	if decision != "approved" {
		return nil
	}
	*mode = agent.ModeFast
	return bridge.Emit(ipc.EventModeChanged, ipc.ModeChangedPayload{Mode: string(agent.ModeFast)})
}

func turnUsedToolName(messages []api.Message, fromIndex int, toolName string) bool {
	for _, msg := range messages[fromIndex:] {
		if msg.Role != api.RoleAssistant {
			continue
		}
		for _, call := range msg.ToolCalls {
			if call.Name == toolName {
				return true
			}
		}
	}
	return false
}
