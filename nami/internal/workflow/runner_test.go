package workflow

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"sort"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func mustResolve(t *testing.T, spec Spec) ResolvedSpec {
	t.Helper()
	resolved, err := spec.Resolve()
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	return resolved
}

func echoRunner(_ context.Context, req NodeRequest) (NodeResult, error) {
	return NodeResult{Output: "out:" + req.ID}, nil
}

func statusOf(result Result, id string) NodeState {
	for _, node := range result.Nodes {
		if node.ID == id {
			return node
		}
	}
	return NodeState{}
}

func TestRunExecutesEveryNodeAfterItsDependencies(t *testing.T) {
	resolved := mustResolve(t, Spec{Nodes: []NodeSpec{
		node("deploy", "test"),
		node("test", "build"),
		node("build"),
	}})

	var mu sync.Mutex
	var order []string
	result, err := resolved.Run(t.Context(), Options{
		RunID: "run-1",
		Run: func(_ context.Context, req NodeRequest) (NodeResult, error) {
			mu.Lock()
			order = append(order, req.ID)
			mu.Unlock()
			return NodeResult{Output: "out:" + req.ID}, nil
		},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !slices.Equal(order, []string{"build", "test", "deploy"}) {
		t.Fatalf("order = %v", order)
	}
	if result.Status != StatusSucceeded || result.Succeeded != 3 {
		t.Fatalf("result = %+v", result)
	}
	if result.RunID != "run-1" {
		t.Fatalf("RunID = %q", result.RunID)
	}
}

// Independent branches must not wait on each other. A phase-based scheduler
// would hold the fast branch until the slow one finished; a dataflow one does
// not, and this is the property the whole design exists for.
func TestRunDoesNotBarrierIndependentBranches(t *testing.T) {
	resolved := mustResolve(t, Spec{
		MaxParallel: 4,
		Nodes: []NodeSpec{
			node("slow"),
			node("slow_child", "slow"),
			node("fast"),
			node("fast_child", "fast"),
		},
	})

	slowStarted := make(chan struct{})
	fastChildDone := make(chan struct{})
	result, err := resolved.Run(t.Context(), Options{
		Run: func(ctx context.Context, req NodeRequest) (NodeResult, error) {
			switch req.ID {
			case "slow":
				close(slowStarted)
				// Hold until the other branch has run all the way to its leaf.
				select {
				case <-fastChildDone:
				case <-ctx.Done():
					return NodeResult{}, ctx.Err()
				}
			case "fast":
				<-slowStarted
			case "fast_child":
				close(fastChildDone)
			}
			return NodeResult{Output: "out:" + req.ID}, nil
		},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Succeeded != 4 {
		t.Fatalf("expected 4 succeeded, got %+v", result)
	}
}

func TestRunHonorsMaxParallel(t *testing.T) {
	nodes := make([]NodeSpec, 0, 8)
	for i := range 8 {
		nodes = append(nodes, node(fmt.Sprintf("n%d", i)))
	}
	resolved := mustResolve(t, Spec{MaxParallel: 2, Nodes: nodes})

	var inflight, peak atomic.Int64
	if _, err := resolved.Run(t.Context(), Options{
		Run: func(_ context.Context, req NodeRequest) (NodeResult, error) {
			current := inflight.Add(1)
			for {
				observed := peak.Load()
				if current <= observed || peak.CompareAndSwap(observed, current) {
					break
				}
			}
			time.Sleep(5 * time.Millisecond)
			inflight.Add(-1)
			return NodeResult{Output: req.ID}, nil
		},
	}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if peak.Load() > 2 {
		t.Fatalf("peak concurrency %d exceeds MaxParallel 2", peak.Load())
	}
}

func TestRunSkipsOnlyTransitiveDependentsOfAFailure(t *testing.T) {
	resolved := mustResolve(t, Spec{Nodes: []NodeSpec{
		node("broken"),
		node("downstream", "broken"),
		node("far_downstream", "downstream"),
		node("unrelated"),
	}})

	result, err := resolved.Run(t.Context(), Options{
		Run: func(_ context.Context, req NodeRequest) (NodeResult, error) {
			if req.ID == "broken" {
				return NodeResult{}, errors.New("boom")
			}
			return NodeResult{Output: req.ID}, nil
		},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := statusOf(result, "broken").Status; got != StatusFailed {
		t.Fatalf("broken status = %q", got)
	}
	for _, id := range []string{"downstream", "far_downstream"} {
		if got := statusOf(result, id).Status; got != StatusSkipped {
			t.Fatalf("%s status = %q, want skipped", id, got)
		}
	}
	if got := statusOf(result, "unrelated").Status; got != StatusSucceeded {
		t.Fatalf("unrelated status = %q, want succeeded", got)
	}
	if result.Failed != 1 || result.Skipped != 2 || result.Succeeded != 1 {
		t.Fatalf("counts = %+v", result)
	}
}

func TestRunAbortPolicySkipsIndependentBranches(t *testing.T) {
	resolved := mustResolve(t, Spec{
		MaxParallel:   1,
		OnNodeFailure: string(FailureAbort),
		Nodes:         []NodeSpec{node("aaa_broken"), node("zzz_unrelated")},
	})

	result, err := resolved.Run(t.Context(), Options{
		Run: func(_ context.Context, req NodeRequest) (NodeResult, error) {
			if req.ID == "aaa_broken" {
				return NodeResult{}, errors.New("boom")
			}
			return NodeResult{Output: req.ID}, nil
		},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := statusOf(result, "zzz_unrelated").Status; got != StatusSkipped {
		t.Fatalf("zzz_unrelated status = %q, want skipped under abort", got)
	}
}

func TestRunExpandsDependencyOutputsIntoPrompts(t *testing.T) {
	resolved := mustResolve(t, Spec{Nodes: []NodeSpec{
		node("collect"),
		{
			ID:          "summarize",
			Description: "summarize",
			Prompt:      "summarize this: ${outputs.collect}",
			DependsOn:   []string{"collect"},
		},
	}})

	var seenPrompt string
	var seenInputs map[string]string
	if _, err := resolved.Run(t.Context(), Options{
		Run: func(_ context.Context, req NodeRequest) (NodeResult, error) {
			if req.ID == "summarize" {
				seenPrompt = req.Prompt
				seenInputs = req.Inputs
			}
			return NodeResult{Output: "out:" + req.ID}, nil
		},
	}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if seenPrompt != "summarize this: out:collect" {
		t.Fatalf("prompt = %q", seenPrompt)
	}
	if seenInputs["collect"] != "out:collect" {
		t.Fatalf("inputs = %v", seenInputs)
	}
}

func TestRunReportsProgressForEveryTransition(t *testing.T) {
	resolved := mustResolve(t, Spec{Nodes: []NodeSpec{node("a"), node("b", "a")}})

	var mu sync.Mutex
	transitions := map[string][]Status{}
	if _, err := resolved.Run(t.Context(), Options{
		Run: echoRunner,
		OnProgress: func(p Progress) {
			mu.Lock()
			transitions[p.Node.ID] = append(transitions[p.Node.ID], p.Node.Status)
			mu.Unlock()
			if p.Total != 2 {
				t.Errorf("Total = %d, want 2", p.Total)
			}
		},
	}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	for _, id := range []string{"a", "b"} {
		if want := []Status{StatusRunning, StatusSucceeded}; !slices.Equal(transitions[id], want) {
			t.Fatalf("%s transitions = %v, want %v", id, transitions[id], want)
		}
	}
}

func TestRunWithoutRunnerErrors(t *testing.T) {
	resolved := mustResolve(t, Spec{Nodes: []NodeSpec{node("a")}})
	if _, err := resolved.Run(t.Context(), Options{}); !errors.Is(err, ErrNoRunner) {
		t.Fatalf("err = %v, want ErrNoRunner", err)
	}
}

func TestRunRecordsNodeTimings(t *testing.T) {
	resolved := mustResolve(t, Spec{Nodes: []NodeSpec{node("a")}})
	result, err := resolved.Run(t.Context(), Options{
		Run: func(_ context.Context, req NodeRequest) (NodeResult, error) {
			time.Sleep(2 * time.Millisecond)
			return NodeResult{Output: req.ID}, nil
		},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if d := statusOf(result, "a").Duration(); d <= 0 {
		t.Fatalf("Duration = %v, want positive", d)
	}
}

func TestResumeReplaysUnchangedPrefixAndRerunsTheRest(t *testing.T) {
	dir := t.TempDir()
	first := filepath.Join(dir, "first.ndjson")
	second := filepath.Join(dir, "second.ndjson")

	spec := Spec{MaxParallel: 1, Nodes: []NodeSpec{node("a"), node("b", "a"), node("c", "b")}}
	resolved := mustResolve(t, spec)

	journal, err := OpenJournal(first, "")
	if err != nil {
		t.Fatalf("OpenJournal: %v", err)
	}
	var firstRuns []string
	if _, err := resolved.Run(t.Context(), Options{
		Journal: journal,
		Run: func(_ context.Context, req NodeRequest) (NodeResult, error) {
			firstRuns = append(firstRuns, req.ID)
			return NodeResult{Output: "v1:" + req.ID}, nil
		},
	}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if err := journal.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if !slices.Equal(firstRuns, []string{"a", "b", "c"}) {
		t.Fatalf("first run executed %v", firstRuns)
	}

	// Edit only the last node. "a" and "b" must replay; "c" must re-execute.
	edited := spec
	edited.Nodes = slices.Clone(spec.Nodes)
	edited.Nodes[2] = NodeSpec{ID: "c", Description: "run c", Prompt: "do c differently", DependsOn: []string{"b"}}
	resumed := mustResolve(t, edited)

	resumeJournal, err := OpenJournal(second, first)
	if err != nil {
		t.Fatalf("OpenJournal: %v", err)
	}
	var secondRuns []string
	result, err := resumed.Run(t.Context(), Options{
		Journal: resumeJournal,
		Run: func(_ context.Context, req NodeRequest) (NodeResult, error) {
			secondRuns = append(secondRuns, req.ID)
			return NodeResult{Output: "v2:" + req.ID}, nil
		},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if err := resumeJournal.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if !slices.Equal(secondRuns, []string{"c"}) {
		t.Fatalf("resume executed %v, want only [c]", secondRuns)
	}
	if got := statusOf(result, "a"); got.Status != StatusCached || got.Output != "v1:a" {
		t.Fatalf("a = %+v, want cached v1:a", got)
	}
	if got := statusOf(result, "c"); got.Status != StatusSucceeded || got.Output != "v2:c" {
		t.Fatalf("c = %+v, want fresh v2:c", got)
	}
	if result.Cached != 2 {
		t.Fatalf("Cached = %d, want 2", result.Cached)
	}
}

// Once the chain breaks, a later node whose key happens to match a recorded one
// must still re-run: its recorded result belongs to a different graph.
func TestResumeStopsReplayingAfterFirstDivergence(t *testing.T) {
	dir := t.TempDir()
	first := filepath.Join(dir, "first.ndjson")
	second := filepath.Join(dir, "second.ndjson")

	resolved := mustResolve(t, Spec{MaxParallel: 1, Nodes: []NodeSpec{node("a"), node("b", "a")}})
	journal, err := OpenJournal(first, "")
	if err != nil {
		t.Fatalf("OpenJournal: %v", err)
	}
	if _, err := resolved.Run(t.Context(), Options{Journal: journal, Run: echoRunner}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if err := journal.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Change the FIRST node. Everything after it must re-execute.
	changed := mustResolve(t, Spec{MaxParallel: 1, Nodes: []NodeSpec{
		{ID: "a", Description: "run a", Prompt: "do a differently"},
		node("b", "a"),
	}})
	resumeJournal, err := OpenJournal(second, first)
	if err != nil {
		t.Fatalf("OpenJournal: %v", err)
	}
	var executed []string
	if _, err := changed.Run(t.Context(), Options{
		Journal: resumeJournal,
		Run: func(_ context.Context, req NodeRequest) (NodeResult, error) {
			executed = append(executed, req.ID)
			return NodeResult{Output: req.ID}, nil
		},
	}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if err := resumeJournal.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	sort.Strings(executed)
	if !slices.Equal(executed, []string{"a", "b"}) {
		t.Fatalf("executed %v, want both nodes re-run", executed)
	}
}

func TestNilJournalIsUsable(t *testing.T) {
	var journal *Journal
	if _, ok := journal.Replay("k"); ok {
		t.Fatal("nil journal replayed")
	}
	journal.Record("k", "n", NodeResult{})
	if journal.Path() != "" {
		t.Fatal("nil journal has a path")
	}
	if err := journal.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func TestOpenJournalToleratesMissingResumeSource(t *testing.T) {
	dir := t.TempDir()
	journal, err := OpenJournal(filepath.Join(dir, "j.ndjson"), filepath.Join(dir, "absent.ndjson"))
	if err != nil {
		t.Fatalf("OpenJournal: %v", err)
	}
	if err := journal.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

// The key a node records must not depend on the order the scheduler happened to
// launch things. Two sibling chains launch their second node in whichever order
// their first node finished, so a key derived from a linear "everything before
// this" chain would differ between two identical runs and miss on every resume.
func TestResumeIsStableAcrossParallelRuns(t *testing.T) {
	dir := t.TempDir()
	resolved := mustResolve(t, Spec{
		MaxParallel: 2,
		Nodes: []NodeSpec{
			node("a"), node("a_next", "a"),
			node("b"), node("b_next", "b"),
		},
	})

	run := func(path string, resumeFrom string, delays map[string]time.Duration) (Result, []string) {
		t.Helper()
		journal, err := OpenJournal(path, resumeFrom)
		if err != nil {
			t.Fatalf("OpenJournal: %v", err)
		}
		var mu sync.Mutex
		var executed []string
		result, err := resolved.Run(t.Context(), Options{
			Journal: journal,
			Run: func(_ context.Context, req NodeRequest) (NodeResult, error) {
				time.Sleep(delays[req.ID])
				mu.Lock()
				executed = append(executed, req.ID)
				mu.Unlock()
				return NodeResult{Output: "out:" + req.ID}, nil
			},
		})
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
		if err := journal.Close(); err != nil {
			t.Fatalf("Close: %v", err)
		}
		return result, executed
	}

	// First run: a finishes well before b, so a_next launches third.
	first := filepath.Join(dir, "first.ndjson")
	_, executed := run(first, "", map[string]time.Duration{"a": time.Millisecond, "b": 30 * time.Millisecond})
	if len(executed) != 4 {
		t.Fatalf("first run executed %v", executed)
	}
	if executed[0] != "a" || executed[1] != "a_next" {
		t.Fatalf("first run order = %v, want a then a_next before b", executed)
	}

	// Second run: b finishes first, so the launch order is reversed.
	result, executed := run(
		filepath.Join(dir, "second.ndjson"),
		first,
		map[string]time.Duration{"a": 30 * time.Millisecond, "b": time.Millisecond},
	)
	if len(executed) != 0 {
		t.Fatalf("resume re-executed %v despite an unchanged graph", executed)
	}
	if result.Cached != 4 {
		t.Fatalf("Cached = %d, want 4", result.Cached)
	}
}

// Changing one branch must re-run that branch and nothing else. A node's key
// commits to its own ancestry, so an untouched branch is untouched.
func TestResumeRerunsOnlyTheAffectedBranch(t *testing.T) {
	dir := t.TempDir()
	first := filepath.Join(dir, "first.ndjson")
	spec := Spec{
		MaxParallel: 2,
		Nodes: []NodeSpec{
			node("left"), node("left_child", "left"),
			node("right"), node("right_child", "right"),
		},
	}

	journal, err := OpenJournal(first, "")
	if err != nil {
		t.Fatalf("OpenJournal: %v", err)
	}
	if _, err := mustResolve(t, spec).Run(t.Context(), Options{Journal: journal, Run: echoRunner}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if err := journal.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	edited := spec
	edited.Nodes = slices.Clone(spec.Nodes)
	edited.Nodes[0] = NodeSpec{ID: "left", Description: "run left", Prompt: "do left differently"}

	resumeJournal, err := OpenJournal(filepath.Join(dir, "second.ndjson"), first)
	if err != nil {
		t.Fatalf("OpenJournal: %v", err)
	}
	var mu sync.Mutex
	var executed []string
	result, err := mustResolve(t, edited).Run(t.Context(), Options{
		Journal: resumeJournal,
		Run: func(_ context.Context, req NodeRequest) (NodeResult, error) {
			mu.Lock()
			executed = append(executed, req.ID)
			mu.Unlock()
			return NodeResult{Output: "out:" + req.ID}, nil
		},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if err := resumeJournal.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	sort.Strings(executed)
	if !slices.Equal(executed, []string{"left", "left_child"}) {
		t.Fatalf("executed %v, want only the left branch", executed)
	}
	for _, id := range []string{"right", "right_child"} {
		if got := statusOf(result, id).Status; got != StatusCached {
			t.Fatalf("%s status = %q, want cached", id, got)
		}
	}
}

// A node that interpolates an upstream result must re-run when that result
// changes, even though its own prompt template is untouched.
func TestResumeRerunsAConsumerWhenUpstreamOutputChanges(t *testing.T) {
	dir := t.TempDir()
	first := filepath.Join(dir, "first.ndjson")
	resolved := mustResolve(t, Spec{
		MaxParallel: 1,
		Nodes: []NodeSpec{
			node("produce"),
			{ID: "consume", Description: "consume", Prompt: "use ${outputs.produce}", DependsOn: []string{"produce"}},
			node("ignore", "produce"),
		},
	})

	output := "v1"
	runner := func(_ context.Context, req NodeRequest) (NodeResult, error) {
		if req.ID == "produce" {
			return NodeResult{Output: output}, nil
		}
		return NodeResult{Output: "out:" + req.ID}, nil
	}

	journal, err := OpenJournal(first, "")
	if err != nil {
		t.Fatalf("OpenJournal: %v", err)
	}
	if _, err := resolved.Run(t.Context(), Options{Journal: journal, Run: runner}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if err := journal.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// "produce" replays, so its output is stable and both consumers replay too.
	resumeJournal, err := OpenJournal(filepath.Join(dir, "second.ndjson"), first)
	if err != nil {
		t.Fatalf("OpenJournal: %v", err)
	}
	var executed []string
	result, err := resolved.Run(t.Context(), Options{
		Journal: resumeJournal,
		Run: func(ctx context.Context, req NodeRequest) (NodeResult, error) {
			executed = append(executed, req.ID)
			return runner(ctx, req)
		},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if err := resumeJournal.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if len(executed) != 0 {
		t.Fatalf("resume executed %v, want nothing", executed)
	}
	if got := statusOf(result, "consume").Output; got != "out:consume" {
		t.Fatalf("consume output = %q", got)
	}
}
