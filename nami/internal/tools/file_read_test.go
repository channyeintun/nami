package tools

import (
	"context"
	"strings"
	"testing"
)

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
