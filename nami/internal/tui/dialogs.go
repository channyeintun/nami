package tui

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
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
		return renderDialogBox(width, title, fmt.Sprintf("%d option(s)", state.SelectionRequest.Count))
	case state.ArtifactReview != nil:
		artifact := state.ArtifactReview.Artifact
		body := fmt.Sprintf("%s v%d   %s approve   %s revise   %s cancel",
			artifact.Title,
			artifact.Version,
			dialogKeyStyle.Render("y"),
			dialogKeyStyle.Render("r"),
			dialogKeyStyle.Render("n"),
		)
		return renderDialogBox(width, "Artifact review", body)
	default:
		return ""
	}
}

func renderQuestionDialog(request questionRequestState, width int) string {
	parts := []string{fmt.Sprintf("%d prompt(s)", request.Count)}
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
	parts = append(parts, dialogKeyStyle.Render("y")+" answer", dialogKeyStyle.Render("n")+" cancel")
	return renderDialogBox(width, "Question", strings.Join(parts, "   "))
}

func renderPermissionDialog(request permissionRequestState, width int) string {
	parts := []string{}
	if request.Tool != "" {
		parts = append(parts, "tool "+request.Tool)
	}
	if request.Risk != "" {
		parts = append(parts, "risk "+request.Risk)
	}
	if request.Command != "" {
		parts = append(parts, request.Command)
	}
	parts = append(parts, dialogKeyStyle.Render("y")+" allow", dialogKeyStyle.Render("a")+" always", dialogKeyStyle.Render("n")+" deny")
	return renderDialogBox(width, "Permission requested", strings.Join(parts, "   "))
}

func renderDialogBox(width int, title string, body string) string {
	innerWidth := width - 4
	if innerWidth < 20 {
		innerWidth = width
	}
	content := lipgloss.JoinVertical(
		lipgloss.Left,
		dialogTitleStyle.Render(title),
		body,
	)
	return dialogStyle.Width(innerWidth).Render(content)
}

func hasActionableDialog(state uiState) bool {
	return state.PermissionRequest != nil || state.ArtifactReview != nil || state.QuestionRequest != nil
}

func dialogHeight(state uiState, width int) int {
	dialog := renderDialog(state, width)
	if strings.TrimSpace(dialog) == "" {
		return 0
	}
	return lipgloss.Height(dialog)
}
