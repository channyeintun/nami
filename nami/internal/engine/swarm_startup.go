package engine

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	artifactspkg "github.com/channyeintun/nami/internal/artifacts"
	"github.com/channyeintun/nami/internal/ipc"
	"github.com/channyeintun/nami/internal/swarm"
)

const (
	swarmSpecArtifactSlot   = "startup"
	swarmSpecArtifactTitle  = "Swarm Spec"
	swarmSpecArtifactSource = "swarm-startup"
)

func maybeEmitSwarmSpecStartup(
	ctx context.Context,
	bridge ipc.EventSink,
	artifactManager *artifactspkg.Manager,
	sessionID string,
	cwd string,
) error {
	path := strings.TrimSpace(swarm.ProjectSpecPath(cwd))
	if path == "" {
		return nil
	}

	resolved, err := swarm.LoadProjectSpec(cwd)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return upsertSwarmSpecArtifact(ctx, bridge, artifactManager, sessionID, swarmSpecArtifactMarkdown(path, ResolvedSpecArtifactStateInvalid, err.Error(), ""), map[string]any{
			"status":    string(ResolvedSpecArtifactStateInvalid),
			"spec_path": path,
			"error":     err.Error(),
		})
	}

	return upsertSwarmSpecArtifact(ctx, bridge, artifactManager, sessionID, resolved.SummaryMarkdown(), map[string]any{
		"status":       string(ResolvedSpecArtifactStateValid),
		"spec_path":    resolved.Path,
		"project_root": resolved.ProjectRoot,
		"role_count":   len(resolved.Roles),
	})
}

type resolvedSpecArtifactState string

const (
	ResolvedSpecArtifactStateValid   resolvedSpecArtifactState = "valid"
	ResolvedSpecArtifactStateInvalid resolvedSpecArtifactState = "invalid"
)

func upsertSwarmSpecArtifact(
	ctx context.Context,
	bridge ipc.EventSink,
	artifactManager *artifactspkg.Manager,
	sessionID string,
	content string,
	metadata map[string]any,
) error {
	if artifactManager == nil || strings.TrimSpace(sessionID) == "" {
		return nil
	}

	artifact, _, created, err := artifactManager.UpsertSessionMarkdown(ctx, artifactspkg.MarkdownRequest{
		Kind:     artifactspkg.KindKnowledgeItem,
		Scope:    artifactspkg.ScopeSession,
		Title:    swarmSpecArtifactTitle,
		Source:   swarmSpecArtifactSource,
		Content:  content,
		Metadata: metadata,
	}, sessionID, swarmSpecArtifactSlot)
	if err != nil {
		return err
	}

	if bridge != nil {
		if created {
			if err := emitArtifactCreated(bridge, artifact); err != nil {
				return err
			}
		}
		if err := emitArtifactUpdated(bridge, artifact, content); err != nil {
			return err
		}
		status := strings.TrimSpace(artifactMetadataString(artifact, "status"))
		specPath := strings.TrimSpace(artifactMetadataString(artifact, "spec_path"))
		if status == string(ResolvedSpecArtifactStateValid) {
			roleCount := swarmMetadataInt(artifact.Metadata, "role_count")
			if err := bridge.EmitNotice(fmt.Sprintf("Loaded swarm spec from %s with %d role(s).", specPath, roleCount)); err != nil {
				return err
			}
		} else if status == string(ResolvedSpecArtifactStateInvalid) {
			if err := bridge.EmitNotice(fmt.Sprintf("Invalid swarm spec at %s. Review the Swarm Spec artifact for details.", specPath)); err != nil {
				return err
			}
		}
	}

	return nil
}

func swarmMetadataInt(metadata map[string]any, key string) int {
	if metadata == nil {
		return 0
	}
	value, ok := metadata[key]
	if !ok {
		return 0
	}
	switch typed := value.(type) {
	case int:
		return typed
	case int64:
		return int(typed)
	case float64:
		return int(typed)
	default:
		return 0
	}
}

func swarmSpecArtifactMarkdown(path string, state resolvedSpecArtifactState, errorMessage string, summary string) string {
	var b strings.Builder
	b.WriteString("# Swarm Spec\n\n")
	b.WriteString("## Status\n\n")
	b.WriteString(fmt.Sprintf("- Status: %s\n", state))
	if strings.TrimSpace(path) != "" {
		b.WriteString(fmt.Sprintf("- Spec path: %s\n", strings.TrimSpace(path)))
	}
	if strings.TrimSpace(summary) != "" {
		b.WriteString(fmt.Sprintf("- Summary: %s\n", strings.TrimSpace(summary)))
	}
	if strings.TrimSpace(errorMessage) != "" {
		b.WriteString("\n## Error\n\n")
		b.WriteString(strings.TrimSpace(errorMessage))
		b.WriteString("\n")
	}
	return b.String()
}
