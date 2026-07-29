package session

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/channyeintun/nami/internal/api"
)

func TestCleanTitleStripsQuotesAndLabels(t *testing.T) {
	cases := map[string]string{
		`"Fix auth token refresh"`: "Fix auth token refresh",
		`'Fix auth'`:               "Fix auth",
		"Title: Fix auth":          "Fix auth",
		"title: Fix auth":          "Fix auth",
		"Title - Fix auth":         "Fix auth",
		"  Fix auth.  ":            "Fix auth",
		"- Fix auth;":              "Fix auth",
	}
	for input, want := range cases {
		if got := cleanTitle(input); got != want {
			t.Errorf("cleanTitle(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestCleanTitleLeavesMismatchedQuotesAlone(t *testing.T) {
	// Only a matched surrounding pair is stripped.
	if got := cleanTitle(`"unterminated`); got != `"unterminated` {
		t.Fatalf("cleanTitle = %q", got)
	}
}

func TestCleanTitleTruncatesWithoutBreakingUTF8(t *testing.T) {
	// Titles are derived from user text, which is frequently not ASCII. A byte
	// slice at the length cap can land mid-rune and emit invalid UTF-8.
	long := strings.Repeat("日", 100)
	got := cleanTitle(long)
	if !utf8.ValidString(got) {
		t.Fatalf("cleanTitle produced invalid UTF-8: %q", got)
	}
}

func TestExtractConversationTextKeepsValidUTF8(t *testing.T) {
	messages := []api.Message{{Role: api.RoleUser, Content: strings.Repeat("日", 5000)}}
	if got := extractConversationText(messages); !utf8.ValidString(got) {
		t.Fatalf("extractConversationText produced invalid UTF-8")
	}
}

func TestDeriveTitleFromTextCapsWordCount(t *testing.T) {
	got := deriveTitleFromText("one two three four five six seven eight nine")
	if want := "one two three four five six seven"; got != want {
		t.Fatalf("deriveTitleFromText = %q, want %q", got, want)
	}
}

func TestDeriveTitleFromTextDropsPunctuation(t *testing.T) {
	got := deriveTitleFromText("fix(auth): token/refresh bug!")
	// Separators collapse to single spaces and stray punctuation is dropped.
	if strings.ContainsAny(got, "():/!") {
		t.Fatalf("deriveTitleFromText = %q, want punctuation removed", got)
	}
	if !strings.Contains(got, "fix") || !strings.Contains(got, "auth") {
		t.Fatalf("deriveTitleFromText = %q, lost content words", got)
	}
}

func TestDeriveTitleFromTextHandlesEmptyAndSymbolOnly(t *testing.T) {
	for _, input := range []string{"", "   ", "!!!", "---"} {
		if got := deriveTitleFromText(input); got != "" {
			t.Errorf("deriveTitleFromText(%q) = %q, want empty", input, got)
		}
	}
}

func TestHeuristicTitlePrefersFirstUsableUserMessage(t *testing.T) {
	messages := []api.Message{
		{Role: api.RoleAssistant, Content: "assistant first"},
		{Role: api.RoleUser, Content: "   "},
		{Role: api.RoleUser, Content: "fix the auth bug"},
	}
	if got := heuristicTitle(messages); got != "fix the auth bug" {
		t.Fatalf("heuristicTitle = %q", got)
	}
}

func TestHeuristicTitleFallsBackToAnyMessage(t *testing.T) {
	messages := []api.Message{{Role: api.RoleAssistant, Content: "only assistant text"}}
	if got := heuristicTitle(messages); got != "only assistant text" {
		t.Fatalf("heuristicTitle = %q", got)
	}
}

func TestHeuristicTitleFallsBackToConstant(t *testing.T) {
	for _, messages := range [][]api.Message{nil, {{Role: api.RoleUser, Content: "!!!"}}} {
		if got := heuristicTitle(messages); got != "Conversation" {
			t.Errorf("heuristicTitle(%+v) = %q, want Conversation", messages, got)
		}
	}
}
