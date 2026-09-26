package workflow

import (
	"errors"
	"slices"
	"strings"
	"testing"
)

func node(id string, deps ...string) NodeSpec {
	return NodeSpec{
		ID:          id,
		Description: "run " + id,
		Prompt:      "do " + id,
		DependsOn:   deps,
	}
}

func TestResolveOrdersNodesTopologically(t *testing.T) {
	spec := Spec{Nodes: []NodeSpec{
		node("publish", "test", "lint"),
		node("test", "build"),
		node("lint", "build"),
		node("build"),
	}}

	resolved, err := spec.Resolve()
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	order := make([]string, len(resolved.Nodes))
	position := make(map[string]int, len(resolved.Nodes))
	for i, n := range resolved.Nodes {
		order[i] = n.ID
		position[n.ID] = i
	}
	for _, n := range resolved.Nodes {
		for _, dep := range n.DependsOn {
			if position[dep] > position[n.ID] {
				t.Fatalf("dependency %q ordered after dependent %q in %v", dep, n.ID, order)
			}
		}
	}
	if order[0] != "build" {
		t.Fatalf("expected build first, got %v", order)
	}
}

func TestResolveIsDeterministic(t *testing.T) {
	spec := Spec{Nodes: []NodeSpec{node("a"), node("b"), node("c"), node("d", "a", "b")}}

	first, err := spec.Resolve()
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	for range 10 {
		next, err := spec.Resolve()
		if err != nil {
			t.Fatalf("Resolve: %v", err)
		}
		for i := range first.Nodes {
			if first.Nodes[i].ID != next.Nodes[i].ID {
				t.Fatalf("order drifted: %s vs %s at %d", first.Nodes[i].ID, next.Nodes[i].ID, i)
			}
		}
	}
}

func TestResolveRecordsDependents(t *testing.T) {
	spec := Spec{Nodes: []NodeSpec{node("root"), node("left", "root"), node("right", "root")}}

	resolved, err := spec.Resolve()
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	root, ok := resolved.Node("root")
	if !ok {
		t.Fatal("root missing")
	}
	if !slices.Contains(root.Dependents, "left") || !slices.Contains(root.Dependents, "right") {
		t.Fatalf("dependents = %v, want left and right", root.Dependents)
	}
	if roots := resolved.Roots(); len(roots) != 1 || roots[0] != "root" {
		t.Fatalf("Roots() = %v, want [root]", roots)
	}
}

func TestResolveRejectsCyclesAndNamesThem(t *testing.T) {
	spec := Spec{Nodes: []NodeSpec{node("a", "c"), node("b", "a"), node("c", "b")}}

	_, err := spec.Resolve()
	validation, ok := errors.AsType[*ValidationError](err)
	if !ok {
		t.Fatalf("expected ValidationError, got %v", err)
	}
	joined := strings.Join(validation.Problems, "; ")
	if !strings.Contains(joined, "cycle") {
		t.Fatalf("expected a cycle problem, got %q", joined)
	}
	for _, id := range []string{"a", "b", "c"} {
		if !strings.Contains(joined, id) {
			t.Fatalf("cycle report %q does not name %q", joined, id)
		}
	}
}

func TestResolveCollectsEveryProblem(t *testing.T) {
	spec := Spec{Nodes: []NodeSpec{
		{ID: "Bad Id", Description: "d", Prompt: "p"},
		{ID: "empty"},
		node("dup"),
		node("dup"),
		node("self", "self"),
		node("dangling", "nowhere"),
	}}

	_, err := spec.Resolve()
	validation, ok := errors.AsType[*ValidationError](err)
	if !ok {
		t.Fatalf("expected ValidationError, got %v", err)
	}
	joined := strings.Join(validation.Problems, "; ")
	for _, want := range []string{"must match", "prompt is required", "description is required", "duplicates", "cannot depend on itself"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("problems %q missing %q", joined, want)
		}
	}
}

func TestResolveRejectsUndeclaredOutputReference(t *testing.T) {
	spec := Spec{Nodes: []NodeSpec{
		node("first"),
		{ID: "second", Description: "d", Prompt: "use ${outputs.first}"},
	}}

	_, err := spec.Resolve()
	validation, ok := errors.AsType[*ValidationError](err)
	if !ok {
		t.Fatalf("expected ValidationError, got %v", err)
	}
	if joined := strings.Join(validation.Problems, "; "); !strings.Contains(joined, "without listing") {
		t.Fatalf("expected a depends_on problem, got %q", joined)
	}
}

func TestResolveAcceptsDeclaredOutputReference(t *testing.T) {
	spec := Spec{Nodes: []NodeSpec{
		node("first"),
		{ID: "second", Description: "d", Prompt: "use ${outputs.first}", DependsOn: []string{"first"}},
	}}

	if _, err := spec.Resolve(); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
}

// Node ids are case-insensitive, so a reference spelled with the id's original
// capitals must be validated like any other — otherwise it slips past both the
// dependency check and substitution and reaches the node as a raw placeholder.
func TestResolveValidatesMixedCaseOutputReferences(t *testing.T) {
	spec := Spec{Nodes: []NodeSpec{
		node("fetchData"),
		{ID: "report", Description: "d", Prompt: "use ${outputs.fetchData}"},
	}}

	_, err := spec.Resolve()
	validation, ok := errors.AsType[*ValidationError](err)
	if !ok {
		t.Fatalf("expected ValidationError for an undeclared mixed-case reference, got %v", err)
	}
	if joined := strings.Join(validation.Problems, "; "); !strings.Contains(joined, `without listing "fetchdata"`) {
		t.Fatalf("expected a depends_on problem naming fetchdata, got %q", joined)
	}
}

func TestResolveRejectsUnknownFailurePolicy(t *testing.T) {
	spec := Spec{Nodes: []NodeSpec{node("a")}, OnNodeFailure: "explode"}

	_, err := spec.Resolve()
	if _, ok := errors.AsType[*ValidationError](err); !ok {
		t.Fatalf("expected ValidationError, got %v", err)
	}
}

func TestResolveDefaultsPolicyAndParallelism(t *testing.T) {
	resolved, err := Spec{Nodes: []NodeSpec{node("a")}}.Resolve()
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if resolved.FailurePolicy != FailureSkipDependents {
		t.Fatalf("FailurePolicy = %q, want %q", resolved.FailurePolicy, FailureSkipDependents)
	}
	if resolved.MaxParallel != DefaultMaxParallel {
		t.Fatalf("MaxParallel = %d, want %d", resolved.MaxParallel, DefaultMaxParallel)
	}
}

func TestOutputReferencesDedupesInOrder(t *testing.T) {
	refs := OutputReferences("${outputs.b} then ${outputs.a} then ${outputs.b}")
	if !slices.Equal(refs, []string{"b", "a"}) {
		t.Fatalf("OutputReferences = %v, want [b a]", refs)
	}
}

func TestOutputReferencesNormalizesCase(t *testing.T) {
	refs := OutputReferences("${outputs.Build} and ${outputs.build} and ${outputs.TEST_1}")
	if !slices.Equal(refs, []string{"build", "test_1"}) {
		t.Fatalf("OutputReferences = %v, want [build test_1]", refs)
	}
}

func TestExpandPromptMarksMissingOutput(t *testing.T) {
	expanded := ExpandPrompt("before ${outputs.a} after", map[string]string{"a": "  "})
	if expanded != "before [no output from a] after" {
		t.Fatalf("ExpandPrompt = %q", expanded)
	}
	if got := ExpandPrompt("x ${outputs.a}", map[string]string{"a": "Y"}); got != "x Y" {
		t.Fatalf("ExpandPrompt = %q", got)
	}
}
