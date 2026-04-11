package tools

import (
	"errors"
	"strings"
)

type EditFailureKind string

const (
	EditFailureInvalidRequest       EditFailureKind = "invalid_request"
	EditFailureInvalidPatchFormat   EditFailureKind = "invalid_patch_format"
	EditFailureTargetMissing        EditFailureKind = "target_missing"
	EditFailureNoMatch              EditFailureKind = "no_match"
	EditFailureMultipleMatch        EditFailureKind = "multiple_matches"
	EditFailureNoOp                 EditFailureKind = "no_op"
	EditFailureInvalidRange         EditFailureKind = "invalid_range"
	EditFailureOverlap              EditFailureKind = "overlapping_ranges"
	EditFailureContentMismatch      EditFailureKind = "content_mismatch"
	EditFailureUnsupportedOperation EditFailureKind = "unsupported_operation"
)

type EditFailure struct {
	Kind     EditFailureKind
	Message  string
	Hint     string
	FilePath string
}

func (e *EditFailure) Error() string {
	if e == nil {
		return ""
	}
	if strings.TrimSpace(e.Hint) == "" {
		return e.Message
	}
	return e.Message + " Recovery hint: " + e.Hint
}

func NewEditFailure(kind EditFailureKind, filePath, message, hint string) *EditFailure {
	return &EditFailure{
		Kind:     kind,
		Message:  strings.TrimSpace(message),
		Hint:     strings.TrimSpace(hint),
		FilePath: strings.TrimSpace(filePath),
	}
}

func ExtractEditFailure(err error) (*EditFailure, bool) {
	var editFailure *EditFailure
	if errors.As(err, &editFailure) && editFailure != nil {
		return editFailure, true
	}
	return nil, false
}

func EditFailureOutput(kind EditFailureKind, filePath, message, hint string) ToolOutput {
	editFailure := NewEditFailure(kind, filePath, message, hint)
	return ToolOutput{
		Output:    editFailure.Error(),
		IsError:   true,
		FilePath:  editFailure.FilePath,
		ErrorKind: string(editFailure.Kind),
		ErrorHint: editFailure.Hint,
	}
}
