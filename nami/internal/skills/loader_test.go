package skills

import "testing"

// Keywords and skill names match whole words. Substring matching made the
// built-in Go guide's "go" keyword fire on "Google", "good", "algorithm" and
// "cargo", injecting it into turns that had nothing to do with Go.
func TestSelectRelevantMatchesWholeWords(t *testing.T) {
	goGuide := Skill{
		Name:        "go-style-guide",
		Description: "Guidance for Go coding tasks",
		Keywords:    []string{"golang", "go", "go concurrency", "errors.AsType"},
	}
	cases := []struct {
		prompt string
		want   bool
	}{
		{"port this script to Go please", true},
		{"Go: why does this code deadlock?", true},
		{"swap errors.As for errors.AsType here", true},
		{"explain go concurrency patterns", true},
		{"run /go-style-guide over the diff", true},
		{"Fix the Google login bug", false},
		{"Is this a good algorithm for sorting?", false},
		{"Refactor the cargo build script", false},
		{"make the logo bigger on mongodb pages", false},
	}
	for _, tc := range cases {
		t.Run(tc.prompt, func(t *testing.T) {
			selected := SelectRelevant([]Skill{goGuide}, tc.prompt)
			if got := len(selected) == 1; got != tc.want {
				t.Fatalf("SelectRelevant(%q) selected=%v, want %v", tc.prompt, got, tc.want)
			}
		})
	}
}

func TestSelectRelevantMatchesSkillNameAsWholeWord(t *testing.T) {
	apiSkill := Skill{Name: "api", Description: "Conventions for handlers"}
	if selected := SelectRelevant([]Skill{apiSkill}, "try rapid prototyping instead"); len(selected) != 0 {
		t.Fatalf("skill %q was selected by a word that only contains its name", apiSkill.Name)
	}
	if selected := SelectRelevant([]Skill{apiSkill}, "update the api client"); len(selected) != 1 {
		t.Fatalf("skill %q was not selected by its own name", apiSkill.Name)
	}
}

func TestContainsPhrase(t *testing.T) {
	cases := []struct {
		text   string
		phrase string
		want   bool
	}{
		{"write it in go", "go", true},
		{"go.mod is stale", "go", true},
		{"(go) or rust", "go", true},
		{"google", "go", false},
		{"ago", "go", false},
		{"cargo and go", "go", true},
		{"i use .net daily", ".net", true},
		{"asp.net core", ".net", true},
		{"c++17 features", "c++", true},
		{"objc++ bridge", "c++", false},
		{"idiomatic go code", "idiomatic go", true},
		{"idiomatic gopher", "idiomatic go", false},
		{"日本語のテスト", "テスト", true},
		{"テストケースを書く", "テスト", true},
		{"anything", "", false},
		{"", "go", false},
	}
	for _, tc := range cases {
		if got := containsPhrase(tc.text, tc.phrase); got != tc.want {
			t.Errorf("containsPhrase(%q, %q) = %v, want %v", tc.text, tc.phrase, got, tc.want)
		}
	}
}
