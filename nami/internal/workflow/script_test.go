package workflow

import (
	"strings"
	"testing"
)

func TestParseScriptReadsMeta(t *testing.T) {
	script, err := ParseScript("review.js", `// leading comment
/* and a block comment */
export const meta = {
  name: 'review-changes',
  description: "Review the diff",
  whenToUse: `+"`after a change`"+`,
  phases: [{ title: 'Find', detail: 'one agent per area' }, { title: 'Verify', model: 'anthropic/claude-haiku-4-5' }],
}
return await agent('look')
`)
	if err != nil {
		t.Fatalf("ParseScript() error = %v", err)
	}
	meta := script.Meta
	if meta.Name != "review-changes" || meta.Description != "Review the diff" || meta.WhenToUse != "after a change" {
		t.Fatalf("meta = %+v", meta)
	}
	if len(meta.Phases) != 2 || meta.Phases[0].Title != "Find" || meta.Phases[0].Detail != "one agent per area" || meta.Phases[1].Model != "anthropic/claude-haiku-4-5" {
		t.Fatalf("phases = %+v", meta.Phases)
	}
}

// Meta is read without running the script, so anything that needs the script
// to run has to be rejected with a message that says why.
func TestParseScriptRejectsMetaThatIsNotAPureLiteral(t *testing.T) {
	cases := map[string]string{
		"variable":      "const n = 'x'\nexport const meta = { name: n, description: 'd' }",
		"reference":     "export const meta = { name: NAME, description: 'd' }",
		"call":          "export const meta = { name: String('x'), description: 'd' }",
		"spread":        "export const meta = { ...base, name: 'x', description: 'd' }",
		"interpolation": "export const meta = { name: `x${1}`, description: 'd' }",
		"computed key":  "export const meta = { ['name']: 'x', description: 'd' }",
		"shorthand":     "export const meta = { name, description: 'd' }",
	}
	for name, source := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := ParseScript("bad.js", source)
			if err == nil {
				t.Fatal("ParseScript() succeeded, want an error")
			}
		})
	}
}

func TestParseScriptRequiresMetaFirst(t *testing.T) {
	cases := map[string]string{
		"missing":       "await agent('x')",
		"not first":     "const x = 1\nexport const meta = { name: 'a', description: 'b' }",
		"not exported":  "const meta = { name: 'a', description: 'b' }",
		"let not const": "export let meta = { name: 'a', description: 'b' }",
		"empty":         "   \n  ",
	}
	for name, source := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseScript("bad.js", source); err == nil {
				t.Fatal("ParseScript() succeeded, want an error")
			}
		})
	}
}

func TestParseScriptReportsEveryMetaProblem(t *testing.T) {
	_, err := ParseScript("bad.js", "export const meta = { name: 'has spaces', phases: [{ detail: 'x' }], colour: 'red' }")
	if err == nil {
		t.Fatal("ParseScript() succeeded, want an error")
	}
	for _, want := range []string{"meta.name", "meta.description is required", "meta.phases[0].title is required", "meta.colour"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err, want)
		}
	}
}

// Line numbers in syntax errors have to match the script as written, which is
// why the wrapper shares the body's first line.
func TestParseScriptSyntaxErrorsKeepTheirLineNumbers(t *testing.T) {
	_, err := ParseScript("broken.js", "export const meta = { name: 'a', description: 'b' }\nconst ok = 1\nconst broken = ;\nconst after = 2\n")
	if err == nil {
		t.Fatal("ParseScript() succeeded, want a syntax error")
	}
	if !strings.Contains(err.Error(), "broken.js") || !strings.Contains(err.Error(), "Line 3") {
		t.Fatalf("error = %q, want it to name broken.js line 3", err)
	}
}

// Scripts are plain JavaScript; a TypeScript annotation is the most common way
// a model gets that wrong, and it must fail before anything runs.
func TestParseScriptRejectsTypeScript(t *testing.T) {
	_, err := ParseScript("ts.js", "export const meta = { name: 'a', description: 'b' }\nconst items: string[] = []\n")
	if err == nil {
		t.Fatal("ParseScript() accepted a type annotation")
	}
}

func TestParseScriptAllowsTopLevelAwaitAndReturn(t *testing.T) {
	_, err := ParseScript("ok.js", "export const meta = { name: 'a', description: 'b' }\nconst x = await agent('x')\nreturn x\n")
	if err != nil {
		t.Fatalf("ParseScript() error = %v", err)
	}
}
