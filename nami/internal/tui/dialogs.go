package tui

import (
	"fmt"
	"strings"
)

func renderDialog(state uiState, width int) string {
	switch {
	case state.PermissionRequest != nil:
		return renderPermissionDialog(*state.PermissionRequest, width)
	case state.QuestionRequest != nil:
		return renderQuestionDialog(*state.QuestionRequest, width)
	case state.SelectionRequest != nil:
		title := strings.TrimSpace(state.SelectionRequest.Title)
		if title == "" {
			title = state.SelectionRequest.Kind + " selection"
		}
		return dialogStyle.Width(width).Render(fmt.Sprintf("%s: %d option(s)", title, state.SelectionRequest.Count))
	case state.ArtifactReview != nil:
		artifact := state.ArtifactReview.Artifact
		return dialogStyle.Width(width).Render(fmt.Sprintf("Artifact review: %s v%d | y approve | r revise | n cancel", artifact.Title, artifact.Version))
	default:
		return ""
	}
}

func renderQuestionDialog(request questionRequestState, width int) string {
	parts := []string{fmt.Sprintf("Question requested: %d prompt(s)", request.Count)}
	for _, question := range request.Questions {
		option, ok := defaultQuestionOption(question.Options)
		if !ok {
			continue
		}
		label := strings.TrimSpace(option.Label)
		if label == "" {
			label = option.Value
		}
		parts = append(parts, question.Header+"="+label)
	}
	parts = append(parts, "y answer", "n cancel")
	return dialogStyle.Width(width).Render(strings.Join(parts, " | "))
}

func renderPermissionDialog(request permissionRequestState, width int) string {
	parts := []string{"Permission requested"}
	if request.Tool != "" {
		parts = append(parts, "tool "+request.Tool)
	}
	if request.Risk != "" {
		parts = append(parts, "risk "+request.Risk)
	}
	if request.Command != "" {
		parts = append(parts, request.Command)
	}
	parts = append(parts, "y allow", "a always", "n deny")
	return dialogStyle.Width(width).Render(strings.Join(parts, " | "))
}

func hasActionableDialog(state uiState) bool {
	return state.PermissionRequest != nil || state.ArtifactReview != nil || state.QuestionRequest != nil
}

func dialogHeight(state uiState, width int) int {
	dialog := renderDialog(state, width)
	if strings.TrimSpace(dialog) == "" {
		return 0
	}
	return 1
}
