// Package workflow runs a workflow as a dependency graph: a set of named nodes,
// each declaring which other nodes must finish before it starts. The engine is
// deliberately free of any agent machinery — a node's work is whatever the
// injected NodeRunner does with it — so it can be reused for any fan-out that
// needs dataflow ordering rather than fixed phases.
package workflow

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
)

// FailurePolicy decides what happens to the rest of the graph when a node fails.
type FailurePolicy string

const (
	// FailureSkipDependents skips only the failed node's transitive dependents
	// and lets independent branches finish. This is the default: a workflow is
	// usually several independent lines of work, and one broken line should not
	// discard the others.
	FailureSkipDependents FailurePolicy = "skip_dependents"
	// FailureAbort stops the whole run at the first failure. Everything not yet
	// started is skipped, including independent branches.
	FailureAbort FailurePolicy = "abort"
)

// DefaultMaxParallel bounds how many nodes run at once when a spec does not say.
const DefaultMaxParallel = 4

// MaxNodes caps graph size. A workflow larger than this is a sign the model is
// enumerating work rather than decomposing it, and the run would cost more than
// it returns.
const MaxNodes = 64

// Spec is the JSON shape a caller submits.
type Spec struct {
	Description   string     `json:"description,omitempty"`
	Nodes         []NodeSpec `json:"nodes"`
	MaxParallel   int        `json:"max_parallel,omitempty"`
	OnNodeFailure string     `json:"on_node_failure,omitempty"`
}

// NodeSpec is one unit of work and the edges into it.
type NodeSpec struct {
	ID          string   `json:"id"`
	Description string   `json:"description"`
	Prompt      string   `json:"prompt"`
	DependsOn   []string `json:"depends_on,omitempty"`
	// Agent carries runner-specific settings verbatim. The graph engine never
	// interprets these; the NodeRunner does.
	Agent AgentSpec `json:"agent,omitzero"`
}

// AgentSpec holds the opaque settings a NodeRunner needs to dispatch a node.
type AgentSpec struct {
	SubagentType      string `json:"subagent_type,omitempty"`
	Role              string `json:"role,omitempty"`
	WorkspaceStrategy string `json:"workspace_strategy,omitempty"`
}

// ResolvedSpec is a validated graph: ids are unique, every dependency exists,
// there are no cycles, and Nodes is in a topological order.
type ResolvedSpec struct {
	Description   string
	MaxParallel   int
	FailurePolicy FailurePolicy
	Nodes         []ResolvedNode
}

// ResolvedNode is a node with both edge directions resolved.
type ResolvedNode struct {
	ID          string
	Description string
	Prompt      string
	DependsOn   []string
	Dependents  []string
	Agent       AgentSpec
}

// ValidationError reports every problem found in one pass so a caller fixing a
// spec sees the whole list instead of one problem per attempt.
type ValidationError struct {
	Problems []string
}

func (e *ValidationError) Error() string {
	if e == nil || len(e.Problems) == 0 {
		return "invalid workflow graph"
	}
	return fmt.Sprintf("invalid workflow graph: %s", strings.Join(e.Problems, "; "))
}

var nodeIDPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`)

// Resolve validates the spec and returns it in topological order.
func (s Spec) Resolve() (ResolvedSpec, error) {
	resolved := ResolvedSpec{
		Description:   strings.TrimSpace(s.Description),
		MaxParallel:   s.MaxParallel,
		FailurePolicy: normalizeFailurePolicy(s.OnNodeFailure),
	}

	var problems []string
	if resolved.FailurePolicy == "" {
		problems = append(problems, fmt.Sprintf("on_node_failure %q is invalid; expected %q or %q", s.OnNodeFailure, FailureSkipDependents, FailureAbort))
	}
	if resolved.MaxParallel < 0 {
		problems = append(problems, fmt.Sprintf("max_parallel %d must not be negative", resolved.MaxParallel))
	}
	if resolved.MaxParallel == 0 {
		resolved.MaxParallel = DefaultMaxParallel
	}
	if len(s.Nodes) == 0 {
		problems = append(problems, "nodes must contain at least one node")
	}
	if len(s.Nodes) > MaxNodes {
		problems = append(problems, fmt.Sprintf("nodes contains %d entries, which exceeds the %d node limit", len(s.Nodes), MaxNodes))
	}

	nodes, index, nodeProblems := resolveNodes(s.Nodes)
	problems = append(problems, nodeProblems...)

	if len(problems) == 0 {
		problems = append(problems, validateEdges(nodes, index)...)
	}
	if len(problems) == 0 {
		ordered, cycle := topologicalOrder(nodes, index)
		if len(cycle) > 0 {
			problems = append(problems, fmt.Sprintf("nodes form a dependency cycle: %s", strings.Join(cycle, " -> ")))
		} else {
			nodes = ordered
		}
	}

	if len(problems) > 0 {
		return ResolvedSpec{}, &ValidationError{Problems: problems}
	}

	resolved.Nodes = nodes
	return resolved, nil
}

func resolveNodes(specs []NodeSpec) ([]ResolvedNode, map[string]int, []string) {
	var problems []string
	nodes := make([]ResolvedNode, 0, len(specs))
	index := make(map[string]int, len(specs))

	for i, spec := range specs {
		display := i + 1
		id := strings.ToLower(strings.TrimSpace(spec.ID))
		switch {
		case id == "":
			problems = append(problems, fmt.Sprintf("node[%d] id is required", display))
			continue
		case !nodeIDPattern.MatchString(id):
			problems = append(problems, fmt.Sprintf("node[%d] id %q must match %s", display, spec.ID, nodeIDPattern.String()))
			continue
		}
		if existing, ok := index[id]; ok {
			problems = append(problems, fmt.Sprintf("node[%d] id %q duplicates node[%d]", display, id, existing+1))
			continue
		}

		prompt := strings.TrimSpace(spec.Prompt)
		if prompt == "" {
			problems = append(problems, fmt.Sprintf("node[%d] %q prompt is required", display, id))
		}
		description := strings.TrimSpace(spec.Description)
		if description == "" {
			problems = append(problems, fmt.Sprintf("node[%d] %q description is required", display, id))
		}

		dependsOn, depProblems := normalizeDependencies(spec.DependsOn, id, display)
		problems = append(problems, depProblems...)

		index[id] = len(nodes)
		nodes = append(nodes, ResolvedNode{
			ID:          id,
			Description: description,
			Prompt:      prompt,
			DependsOn:   dependsOn,
			Agent: AgentSpec{
				SubagentType:      strings.TrimSpace(spec.Agent.SubagentType),
				Role:              strings.TrimSpace(spec.Agent.Role),
				WorkspaceStrategy: strings.TrimSpace(spec.Agent.WorkspaceStrategy),
			},
		})
	}

	return nodes, index, problems
}

func normalizeDependencies(raw []string, id string, display int) ([]string, []string) {
	var problems []string
	dependsOn := make([]string, 0, len(raw))
	for _, dep := range raw {
		dep = strings.ToLower(strings.TrimSpace(dep))
		switch {
		case dep == "":
			problems = append(problems, fmt.Sprintf("node[%d] %q depends_on cannot contain empty entries", display, id))
		case dep == id:
			problems = append(problems, fmt.Sprintf("node[%d] %q cannot depend on itself", display, id))
		case slices.Contains(dependsOn, dep):
			problems = append(problems, fmt.Sprintf("node[%d] %q depends_on lists %q twice", display, id, dep))
		default:
			dependsOn = append(dependsOn, dep)
		}
	}
	return dependsOn, problems
}

// validateEdges checks that dependencies exist and that every ${outputs.x}
// reference names a declared dependency. Catching the reference here means a
// prompt can never reach a node with an unsubstituted placeholder in it.
func validateEdges(nodes []ResolvedNode, index map[string]int) []string {
	var problems []string
	for i, node := range nodes {
		display := i + 1
		for _, dep := range node.DependsOn {
			if _, ok := index[dep]; !ok {
				problems = append(problems, fmt.Sprintf("node[%d] %q depends on %q, which is not defined", display, node.ID, dep))
			}
		}
		for _, ref := range OutputReferences(node.Prompt) {
			if _, ok := index[ref]; !ok {
				problems = append(problems, fmt.Sprintf("node[%d] %q references ${outputs.%s}, which is not defined", display, node.ID, ref))
				continue
			}
			if !slices.Contains(node.DependsOn, ref) {
				problems = append(problems, fmt.Sprintf("node[%d] %q references ${outputs.%s} without listing %q in depends_on", display, node.ID, ref, ref))
			}
		}
	}
	return problems
}

func normalizeFailurePolicy(raw string) FailurePolicy {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", string(FailureSkipDependents), "skip-dependents":
		return FailureSkipDependents
	case string(FailureAbort):
		return FailureAbort
	default:
		return ""
	}
}

// Node returns the resolved node with the given id.
func (s ResolvedSpec) Node(id string) (ResolvedNode, bool) {
	id = strings.ToLower(strings.TrimSpace(id))
	for _, node := range s.Nodes {
		if node.ID == id {
			return node, true
		}
	}
	return ResolvedNode{}, false
}

// Roots returns the ids of nodes with no dependencies, in graph order.
func (s ResolvedSpec) Roots() []string {
	roots := make([]string, 0, len(s.Nodes))
	for _, node := range s.Nodes {
		if len(node.DependsOn) == 0 {
			roots = append(roots, node.ID)
		}
	}
	return roots
}
