package agent

import (
	"slices"
	"testing"
)

func TestExtractFilePathMatches(t *testing.T) {
	tests := []struct {
		name string
		text string
		want []string
	}{
		{name: "one path per line", text: "a.go\nb.go\nc.go\nd.go\n", want: []string{"a.go", "b.go", "c.go", "d.go"}},
		{name: "space separated", text: "compare a.go b.go c.go", want: []string{"a.go", "b.go", "c.go"}},
		{name: "comma after a path", text: "Fix the bug in main.go, please", want: []string{"main.go"}},
		{name: "question mark after a path", text: "What is wrong with loop.go?", want: []string{"loop.go"}},
		{name: "sentence end", text: "Update internal/agent/README.md.", want: []string{"internal/agent/README.md"}},
		{name: "compiler location", text: "retrieval.go:12:3: undefined: x", want: []string{"retrieval.go"}},
		{name: "quoted and parenthesized", text: "`query_stream.go` (loop_test.go) \"a/b.ts\"", want: []string{"query_stream.go", "loop_test.go", "a/b.ts"}},
		{name: "longer extension wins", text: "load config.json now", want: []string{"config.json"}},
		{name: "extension prefix of a longer word", text: "a.gopher and b.jsonl", want: nil},
		{name: "inside a URL", text: "see https://example.com/x/main.go", want: nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := extractFilePathMatches(tt.text); !slices.Equal(got, tt.want) {
				t.Fatalf("extractFilePathMatches(%q) = %q, want %q", tt.text, got, tt.want)
			}
		})
	}
}
