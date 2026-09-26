package tools

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestReadLineBoundedKeepsAtMostLimitBytes(t *testing.T) {
	long := strings.Repeat("a", 10_000)
	reader := bufio.NewReaderSize(strings.NewReader("short\n"+long+"\r\nnext\r\nlast"), 16)

	cases := []struct {
		want    string
		wantEOF bool
	}{
		{"short\n", false},
		{long[:32], false}, // the rest of the long line and its newline are skipped
		{"next\r\n", false},
		{"last", true},
		{"", true},
	}
	for index, tc := range cases {
		line, err := readLineBounded(reader, 32)
		if line != tc.want {
			t.Fatalf("line %d = %q, want %q", index, line, tc.want)
		}
		if gotEOF := errors.Is(err, io.EOF); gotEOF != tc.wantEOF || (err != nil && !gotEOF) {
			t.Fatalf("line %d err = %v, want EOF %v", index, err, tc.wantEOF)
		}
	}
}

// One huge line must not be loaded whole just to show its first characters,
// and the lines after it must stay readable.
func TestFileReadClipsHugeLinesWithoutLoadingThem(t *testing.T) {
	workspace := inWorkspace(t)
	huge := "{\"data\": \"" + strings.Repeat("é", 1_000_000) + "\"}"
	path := writeWorkspaceFile(t, workspace, "dump.json", huge+"\nsecond line\n")

	output, err := NewFileReadTool().Execute(context.Background(), ToolInput{Params: map[string]any{"filePath": path}})
	if err != nil {
		t.Fatalf("read_file: %v", err)
	}
	lines := strings.Split(output.Output, "\n")
	if len(lines) != 2 || lines[1] != "2\tsecond line" {
		t.Fatalf("output lines = %q, want the clipped first line and the second line", lines)
	}
	if want := "1\t" + huge[:len("{\"data\": \"")]; !strings.HasPrefix(lines[0], want) || !strings.HasSuffix(lines[0], "...") {
		t.Fatalf("first line = %.80q..., want it clipped with an ellipsis", lines[0])
	}
	if got := utf8.RuneCountInString(strings.TrimPrefix(lines[0], "1\t")); got != fileReadMaxRenderedLineChars {
		t.Fatalf("first line has %d characters, want %d", got, fileReadMaxRenderedLineChars)
	}
	if !output.Truncated {
		t.Fatal("Truncated = false for a clipped line")
	}
}

// Previews are cut to a byte budget; the cut must not split a character.
func TestFileReadPreviewKeepsWholeCharacters(t *testing.T) {
	workspace := inWorkspace(t)
	// "1\tx" puts every two-byte "é" at an odd offset, so byte PreviewChars
	// falls inside one.
	path := writeWorkspaceFile(t, workspace, "accents.txt", "x"+strings.Repeat("é", 1500)+"\n")

	output, err := NewFileReadTool().Execute(context.Background(), ToolInput{Params: map[string]any{"filePath": path}})
	if err != nil {
		t.Fatalf("read_file: %v", err)
	}
	if len(output.Output) <= PreviewChars {
		t.Fatalf("output is %d bytes; the test needs more than %d", len(output.Output), PreviewChars)
	}
	if !utf8.ValidString(output.Preview) {
		t.Fatalf("preview ends with a split character: %q", output.Preview[len(output.Preview)-4:])
	}
	if len(output.Preview) > PreviewChars || len(output.Preview) < PreviewChars-utf8.UTFMax {
		t.Fatalf("preview is %d bytes, want just under %d", len(output.Preview), PreviewChars)
	}
}

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
