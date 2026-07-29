package tools

import (
	"slices"
	"testing"
)

func TestParseGoModDependenciesSplitsDirectAndIndirect(t *testing.T) {
	sections := parseGoModDependencies(`module example.com/x

go 1.26

// a comment
require github.com/single/dep v1.2.3

require (
	github.com/direct/one v1.0.0
	github.com/indirect/two v2.0.0 // indirect
)
`)

	if got := sections["require"]; !slices.Contains(got, "github.com/single/dep") {
		t.Errorf("single-line require missing: %v", got)
	}
	if got := sections["require"]; !slices.Contains(got, "github.com/direct/one") {
		t.Errorf("block require missing: %v", got)
	}
	// The indirect marker routes the module to its own section.
	if got := sections["require_indirect"]; !slices.Contains(got, "github.com/indirect/two") {
		t.Errorf("indirect require missing: %v", got)
	}
	if got := sections["require"]; slices.Contains(got, "github.com/indirect/two") {
		t.Errorf("indirect module leaked into direct requires: %v", got)
	}
}

func TestParseGoModDependenciesIgnoresCommentsAndBlanks(t *testing.T) {
	sections := parseGoModDependencies("// require github.com/commented/out v1.0.0\n\n")
	if len(sections["require"]) != 0 {
		t.Fatalf("commented require was parsed: %v", sections["require"])
	}
}

func TestParseGoModRequireLineTakesTheModulePath(t *testing.T) {
	if got := parseGoModRequireLine("github.com/a/b v1.2.3 // indirect"); got != "github.com/a/b" {
		t.Errorf("parseGoModRequireLine = %q", got)
	}
	if got := parseGoModRequireLine("   "); got != "" {
		t.Errorf("parseGoModRequireLine(blank) = %q, want empty", got)
	}
}

func TestParsePackageJSONDependenciesSeparatesSections(t *testing.T) {
	sections := parsePackageJSONDependencies(`{
		"dependencies": {"react": "^19.0.0"},
		"devDependencies": {"typescript": "^5.0.0"},
		"peerDependencies": {"react-dom": "^19.0.0"},
		"optionalDependencies": {"fsevents": "^2.0.0"}
	}`)

	for section, want := range map[string]string{
		"dependencies":         "react",
		"devDependencies":      "typescript",
		"peerDependencies":     "react-dom",
		"optionalDependencies": "fsevents",
	} {
		if !slices.Contains(sections[section], want) {
			t.Errorf("%s missing %q: %v", section, want, sections[section])
		}
	}
}

func TestParsePackageJSONDependenciesRejectsMalformedJSON(t *testing.T) {
	if got := parsePackageJSONDependencies("{not json"); got != nil {
		t.Fatalf("parsePackageJSONDependencies = %v, want nil on malformed input", got)
	}
}

func TestParsePyProjectDependenciesHandlesInlineAndMultilineArrays(t *testing.T) {
	sections := parsePyProjectDependencies(`[project]
name = "x"
dependencies = ["requests>=2.0", "urllib3"]

[project.optional-dependencies]
dev = [
  "pytest>=8.0",
  "ruff",
]
`)

	deps := sections["project.dependencies"]
	// The PEP 508 version specifier is stripped down to the bare name.
	if !slices.Contains(deps, "requests") || !slices.Contains(deps, "urllib3") {
		t.Errorf("project.dependencies = %v", deps)
	}

	// A bracket left open continues accumulating until the closing bracket.
	dev := sections["project.optional-dependencies.dev"]
	if !slices.Contains(dev, "pytest") || !slices.Contains(dev, "ruff") {
		t.Errorf("optional dev dependencies = %v", dev)
	}
}

func TestParsePyProjectDependenciesIgnoresUnrelatedSections(t *testing.T) {
	sections := parsePyProjectDependencies(`[tool.ruff]
dependencies = ["not-a-real-dependency"]
`)
	if len(sections) != 0 {
		t.Fatalf("unrelated section produced %v", sections)
	}
}

func TestParseRequirementsDependenciesStripsSpecifiersAndDirectives(t *testing.T) {
	sections := parseRequirementsDependencies(`# a comment
requests>=2.0
urllib3 == 1.26  # trailing comment
-r other-requirements.txt
--index-url https://example.com

`)

	deps := sections["requirements"]
	if !slices.Contains(deps, "requests") || !slices.Contains(deps, "urllib3") {
		t.Errorf("requirements = %v", deps)
	}
	// pip directives are options, not packages.
	for _, unwanted := range []string{"-r", "--index-url"} {
		if slices.Contains(deps, unwanted) {
			t.Errorf("directive %q parsed as a dependency: %v", unwanted, deps)
		}
	}
}

func TestParseCargoDependenciesOnlyReadsDependencySections(t *testing.T) {
	sections := parseCargoDependencies(`[package]
name = "x"

[dependencies]
serde = "1.0"
tokio = { version = "1", features = ["full"] }

[dev-dependencies]
criterion = "0.5"
`)

	if !slices.Contains(sections["dependencies"], "serde") {
		t.Errorf("dependencies = %v", sections["dependencies"])
	}
	if !slices.Contains(sections["dependencies"], "tokio") {
		t.Errorf("inline-table dependency missing: %v", sections["dependencies"])
	}
	if !slices.Contains(sections["dev-dependencies"], "criterion") {
		t.Errorf("dev-dependencies = %v", sections["dev-dependencies"])
	}
	// [package] is not a dependency section, so its keys are skipped.
	if slices.Contains(sections["package"], "name") {
		t.Errorf("package metadata parsed as dependencies: %v", sections["package"])
	}
}

func TestParseGemfileDependenciesExtractsGemNames(t *testing.T) {
	sections := parseGemfileDependencies(`source "https://rubygems.org"
# a comment
gem "rails", "~> 7.0"
gem 'puma'
`)

	gems := sections["gem"]
	if !slices.Contains(gems, "rails") || !slices.Contains(gems, "puma") {
		t.Errorf("gems = %v", gems)
	}
	if slices.Contains(gems, "https://rubygems.org") {
		t.Errorf("source line parsed as a gem: %v", gems)
	}
}

func TestSplitTomlAssignment(t *testing.T) {
	key, value, ok := splitTomlAssignment(`serde = "1.0"`)
	if !ok || key != "serde" || value != `"1.0"` {
		t.Fatalf("splitTomlAssignment = (%q, %q, %v)", key, value, ok)
	}
	// The value may itself contain "=", so only the first one splits.
	key, value, ok = splitTomlAssignment(`flags = "a=b"`)
	if !ok || key != "flags" || value != `"a=b"` {
		t.Fatalf("splitTomlAssignment = (%q, %q, %v)", key, value, ok)
	}
	if _, _, ok := splitTomlAssignment("no assignment here"); ok {
		t.Fatal("splitTomlAssignment accepted a line with no =")
	}
}

func TestParseQuotedStringsHandlesBothQuoteStyles(t *testing.T) {
	got := parseQuotedStrings(`["a", 'b', "", "c"]`)
	want := []string{"a", "b", "c"}
	if !slices.Equal(got, want) {
		t.Fatalf("parseQuotedStrings = %#v, want %#v", got, want)
	}
}

func TestNormalizePythonDependencyNameStripsSpecifiers(t *testing.T) {
	cases := map[string]string{
		"requests":           "requests",
		"requests>=2.0":      "requests",
		"requests[security]": "requests",
		"ruff == 0.1.0":      "ruff",
		"  urllib3  ":        "urllib3",
		"":                   "",
	}
	for input, want := range cases {
		if got := normalizePythonDependencyName(input); got != want {
			t.Errorf("normalizePythonDependencyName(%q) = %q, want %q", input, got, want)
		}
	}
}
