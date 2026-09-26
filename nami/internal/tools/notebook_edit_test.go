package tools

import (
	"encoding/json"
	"os"
	"reflect"
	"testing"
)

const emptyNotebook = `{"cells": [], "metadata": {}, "nbformat": 4, "nbformat_minor": 5}`

func runNotebookEdit(t *testing.T, params map[string]any) ToolOutput {
	t.Helper()
	output, err := runValidatedTool(t, NewNotebookEditTool(), params)
	if err != nil {
		t.Fatalf("notebook_edit: %v", err)
	}
	return output
}

func notebookCellSources(t *testing.T, path string) [][]string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read notebook: %v", err)
	}
	var notebook struct {
		Cells []struct {
			Source []string `json:"source"`
		} `json:"cells"`
	}
	if err := json.Unmarshal(data, &notebook); err != nil {
		t.Fatalf("parse notebook: %v", err)
	}
	sources := make([][]string, 0, len(notebook.Cells))
	for _, cell := range notebook.Cells {
		sources = append(sources, cell.Source)
	}
	return sources
}

// Cell source is content, not an identifier: leading indentation (a markdown
// code block) and blank lines have to survive exactly as sent.
func TestNotebookEditKeepsCellSourceVerbatim(t *testing.T) {
	workspace := inWorkspace(t)
	path := writeWorkspaceFile(t, workspace, "book.ipynb", emptyNotebook)

	runNotebookEdit(t, map[string]any{
		"filePath":  path,
		"operation": "insert",
		"cellType":  "markdown",
		"source":    "    indented code block\nsecond line\n",
	})
	runNotebookEdit(t, map[string]any{
		"filePath":  path,
		"operation": "insert",
		"cellType":  "code",
		"source":    "\n\nx = 1",
	})
	runNotebookEdit(t, map[string]any{
		"filePath":  path,
		"operation": "insert",
		"cellType":  "code",
		"source":    "placeholder",
	})
	runNotebookEdit(t, map[string]any{
		"filePath":  path,
		"operation": "edit",
		"cellIndex": 3,
		"source":    "  y = 2  ",
	})

	want := [][]string{
		{"    indented code block\n", "second line\n"},
		{"\n", "\n", "x = 1"},
		{"  y = 2  "},
	}
	if got := notebookCellSources(t, path); !reflect.DeepEqual(got, want) {
		t.Fatalf("cell sources = %q, want %q", got, want)
	}
}
