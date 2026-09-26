package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// writeNotebook writes a notebook whose code cells have the given sources.
func writeNotebook(t *testing.T, workspace, name string, sources ...[]string) string {
	t.Helper()
	cells := make([]any, 0, len(sources))
	for _, source := range sources {
		cells = append(cells, map[string]any{"cell_type": "code", "source": source, "metadata": map[string]any{}})
	}
	data, err := json.Marshal(map[string]any{"cells": cells, "metadata": map[string]any{}, "nbformat": 4, "nbformat_minor": 5})
	if err != nil {
		t.Fatalf("marshal notebook: %v", err)
	}
	return writeWorkspaceFile(t, workspace, name, string(data))
}

// bulkyCellSource renders to roughly lineCount*lineChars bytes.
func bulkyCellSource(marker string, lineCount, lineChars int) []string {
	lines := make([]string, 0, lineCount)
	for range lineCount {
		lines = append(lines, marker+strings.Repeat("x", lineChars)+"\n")
	}
	return lines
}

func readNotebookAt(t *testing.T, path string, offset int) ToolOutput {
	t.Helper()
	output, err := NewFileReadTool().Execute(context.Background(), ToolInput{Params: map[string]any{"filePath": path, "offset": offset}})
	if err != nil {
		t.Fatalf("read_file offset=%d: %v", offset, err)
	}
	if len(output.Output) > fileReadMaxOutputBytes {
		t.Fatalf("read_file returned %d bytes, over the %d byte limit", len(output.Output), fileReadMaxOutputBytes)
	}
	return output
}

// Paging through a notebook must always make progress: a read returns at
// least one cell, and the continuation offset points past what it returned.
func TestNotebookReadPagesPastOversizedCells(t *testing.T) {
	workspace := inWorkspace(t)
	path := writeNotebook(t, workspace, "big.ipynb",
		bulkyCellSource("first", 60, 1500), // ~90 KB, more than a whole read
		bulkyCellSource("second", 20, 1500),
		[]string{"print('last')\n"},
	)

	first := readNotebookAt(t, path, 1)
	for _, want := range []string{"Notebook cells 1-1 of 3", "[Cell 1]", "firstxxx", "[cell truncated", "Continue with offset=2"} {
		if !strings.Contains(first.Output, want) {
			t.Fatalf("first read = %.300q..., want it to contain %q", first.Output, want)
		}
	}
	if !first.Truncated {
		t.Fatal("first read Truncated = false")
	}

	rest := readNotebookAt(t, path, 2)
	for _, want := range []string{"Notebook cells 2-3 of 3", "secondxxx", "print('last')"} {
		if !strings.Contains(rest.Output, want) {
			t.Fatalf("second read = %.300q..., want it to contain %q", rest.Output, want)
		}
	}
	if strings.Contains(rest.Output, "Continue with") || rest.Truncated {
		t.Fatalf("second read reached the end but still asks to continue: truncated=%v", rest.Truncated)
	}
}

// When the budget runs out between cells, the header and the continuation
// offset describe the cells actually shown.
func TestNotebookReadReportsCellsShown(t *testing.T) {
	workspace := inWorkspace(t)
	path := writeNotebook(t, workspace, "split.ipynb",
		bulkyCellSource("one", 20, 1500),
		bulkyCellSource("two", 20, 1500),
		bulkyCellSource("three", 20, 1500),
	)

	output := readNotebookAt(t, path, 1)
	for _, want := range []string{"Notebook cells 1-1 of 3", "onexxx", "Continue with offset=2"} {
		if !strings.Contains(output.Output, want) {
			t.Fatalf("read = %.300q..., want it to contain %q", output.Output, want)
		}
	}
	if strings.Contains(output.Output, "[cell truncated") {
		t.Fatal("a cell that fits was truncated")
	}
}

func TestIsLikelyBinaryFile(t *testing.T) {
	// 2731 three-byte characters overrun the sample by one byte, so a sample
	// cut at fileReadBinarySampleBytes ends two bytes into a character.
	cjk := []byte(strings.Repeat("中", 2731))[:fileReadBinarySampleBytes]
	emoji := []byte("text 😀")

	cases := []struct {
		name   string
		path   string
		sample []byte
		want   bool
	}{
		{"empty", "a.txt", nil, false},
		{"ascii text", "a.txt", []byte("hello\nworld\n"), false},
		{"multibyte text cut at sample boundary", "a.txt", cjk, false},
		{"emoji cut after first byte", "a.txt", emoji[:len(emoji)-3], false},
		{"complete emoji", "a.txt", emoji, false},
		{"nul byte", "a.txt", []byte("abc\x00def"), true},
		{"invalid utf-8 in the middle", "a.txt", []byte("abc\xffdef"), true},
		{"stray continuation bytes at the end", "a.txt", []byte("abc\x80\x80\x80\x80"), true},
		{"image extension", "a.png", []byte("hello"), true},
		{"notebook", "a.ipynb", []byte("{\"cells\": []}"), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isLikelyBinaryFile(tc.path, tc.sample); got != tc.want {
				t.Fatalf("isLikelyBinaryFile(%q, %d-byte sample) = %v, want %v", tc.path, len(tc.sample), got, tc.want)
			}
		})
	}
}

// A text file larger than the binary-detection sample must stay readable and
// patchable even when the sample boundary falls inside a character.
func TestMultibyteTextLargerThanSampleIsText(t *testing.T) {
	workspace := inWorkspace(t)
	line := strings.Repeat("中", 3000)
	path := writeWorkspaceFile(t, workspace, "cjk.txt", line+"\n")

	output, err := NewFileReadTool().Execute(context.Background(), ToolInput{Params: map[string]any{"filePath": path}})
	if err != nil {
		t.Fatalf("read_file: %v", err)
	}
	if output.IsError {
		t.Fatalf("read_file refused a text file: %s", output.Output)
	}

	patched := runApplyPatch(t, strings.Join([]string{
		"*** Begin Patch",
		"*** Update File: cjk.txt",
		"@@",
		"-" + line,
		"+changed",
		"*** End Patch",
	}, "\n"))
	if patched.IsError {
		t.Fatalf("apply_patch refused a text file: %s", patched.Output)
	}
}
