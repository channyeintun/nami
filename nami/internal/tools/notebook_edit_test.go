package tools

import (
	"encoding/json"
	"os"
	"reflect"
	"strings"
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

// canonicalNotebook is laid out the way nbformat writes notebooks: one-space
// indentation, sorted keys, and characters such as < and & left unescaped.
const canonicalNotebook = `{
 "cells": [
  {
   "cell_type": "code",
   "execution_count": 1,
   "metadata": {},
   "outputs": [
    {
     "data": {
      "application/json": {
       "ratio": 1.0
      }
     },
     "metadata": {},
     "output_type": "execute_result"
    }
   ],
   "source": [
    "if a < b and c > d & e:\n",
    "    print(\"ok\")"
   ]
  },
  {
   "cell_type": "markdown",
   "metadata": {},
   "source": [
    "old text"
   ]
  }
 ],
 "metadata": {
  "kernelspec": {
   "display_name": "Python 3",
   "language": "python",
   "name": "python3"
  }
 },
 "nbformat": 4,
 "nbformat_minor": 5
}
`

// Editing one cell must change only that cell's lines in the file, or every
// notebook edit shows up as a rewrite of the whole file.
func TestNotebookEditKeepsNotebookFormatting(t *testing.T) {
	for _, lineEnding := range []string{"\n", "\r\n"} {
		t.Run(strings.ReplaceAll(strings.ReplaceAll(lineEnding, "\r", "CR"), "\n", "LF"), func(t *testing.T) {
			workspace := inWorkspace(t)
			original := strings.ReplaceAll(canonicalNotebook, "\n", lineEnding)
			path := writeWorkspaceFile(t, workspace, "book.ipynb", original)

			runNotebookEdit(t, map[string]any{"filePath": path, "operation": "edit", "cellIndex": 2, "source": "new text"})

			want := strings.Replace(original, `"old text"`, `"new text"`, 1)
			if got := readWorkspaceFile(t, path); got != want {
				t.Fatalf("notebook rewritten:\n%s\nwant:\n%s", got, want)
			}
		})
	}
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
