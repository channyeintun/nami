package workflow

import (
	"regexp"
	"strings"
)

// outputRefPattern matches a dependency-output placeholder. Deliberately plain
// string substitution rather than text/template: a node prompt is written by a
// model and routinely contains braces, backticks, and JSON, none of which should
// be parsed as markup. Substitution also has to stay byte-stable, because the
// substituted prompt is what the journal hashes for resume.
var outputRefPattern = regexp.MustCompile(`\$\{outputs\.([a-z0-9][a-z0-9_-]*)\}`)

// OutputReferences lists the node ids a prompt interpolates, in first-use order
// and without duplicates.
func OutputReferences(prompt string) []string {
	matches := outputRefPattern.FindAllStringSubmatch(prompt, -1)
	refs := make([]string, 0, len(matches))
	seen := make(map[string]struct{}, len(matches))
	for _, match := range matches {
		id := match[1]
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		refs = append(refs, id)
	}
	return refs
}

// ExpandPrompt replaces every ${outputs.id} with that node's output. Resolve has
// already proven each reference names a declared dependency, so a missing entry
// here means the dependency produced no output; an explicit marker is clearer to
// the node than an empty gap in its instructions.
func ExpandPrompt(prompt string, outputs map[string]string) string {
	return outputRefPattern.ReplaceAllStringFunc(prompt, func(match string) string {
		id := outputRefPattern.FindStringSubmatch(match)[1]
		output, ok := outputs[id]
		if !ok || strings.TrimSpace(output) == "" {
			return "[no output from " + id + "]"
		}
		return output
	})
}
