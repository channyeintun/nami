package webfetch

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestParseMode(t *testing.T) {
	cases := map[string]Mode{
		"":          ModeReport,
		"report":    ModeReport,
		"  REPORT ": ModeReport,
		"markdown":  ModeMarkdown,
		"Markdown":  ModeMarkdown,
	}
	for input, want := range cases {
		got, err := ParseMode(input)
		if err != nil {
			t.Errorf("ParseMode(%q): %v", input, err)
			continue
		}
		if got != want {
			t.Errorf("ParseMode(%q) = %q, want %q", input, got, want)
		}
	}
	if _, err := ParseMode("summary"); err == nil {
		t.Error("ParseMode(\"summary\") = nil error, want a rejection")
	}
}

func TestRenderMarkdownMode(t *testing.T) {
	content := Content{Markdown: "# Heading\n\nBody"}
	if got := Render("https://example.com", "anything", ModeMarkdown, content); got != "# Heading\n\nBody" {
		t.Fatalf("Render = %q, want the raw markdown", got)
	}
	if got := Render("https://example.com", "", ModeMarkdown, Content{}); got != "No readable content returned." {
		t.Fatalf("Render on empty content = %q", got)
	}
}

func TestRenderReportIncludesHeaderAndPassages(t *testing.T) {
	content := Content{
		StatusText:  "200 OK",
		ContentType: "text/html",
		Bytes:       128,
		Markdown:    "Unrelated opening line\nThe retry policy uses exponential backoff\nAnother unrelated line",
	}
	got := Render("https://example.com/docs", "retry policy", ModeReport, content)

	for _, want := range []string{
		"Fetched: https://example.com/docs",
		"Status: 200 OK",
		"Content-Type: text/html",
		"Bytes: 128",
		"Prompt: retry policy",
		"Relevant excerpts:",
		"The retry policy uses exponential backoff",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("Render output missing %q:\n%s", want, got)
		}
	}
}

func TestRenderReportFallsBackToContent(t *testing.T) {
	content := Content{StatusText: "200 OK", Markdown: "aa bb"}
	got := Render("https://example.com", "zzz qqq", ModeReport, content)
	if !strings.Contains(got, "Relevant excerpts:") {
		t.Fatalf("Render should fall back to the opening lines:\n%s", got)
	}
}

func TestRenderReportWithoutContent(t *testing.T) {
	got := Render("https://example.com", "prompt", ModeReport, Content{StatusText: "204 No Content"})
	if !strings.Contains(got, "No readable content returned.") {
		t.Fatalf("Render = %q, want the empty-content notice", got)
	}
}

func TestRelevantPassagesRanksByKeywordOverlap(t *testing.T) {
	markdown := strings.Join([]string{
		"nothing to see here",
		"the cache stores markdown",
		"cache eviction uses a policy",
		"unrelated trailing line",
	}, "\n")
	passages := RelevantPassages(markdown, "cache eviction policy")
	if len(passages) != 2 {
		t.Fatalf("passages = %#v, want the two lines that mention keywords", passages)
	}
	if passages[0] != "cache eviction uses a policy" {
		t.Fatalf("best passage = %q, want the highest-overlap line", passages[0])
	}
	for _, passage := range passages {
		if passage == "nothing to see here" {
			t.Fatalf("unrelated line was selected: %#v", passages)
		}
	}
}

func TestRelevantPassagesIsStableForTiedScores(t *testing.T) {
	markdown := "alpha cache line\nbeta cache line\ngamma cache line"
	first := RelevantPassages(markdown, "cache")
	for range 20 {
		if got := RelevantPassages(markdown, "cache"); len(got) != len(first) || got[0] != first[0] || got[1] != first[1] {
			t.Fatalf("ranking changed between runs: %#v vs %#v", got, first)
		}
	}
	if first[0] != "alpha cache line" {
		t.Fatalf("tied scores should keep document order, got %q", first[0])
	}
}

func TestRelevantPassagesWithoutKeywords(t *testing.T) {
	markdown := "one\ntwo\nthree\nfour"
	passages := RelevantPassages(markdown, "a b")
	if len(passages) != 3 || passages[0] != "one" {
		t.Fatalf("passages = %#v, want the first three lines", passages)
	}
	if got := RelevantPassages("", "prompt"); got != nil {
		t.Fatalf("RelevantPassages on empty markdown = %#v, want nil", got)
	}
}

func TestTruncateMarkdownCutsOnRuneBoundary(t *testing.T) {
	short := "already short"
	if got := TruncateMarkdown(short); got != short {
		t.Fatalf("TruncateMarkdown = %q, want it unchanged", got)
	}

	long := strings.Repeat("é", maxMarkdownChars)
	got := TruncateMarkdown(long)
	if !strings.HasSuffix(got, "[Content truncated due to length...]") {
		t.Fatal("TruncateMarkdown did not add the truncation notice")
	}
	if !utf8.ValidString(got) {
		t.Fatal("TruncateMarkdown produced invalid UTF-8")
	}
}

func TestSplitSectionsCollapsesWhitespace(t *testing.T) {
	got := splitSections("  first   line \n\n\t second\tline  \r\n")
	want := []string{"first line", "second line"}
	if len(got) != len(want) {
		t.Fatalf("splitSections = %#v, want %#v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("splitSections = %#v, want %#v", got, want)
		}
	}
}

func TestKeywordSetDropsShortTokens(t *testing.T) {
	keywords := keywordSet("Go is a Fast language, v2!")
	if _, ok := keywords["language"]; !ok {
		t.Error("expected \"language\" in the keyword set")
	}
	if _, ok := keywords["fast"]; !ok {
		t.Error("keywords should be lowercased")
	}
	for _, dropped := range []string{"go", "is", "a", "v2"} {
		if _, ok := keywords[dropped]; ok {
			t.Errorf("short token %q should be dropped", dropped)
		}
	}
}
