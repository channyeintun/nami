package workflow

import (
	"slices"
	"sort"
)

// topologicalOrder returns the nodes in dependency order and fills in each
// node's Dependents. When the graph has a cycle it returns the cycle's node ids
// instead, so the error can name the nodes involved rather than just reporting
// that one exists.
func topologicalOrder(nodes []ResolvedNode, index map[string]int) ([]ResolvedNode, []string) {
	dependents := make([][]string, len(nodes))
	remaining := make([]int, len(nodes))
	for i, node := range nodes {
		remaining[i] = len(node.DependsOn)
		for _, dep := range node.DependsOn {
			depIdx := index[dep]
			dependents[depIdx] = append(dependents[depIdx], node.ID)
		}
	}

	// Ready nodes are kept sorted by their original position so a graph with
	// several valid orders always resolves to the same one. Deterministic order
	// keeps run transcripts and tests comparable between runs.
	ready := make([]int, 0, len(nodes))
	for i := range nodes {
		if remaining[i] == 0 {
			ready = append(ready, i)
		}
	}

	ordered := make([]ResolvedNode, 0, len(nodes))
	for len(ready) > 0 {
		sort.Ints(ready)
		next := ready[0]
		ready = ready[1:]

		node := nodes[next]
		node.Dependents = dependents[next]
		ordered = append(ordered, node)

		for _, dependentID := range dependents[next] {
			dependentIdx := index[dependentID]
			remaining[dependentIdx]--
			if remaining[dependentIdx] == 0 {
				ready = append(ready, dependentIdx)
			}
		}
	}

	if len(ordered) != len(nodes) {
		return nil, cycleNodes(nodes, remaining, index)
	}
	return ordered, nil
}

// cycleNodes walks the unresolved remainder to name one concrete cycle. Naming a
// single cycle is more useful than listing every node still blocked, most of
// which are only blocked because they sit downstream of the real problem.
func cycleNodes(nodes []ResolvedNode, remaining []int, index map[string]int) []string {
	start := -1
	for i := range nodes {
		if remaining[i] > 0 {
			start = i
			break
		}
	}
	if start < 0 {
		return nil
	}

	path := make([]string, 0, len(nodes))
	seen := make(map[string]int, len(nodes))
	current := start
	for {
		id := nodes[current].ID
		if at, ok := seen[id]; ok {
			return append(slices.Clone(path[at:]), id)
		}
		seen[id] = len(path)
		path = append(path, id)

		next := -1
		for _, dep := range nodes[current].DependsOn {
			depIdx, ok := index[dep]
			if ok && remaining[depIdx] > 0 {
				next = depIdx
				break
			}
		}
		if next < 0 {
			return path
		}
		current = next
	}
}

// transitiveDependents returns every node reachable downstream of the given
// node, excluding the node itself.
func transitiveDependents(nodes []ResolvedNode, index map[string]int, id string) []string {
	start, ok := index[id]
	if !ok {
		return nil
	}

	seen := make(map[string]struct{}, len(nodes))
	queue := slices.Clone(nodes[start].Dependents)
	reached := make([]string, 0, len(nodes))
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		if _, done := seen[current]; done {
			continue
		}
		seen[current] = struct{}{}
		reached = append(reached, current)
		if idx, ok := index[current]; ok {
			queue = append(queue, nodes[idx].Dependents...)
		}
	}
	return reached
}
