package engine

import (
	"strings"
	"testing"

	"github.com/channyeintun/nami/internal/agent"
	"github.com/channyeintun/nami/internal/api"
)

func TestNormalizeProviderDefaultsToAnthropic(t *testing.T) {
	for _, input := range []string{"", "   "} {
		if got := normalizeProvider(input); got != "anthropic" {
			t.Errorf("normalizeProvider(%q) = %q, want anthropic", input, got)
		}
	}
	if got := normalizeProvider("openai"); got != "openai" {
		t.Errorf("normalizeProvider = %q, want the explicit provider preserved", got)
	}
}

func TestParseExecutionModeFallsBackToFast(t *testing.T) {
	if got := parseExecutionMode("plan"); got != agent.ModePlan {
		t.Errorf("parseExecutionMode(plan) = %v", got)
	}
	// Case and padding are tolerated.
	if got := parseExecutionMode("  PLAN  "); got != agent.ModePlan {
		t.Errorf("parseExecutionMode(padded) = %v", got)
	}
	for _, input := range []string{"", "fast", "nonsense"} {
		if got := parseExecutionMode(input); got != agent.ModeFast {
			t.Errorf("parseExecutionMode(%q) = %v, want fast", input, got)
		}
	}
}

func TestMergeUsageAccumulatesEveryCounter(t *testing.T) {
	current := api.Usage{InputTokens: 10, OutputTokens: 20, CacheReadTokens: 30, CacheCreationTokens: 40}
	next := api.Usage{InputTokens: 1, OutputTokens: 2, CacheReadTokens: 3, CacheCreationTokens: 4}

	got := mergeUsage(current, next)
	want := api.Usage{InputTokens: 11, OutputTokens: 22, CacheReadTokens: 33, CacheCreationTokens: 44}
	if got != want {
		t.Fatalf("mergeUsage = %+v, want %+v", got, want)
	}
}

func TestMergeUsageDoesNotMutateInputs(t *testing.T) {
	current := api.Usage{InputTokens: 10}
	next := api.Usage{InputTokens: 5}
	mergeUsage(current, next)

	// Usage is passed by value; a caller accumulating in a loop relies on the
	// originals staying put.
	if current.InputTokens != 10 || next.InputTokens != 5 {
		t.Fatalf("mergeUsage mutated its arguments: %+v %+v", current, next)
	}
}

func TestTruncateOutputPreviewClampsPreviewLength(t *testing.T) {
	output := "hello world"

	// A preview longer than the output must not slice out of range.
	got := truncateOutputPreview(output, 500, "", len(output))
	if !strings.HasPrefix(got, output) {
		t.Fatalf("oversized preview = %q", got)
	}
	// Zero or negative means "no limit" rather than an empty preview.
	for _, previewLen := range []int{0, -1} {
		got := truncateOutputPreview(output, previewLen, "", len(output))
		if !strings.HasPrefix(got, output) {
			t.Fatalf("previewLen %d = %q", previewLen, got)
		}
	}
}

func TestTruncateOutputPreviewMentionsArtifactWhenSpilled(t *testing.T) {
	got := truncateOutputPreview("hello world", 5, "/tmp/tool-log/a.md", 11)
	if !strings.HasPrefix(got, "hello") {
		t.Fatalf("preview = %q", got)
	}
	if !strings.Contains(got, "/tmp/tool-log/a.md") {
		t.Fatalf("artifact path missing from %q", got)
	}
	if !strings.Contains(got, "11") {
		t.Fatalf("total char count missing from %q", got)
	}
}

func TestTruncateOutputPreviewOmitsArtifactWhenNotSpilled(t *testing.T) {
	got := truncateOutputPreview("hello world", 5, "", 11)
	if strings.Contains(got, "artifact saved") {
		t.Fatalf("preview mentions an artifact that does not exist: %q", got)
	}
	if !strings.Contains(got, "truncated") {
		t.Fatalf("preview does not say it was truncated: %q", got)
	}
}

func TestSanitizeWorktreeNameStripsPathTraversal(t *testing.T) {
	// Worktree names become directory names, so no separator or dot segment may
	// survive into the result.
	for _, input := range []string{"../../etc", "..", "/", "./x", `a\b`} {
		got := sanitizeWorktreeName(input)
		for _, forbidden := range []string{"/", `\`, ".."} {
			if strings.Contains(got, forbidden) {
				t.Errorf("sanitizeWorktreeName(%q) = %q, still contains %q", input, got, forbidden)
			}
		}
		if got == "" {
			t.Errorf("sanitizeWorktreeName(%q) = empty", input)
		}
	}
}

func TestSanitizeWorktreeNameNormalizesCase(t *testing.T) {
	if got := sanitizeWorktreeName("  Feature/Auth Fix  "); got != "feature-auth-fix" {
		t.Fatalf("sanitizeWorktreeName = %q, want feature-auth-fix", got)
	}
}

func TestSanitizeWorktreeNameFallsBackForEmptyResults(t *testing.T) {
	for _, input := range []string{"", "   ", "...", "///", "---"} {
		if got := sanitizeWorktreeName(input); got != "worktree" {
			t.Errorf("sanitizeWorktreeName(%q) = %q, want the worktree fallback", input, got)
		}
	}
}

func TestSanitizeWorktreeNameKeepsSafeCharacters(t *testing.T) {
	if got := sanitizeWorktreeName("fix_auth-123"); got != "fix_auth-123" {
		t.Fatalf("sanitizeWorktreeName = %q, want the input preserved", got)
	}
}
