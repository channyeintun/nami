package tools

import (
	"context"
	"os"
	"testing"
)

// runValidatedTool mirrors the engine: schema and semantic validation first,
// then execution.
func runValidatedTool(t *testing.T, tool Tool, params map[string]any) (ToolOutput, error) {
	t.Helper()
	input := ToolInput{Name: tool.Name(), Params: params}
	if err := ValidateToolCall(tool, input); err != nil {
		return ToolOutput{}, err
	}
	return tool.Execute(context.Background(), input)
}

func readWorkspaceFile(t *testing.T, path string) string {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(content)
}

// Replacement text is often empty (deleting a snippet) and snippets are often
// pure whitespace (collapsing blank lines, re-indenting); both are real edits.
func TestFileEditAcceptsEmptyAndWhitespaceStrings(t *testing.T) {
	cases := []struct {
		name      string
		content   string
		oldString string
		newString string
		want      string
	}{
		{"delete a line", "keep\ndrop me\nkeep\n", "drop me\n", "", "keep\nkeep\n"},
		{"collapse blank lines", "a\n\n\n\nb\n", "\n\n\n\n", "\n\n", "a\n\nb\n"},
		{"replace indentation", "x\n    y\n", "    y", "\ty", "x\n\ty\n"},
		{"replace text with a space", "a-b\n", "-", " ", "a b\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			workspace := inWorkspace(t)
			path := writeWorkspaceFile(t, workspace, "edit.txt", tc.content)

			output, err := runValidatedTool(t, NewFileEditTool(), map[string]any{
				"filePath":  path,
				"oldString": tc.oldString,
				"newString": tc.newString,
			})
			if err != nil {
				t.Fatalf("replace_string_in_file: %v", err)
			}
			if output.IsError {
				t.Fatalf("output = %s", output.Output)
			}
			if got := readWorkspaceFile(t, path); got != tc.want {
				t.Fatalf("content = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestFileEditStillRequiresOldAndNewString(t *testing.T) {
	workspace := inWorkspace(t)
	path := writeWorkspaceFile(t, workspace, "edit.txt", "content\n")

	cases := map[string]map[string]any{
		"empty oldString":   {"filePath": path, "oldString": "", "newString": "x"},
		"missing newString": {"filePath": path, "oldString": "content"},
	}
	for name, params := range cases {
		t.Run(name, func(t *testing.T) {
			output, err := runValidatedTool(t, NewFileEditTool(), params)
			if err == nil && !output.IsError {
				t.Fatalf("edit succeeded with output %q, want a failure", output.Output)
			}
			if got := readWorkspaceFile(t, path); got != "content\n" {
				t.Fatalf("file changed to %q", got)
			}
		})
	}
}

func TestMultiReplaceAcceptsDeletionsAndWhitespaceSnippets(t *testing.T) {
	workspace := inWorkspace(t)
	path := writeWorkspaceFile(t, workspace, "multi.txt", "keep\ndrop\n\n\n\nend\n")

	output, err := runValidatedTool(t, NewMultiReplaceStringInFileTool(), map[string]any{
		"explanation": "tidy",
		"replacements": []any{
			map[string]any{"filePath": path, "oldString": "drop\n", "newString": ""},
			map[string]any{"filePath": path, "oldString": "\n\n\n", "newString": "\n"},
		},
	})
	if err != nil {
		t.Fatalf("multi_replace_string_in_file: %v", err)
	}
	if output.IsError {
		t.Fatalf("output = %s", output.Output)
	}
	if got, want := readWorkspaceFile(t, path), "keep\n\nend\n"; got != want {
		t.Fatalf("content = %q, want %q", got, want)
	}
}

// Empty files are ordinary (__init__.py, .gitkeep, py.typed), and so is
// truncating a file to nothing.
func TestFileToolsWriteEmptyContent(t *testing.T) {
	workspace := inWorkspace(t)

	created := workspace + "/pkg/__init__.py"
	output, err := runValidatedTool(t, NewCreateFileTool(), map[string]any{"file_path": created, "content": ""})
	if err != nil {
		t.Fatalf("create_file: %v", err)
	}
	if output.IsError {
		t.Fatalf("create_file output = %s", output.Output)
	}
	if got := readWorkspaceFile(t, created); got != "" {
		t.Fatalf("created content = %q, want empty", got)
	}

	existing := writeWorkspaceFile(t, workspace, "notes.txt", "old notes\n")
	output, err = runValidatedTool(t, NewFileWriteTool(), map[string]any{"file_path": existing, "content": ""})
	if err != nil {
		t.Fatalf("file_write: %v", err)
	}
	if output.IsError {
		t.Fatalf("file_write output = %s", output.Output)
	}
	if got := readWorkspaceFile(t, existing); got != "" {
		t.Fatalf("written content = %q, want empty", got)
	}
}
