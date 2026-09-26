package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

type NotebookEditTool struct{}

func NewNotebookEditTool() *NotebookEditTool {
	return &NotebookEditTool{}
}

func (t *NotebookEditTool) Name() string {
	return "notebook_edit"
}

func (t *NotebookEditTool) Description() string {
	return "Edit a Jupyter notebook at the cell level by inserting, updating, or deleting cells instead of modifying raw JSON."
}

func (t *NotebookEditTool) InputSchema() any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"filePath":   map[string]any{"type": "string", "description": "Absolute path to the .ipynb notebook file."},
			"file_path":  map[string]any{"type": "string", "description": "Compatibility alias for filePath."},
			"operation":  map[string]any{"type": "string", "enum": []string{"insert", "edit", "delete"}},
			"cellIndex":  map[string]any{"type": "integer", "minimum": 1, "description": "1-based cell index. For insert, defaults to appending when omitted."},
			"cell_index": map[string]any{"type": "integer", "minimum": 1, "description": "Snake_case alias for cellIndex."},
			"cellType":   map[string]any{"type": "string", "description": "Cell type for insert or edit, usually markdown or code."},
			"cell_type":  map[string]any{"type": "string", "description": "Snake_case alias for cellType."},
			"source":     map[string]any{"type": "string", "description": "Full cell source text for insert or edit."},
		},
		"required": []string{"operation"},
	}
}

func (t *NotebookEditTool) Permission() PermissionLevel {
	return PermissionWrite
}

func (t *NotebookEditTool) Concurrency(input ToolInput) ConcurrencyDecision {
	return ConcurrencySerial
}

func (t *NotebookEditTool) Validate(input ToolInput) error {
	filePath, ok := firstStringParam(input.Params, "filePath", "file_path")
	if !ok || strings.TrimSpace(filePath) == "" {
		return fmt.Errorf("notebook_edit requires filePath")
	}
	resolvedPath, err := resolveToolPath(filePath)
	if err != nil {
		return err
	}
	if !isNotebookFile(resolvedPath) {
		return fmt.Errorf("notebook_edit only supports .ipynb files")
	}
	operation, ok := stringParam(input.Params, "operation")
	if !ok || strings.TrimSpace(operation) == "" {
		return fmt.Errorf("notebook_edit requires operation")
	}
	switch strings.ToLower(strings.TrimSpace(operation)) {
	case "insert":
		if firstStringOrEmpty(input.Params, "cellType", "cell_type") == "" {
			return fmt.Errorf("notebook_edit insert requires cellType")
		}
	case "edit":
		if _, ok := firstIntParam(input.Params, "cellIndex", "cell_index"); !ok {
			return fmt.Errorf("notebook_edit edit requires cellIndex")
		}
		hasSource := false
		if _, exists := input.Params["source"]; exists {
			hasSource = true
		}
		hasCellType := firstStringOrEmpty(input.Params, "cellType", "cell_type") != ""
		if !hasSource && !hasCellType {
			return fmt.Errorf("notebook_edit edit requires source or cellType")
		}
	case "delete":
		if _, ok := firstIntParam(input.Params, "cellIndex", "cell_index"); !ok {
			return fmt.Errorf("notebook_edit delete requires cellIndex")
		}
	default:
		return fmt.Errorf("unsupported notebook_edit operation %q", operation)
	}
	return nil
}

func (t *NotebookEditTool) Execute(ctx context.Context, input ToolInput) (ToolOutput, error) {
	select {
	case <-ctx.Done():
		return ToolOutput{}, ctx.Err()
	default:
	}

	filePath, _ := firstStringParam(input.Params, "filePath", "file_path")
	filePath, err := resolveToolPath(filePath)
	if err != nil {
		return ToolOutput{}, err
	}
	contentBytes, err := os.ReadFile(filePath)
	if err != nil {
		return ToolOutput{}, fmt.Errorf("read notebook %q: %w", filePath, err)
	}

	// Numbers stay json.Number so they are written back exactly as they were
	// (1.0 stays 1.0).
	decoder := json.NewDecoder(bytes.NewReader(contentBytes))
	decoder.UseNumber()
	var notebook map[string]any
	if err := decoder.Decode(&notebook); err != nil {
		return ToolOutput{}, fmt.Errorf("parse notebook %q: %w", filePath, err)
	}
	rawCells, ok := notebook["cells"].([]any)
	if !ok {
		return ToolOutput{}, fmt.Errorf("notebook %q does not contain a valid cells array", filePath)
	}

	operation := strings.ToLower(strings.TrimSpace(firstStringOrEmpty(input.Params, "operation")))
	cellIndex, hasCellIndex := firstIntParam(input.Params, "cellIndex", "cell_index")
	cellType := normalizeNotebookEditCellType(firstStringOrEmpty(input.Params, "cellType", "cell_type"))
	// Source is cell content: read it untrimmed so indentation and blank
	// lines survive.
	source, _ := stringParam(input.Params, "source")

	message := ""
	switch operation {
	case "insert":
		insertAt := len(rawCells)
		if hasCellIndex {
			if cellIndex < 1 || cellIndex > len(rawCells)+1 {
				return ToolOutput{}, fmt.Errorf("cellIndex must be between 1 and %d", len(rawCells)+1)
			}
			insertAt = cellIndex - 1
		}
		newCell := buildNotebookCell(cellType, source)
		rawCells = append(rawCells[:insertAt], append([]any{newCell}, rawCells[insertAt:]...)...)
		message = fmt.Sprintf("Inserted notebook cell %d in %s", insertAt+1, filePath)
	case "edit":
		if cellIndex < 1 || cellIndex > len(rawCells) {
			return ToolOutput{}, fmt.Errorf("cellIndex must be between 1 and %d", len(rawCells))
		}
		cellMap, ok := rawCells[cellIndex-1].(map[string]any)
		if !ok {
			return ToolOutput{}, fmt.Errorf("cell %d is not a valid notebook cell", cellIndex)
		}
		if cellType != "" {
			applyNotebookCellType(cellMap, cellType)
		}
		if _, exists := input.Params["source"]; exists {
			cellMap["source"] = notebookSourceLines(source)
		}
		message = fmt.Sprintf("Edited notebook cell %d in %s", cellIndex, filePath)
	case "delete":
		if cellIndex < 1 || cellIndex > len(rawCells) {
			return ToolOutput{}, fmt.Errorf("cellIndex must be between 1 and %d", len(rawCells))
		}
		rawCells = append(rawCells[:cellIndex-1], rawCells[cellIndex:]...)
		message = fmt.Sprintf("Deleted notebook cell %d from %s", cellIndex, filePath)
	}
	notebook["cells"] = rawCells

	updatedContent, err := encodeNotebookLike(notebook, string(contentBytes))
	if err != nil {
		return ToolOutput{}, fmt.Errorf("marshal notebook %q: %w", filePath, err)
	}

	if err := trackFileBeforeWrite(filePath); err != nil {
		return ToolOutput{}, err
	}
	if err := os.WriteFile(filePath, []byte(updatedContent), 0o644); err != nil {
		return ToolOutput{}, fmt.Errorf("write notebook %q: %w", filePath, err)
	}
	invalidateFileReadState(filePath)
	preview, insertions, deletions := buildFileDiffPreview(string(contentBytes), updatedContent)

	return ToolOutput{
		Output:     message,
		FilePath:   filePath,
		Preview:    preview,
		Insertions: insertions,
		Deletions:  deletions,
	}, nil
}

// encodeNotebookLike writes notebook the way nbformat does — sorted keys and
// characters such as < and & left unescaped — using the original file's
// indentation and line endings, so an edit changes only the lines of the cells
// it touches instead of every line of the file.
func encodeNotebookLike(notebook map[string]any, original string) (string, error) {
	var encoded strings.Builder
	encoder := json.NewEncoder(&encoded)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", notebookIndent(original))
	if err := encoder.Encode(notebook); err != nil {
		return "", err
	}
	// JSON strings cannot hold a raw newline, so every "\n" here is layout.
	return strings.ReplaceAll(encoded.String(), "\n", newLineEndingText(original).lineEnding), nil
}

// notebookIndent returns the indentation of the first indented line of a
// notebook file, which is one level, or nbformat's single space.
func notebookIndent(content string) string {
	for line := range strings.SplitSeq(content, "\n") {
		body := strings.TrimLeft(line, " \t")
		if body != "" && len(body) < len(line) {
			return line[:len(line)-len(body)]
		}
	}
	return " "
}

func normalizeNotebookEditCellType(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return ""
	}
	return value
}

func applyNotebookCellType(cell map[string]any, cellType string) {
	if cell == nil {
		return
	}
	if cellType == "" {
		cellType = "markdown"
	}
	cell["cell_type"] = cellType
	if _, ok := cell["metadata"].(map[string]any); !ok {
		cell["metadata"] = map[string]any{}
	}
	if cellType == "code" {
		if _, ok := cell["execution_count"]; !ok {
			cell["execution_count"] = nil
		}
		if _, ok := cell["outputs"]; !ok {
			cell["outputs"] = []any{}
		}
		return
	}
	delete(cell, "execution_count")
	delete(cell, "outputs")
}

func buildNotebookCell(cellType, source string) map[string]any {
	if cellType == "" {
		cellType = "markdown"
	}
	cell := map[string]any{
		"cell_type": cellType,
		"metadata":  map[string]any{},
		"source":    notebookSourceLines(source),
	}
	applyNotebookCellType(cell, cellType)
	return cell
}

func notebookSourceLines(source string) []string {
	if source == "" {
		return []string{}
	}
	normalized := strings.ReplaceAll(source, "\r\n", "\n")
	parts := strings.SplitAfter(normalized, "\n")
	if len(parts) == 0 {
		return []string{normalized}
	}
	if parts[len(parts)-1] == "" {
		parts = parts[:len(parts)-1]
	}
	return parts
}
