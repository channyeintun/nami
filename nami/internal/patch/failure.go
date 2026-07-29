// Package patch parses and applies the structured "*** Begin Patch" format
// used by the apply_patch tool. It is deliberately free of filesystem and tool
// plumbing so the format can be parsed, fuzzed, and reused on its own.
package patch

import "strings"

// FailureKind classifies why a patch could not be parsed or applied. The values
// match the edit-failure taxonomy the tool layer reports back to the model.
type FailureKind string

const (
	FailureInvalidFormat        FailureKind = "invalid_patch_format"
	FailureNoMatch              FailureKind = "no_match"
	FailureMultipleMatch        FailureKind = "multiple_matches"
	FailureOverlap              FailureKind = "overlapping_ranges"
	FailureNoOp                 FailureKind = "no_op"
	FailureUnsupportedOperation FailureKind = "unsupported_operation"
)

// Failure is a patch error carrying the classification and a recovery hint so
// callers can render an actionable message without string matching.
type Failure struct {
	Kind    FailureKind
	Path    string
	Message string
	Hint    string
}

func (f *Failure) Error() string {
	if f == nil {
		return ""
	}
	if strings.TrimSpace(f.Hint) == "" {
		return f.Message
	}
	return f.Message + " Recovery hint: " + f.Hint
}

func newFailure(kind FailureKind, path, message, hint string) *Failure {
	return &Failure{
		Kind:    kind,
		Path:    strings.TrimSpace(path),
		Message: strings.TrimSpace(message),
		Hint:    strings.TrimSpace(hint),
	}
}
