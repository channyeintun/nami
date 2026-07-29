package patch

import (
	"fmt"
	"strings"
)

// Action is the file-level operation a patch section performs.
type Action string

const (
	ActionAdd    Action = "add"
	ActionUpdate Action = "update"
	ActionDelete Action = "delete"
)

const (
	beginMarker      = "*** Begin Patch"
	endMarker        = "*** End Patch"
	addFileHeader    = "*** Add File:"
	updateFileHeader = "*** Update File:"
	deleteFileHeader = "*** Delete File:"
	hunkHeader       = "@@"
)

// Document is a parsed patch: an ordered list of file operations.
type Document struct {
	Operations []FileOperation
}

// FileOperation is one "*** Add/Update/Delete File" section. Lines carries the
// new file body for add sections; Hunks carries the edits for update sections.
type FileOperation struct {
	Action Action
	Path   string
	Hunks  []Hunk
	Lines  []string
}

// Hunk is a contiguous run of context, removal, and addition lines.
type Hunk struct {
	Lines []Line
}

// Line is a single hunk line. Kind is one of ' ' (context), '-', or '+'.
type Line struct {
	Kind  byte
	Value string
}

// Parse reads a structured patch into a Document. Every error it returns is a
// *Failure describing what to fix.
func Parse(text string) (Document, error) {
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != beginMarker {
		return Document{}, newFailure(FailureInvalidFormat, "", "apply_patch must start with "+beginMarker, "Wrap the patch body between "+beginMarker+" and "+endMarker+".")
	}

	document := Document{}
	var current *FileOperation
	body := make([]string, 0)
	finishCurrent := func() error {
		if current == nil {
			return nil
		}
		operation, err := finalizeOperation(*current, body)
		if err != nil {
			return err
		}
		document.Operations = append(document.Operations, operation)
		current = nil
		body = body[:0]
		return nil
	}

	for index := 1; index < len(lines); index++ {
		line := lines[index]
		trimmed := strings.TrimSpace(line)
		if trimmed == endMarker {
			if err := finishCurrent(); err != nil {
				return Document{}, err
			}
			if strings.TrimSpace(strings.Join(lines[index+1:], "")) != "" {
				return Document{}, newFailure(FailureInvalidFormat, "", "unexpected content after "+endMarker, "Remove any trailing lines after the end-of-patch marker.")
			}
			return document, nil
		}
		if isFileHeader(trimmed) {
			if err := finishCurrent(); err != nil {
				return Document{}, err
			}
			operation, err := parseHeader(trimmed)
			if err != nil {
				return Document{}, err
			}
			current = &operation
			continue
		}
		if current == nil {
			if trimmed == "" {
				continue
			}
			return Document{}, newFailure(FailureInvalidFormat, "", fmt.Sprintf("unexpected line outside a file section: %s", trimmed), "Start each file section with *** Add File, *** Update File, or *** Delete File.")
		}
		body = append(body, line)
	}

	return Document{}, newFailure(FailureInvalidFormat, "", "apply_patch must end with "+endMarker, "Add "+endMarker+" after the last file section.")
}

// Targets returns the file paths a patch touches, in document order.
func Targets(text string) ([]string, error) {
	document, err := Parse(text)
	if err != nil {
		return nil, err
	}
	targets := make([]string, 0, len(document.Operations))
	for _, operation := range document.Operations {
		targets = append(targets, strings.TrimSpace(operation.Path))
	}
	return targets, nil
}

func isFileHeader(trimmed string) bool {
	return strings.HasPrefix(trimmed, addFileHeader) ||
		strings.HasPrefix(trimmed, updateFileHeader) ||
		strings.HasPrefix(trimmed, deleteFileHeader)
}

func parseHeader(line string) (FileOperation, error) {
	headers := []struct {
		prefix string
		action Action
	}{
		{addFileHeader, ActionAdd},
		{updateFileHeader, ActionUpdate},
		{deleteFileHeader, ActionDelete},
	}
	for _, header := range headers {
		if !strings.HasPrefix(line, header.prefix) {
			continue
		}
		path := strings.TrimSpace(strings.TrimPrefix(line, header.prefix))
		if path == "" {
			return FileOperation{}, newFailure(FailureInvalidFormat, "", fmt.Sprintf("missing file path in patch header: %s", line), "Provide a file path after the patch action header.")
		}
		return FileOperation{Action: header.action, Path: path}, nil
	}
	return FileOperation{}, newFailure(FailureInvalidFormat, "", fmt.Sprintf("unsupported patch header: %s", line), "Use *** Add File, *** Update File, or *** Delete File headers.")
}

func finalizeOperation(operation FileOperation, body []string) (FileOperation, error) {
	switch operation.Action {
	case ActionAdd:
		lines, err := addedFileLines(operation.Path, body)
		if err != nil {
			return FileOperation{}, err
		}
		operation.Lines = lines
		return operation, nil
	case ActionDelete:
		for _, line := range body {
			if strings.TrimSpace(line) != "" {
				return FileOperation{}, newFailure(FailureInvalidFormat, operation.Path, fmt.Sprintf("delete file sections cannot contain body lines: %s", strings.TrimSpace(line)), "Remove hunk lines from *** Delete File sections.")
			}
		}
		return operation, nil
	case ActionUpdate:
		hunks, err := updateHunks(operation.Path, body)
		if err != nil {
			return FileOperation{}, err
		}
		operation.Hunks = hunks
		return operation, nil
	default:
		return FileOperation{}, newFailure(FailureUnsupportedOperation, operation.Path, fmt.Sprintf("unsupported patch action: %s", operation.Action), "Use *** Add File, *** Update File, or *** Delete File sections only.")
	}
}

func addedFileLines(path string, body []string) ([]string, error) {
	lines := make([]string, 0, len(body))
	for _, line := range body {
		if strings.HasPrefix(line, hunkHeader) {
			continue
		}
		if !strings.HasPrefix(line, "+") {
			return nil, newFailure(FailureInvalidFormat, path, fmt.Sprintf("add file sections only support + lines: %s", line), "Prefix every added line with + inside *** Add File sections.")
		}
		lines = append(lines, strings.TrimPrefix(line, "+"))
	}
	return lines, nil
}

func updateHunks(path string, body []string) ([]Hunk, error) {
	hunks := make([]Hunk, 0)
	current := Hunk{}
	currentHasChange := false
	sawBody := false

	finishHunk := func() {
		if len(current.Lines) > 0 && currentHasChange {
			hunks = append(hunks, current)
		}
		current = Hunk{}
		currentHasChange = false
	}

	for _, line := range body {
		if strings.HasPrefix(line, hunkHeader) {
			finishHunk()
			continue
		}
		sawBody = true
		kind, value := classifyHunkLine(line)
		if kind == '+' || kind == '-' {
			currentHasChange = true
		}
		current.Lines = append(current.Lines, Line{Kind: kind, Value: value})
	}
	finishHunk()

	if !sawBody || len(hunks) == 0 {
		return nil, newFailure(FailureInvalidFormat, path, fmt.Sprintf("update section for %s did not contain any hunk lines", path), "Add context lines plus +/- changes under *** Update File sections.")
	}
	return hunks, nil
}

// classifyHunkLine splits a hunk line into its marker and payload. Lines
// without a recognized marker are treated as context so patches that drop the
// leading space on blank context lines still apply.
func classifyHunkLine(line string) (byte, string) {
	if line == "" {
		return ' ', ""
	}
	switch line[0] {
	case ' ', '+', '-':
		return line[0], line[1:]
	default:
		return ' ', line
	}
}
