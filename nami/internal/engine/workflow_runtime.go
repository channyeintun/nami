package engine

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
	"uuid"

	"github.com/channyeintun/nami/internal/ipc"
	toolpkg "github.com/channyeintun/nami/internal/tools"
	workflowpkg "github.com/channyeintun/nami/internal/workflow"
)

// workflowRunRetention is how long a finished run stays queryable through
// workflow_status before it is evicted.
const workflowRunRetention = 5 * time.Minute

var (
	workflowRunsMu sync.RWMutex
	workflowRuns   = map[string]*workflowRun{}
)

// workflowRun keeps a run's live state so workflow_status can report on it
// while it is still going and after it finishes.
type workflowRun struct {
	mu          sync.RWMutex
	id          string
	description string
	journalPath string
	resumedFrom string
	sessionDir  string
	startedAt   time.Time
	completedAt time.Time
	nodes       []toolpkg.WorkflowNodeSnapshot
	nodeIndex   map[string]int
	result      *workflowpkg.Result
	err         string
	// warnings are problems that did not stop the run, for the run result.
	warnings []string
	// updated is closed and replaced on every node transition so a waiting
	// workflow_status call wakes on real progress rather than polling.
	updated chan struct{}
}

// newWorkflowRunID returns an id no other process hands out. A run's journal is
// named after its id, and a resumed session's runs share its workflows
// directory with the runs of earlier processes; a counter restarted at wf_1 in
// every process. Twelve hex digits of a random UUID keep the id short enough
// to type.
func newWorkflowRunID() string {
	id := uuid.New()
	return "wf_" + hex.EncodeToString(id[:6])
}

func registerWorkflowRun(run *workflowRun) {
	workflowRunsMu.Lock()
	defer workflowRunsMu.Unlock()
	workflowRuns[run.id] = run
}

// scheduleWorkflowRunCleanup drops a finished run once its results have had
// long enough to be read back. Without this the registry grows for the life of
// the session, holding every node's output from every run ever launched. The
// journal on disk outlives the entry, so a run stays resumable after eviction.
func scheduleWorkflowRunCleanup(run *workflowRun) {
	time.AfterFunc(workflowRunRetention, func() {
		if !run.done() {
			scheduleWorkflowRunCleanup(run)
			return
		}
		workflowRunsMu.Lock()
		defer workflowRunsMu.Unlock()
		if current, ok := workflowRuns[run.id]; ok && current == run {
			delete(workflowRuns, run.id)
		}
	})
}

func getWorkflowRun(runID string) (*workflowRun, error) {
	runID = strings.TrimSpace(runID)
	workflowRunsMu.RLock()
	defer workflowRunsMu.RUnlock()
	run, ok := workflowRuns[runID]
	if !ok {
		return nil, fmt.Errorf("unknown workflow run %q", runID)
	}
	return run, nil
}

// journalPathForRun is where a run records its node results.
func journalPathForRun(sessionDir string, runID string) string {
	if strings.TrimSpace(sessionDir) == "" {
		return ""
	}
	return filepath.Join(sessionDir, "workflows", runID+".ndjson")
}

// launchWorkflow resolves the graph and runs it to completion. Each node is one
// synchronous child agent: the graph owns scheduling, so handing nodes to the
// background agent machinery as well would schedule the same work twice.
func launchWorkflow(
	ctx context.Context,
	runner toolpkg.AgentRunner,
	bridge *ipc.Bridge,
	sessionDir string,
	req toolpkg.WorkflowLaunchRequest,
) (toolpkg.WorkflowRunResult, error) {
	if runner == nil {
		return toolpkg.WorkflowRunResult{}, fmt.Errorf("workflow launcher is not configured")
	}

	resolved, err := req.Spec.Resolve()
	if err != nil {
		return toolpkg.WorkflowRunResult{}, err
	}

	runID := newWorkflowRunID()
	run := &workflowRun{
		id:          runID,
		description: resolved.Description,
		sessionDir:  sessionDir,
		startedAt:   time.Now(),
		journalPath: journalPathForRun(sessionDir, runID),
		nodes:       make([]toolpkg.WorkflowNodeSnapshot, 0, len(resolved.Nodes)),
		nodeIndex:   make(map[string]int, len(resolved.Nodes)),
		updated:     make(chan struct{}),
	}
	for _, node := range resolved.Nodes {
		run.nodeIndex[node.ID] = len(run.nodes)
		run.nodes = append(run.nodes, toolpkg.WorkflowNodeSnapshot{
			ID:          node.ID,
			Description: node.Description,
			Status:      string(workflowpkg.StatusQueued),
			DependsOn:   node.DependsOn,
		})
	}
	var journal *workflowpkg.Journal
	if run.journalPath != "" {
		resumePath := ""
		if previous := strings.TrimSpace(req.ResumeFromRunID); previous != "" {
			run.resumedFrom = previous
			resumePath = resumeJournalPath(run, sessionDir, previous)
		}
		journal = openRunJournal(run, resumePath)
	}
	registerWorkflowRun(run)
	scheduleWorkflowRunCleanup(run)

	result, err := resolved.Run(ctx, workflowpkg.Options{
		RunID:      runID,
		Journal:    journal,
		Run:        workflowNodeRunner(runner),
		OnProgress: workflowProgressReporter(run, bridge),
	})
	// Closed before the run finishes, so a failure reaches its result.
	if closeErr := journal.Close(); closeErr != nil {
		run.addWarning(fmt.Sprintf("The journal may be missing results, and resuming this run would run those nodes again: %v", closeErr))
	}
	if err != nil {
		run.finish(nil, err)
		return toolpkg.WorkflowRunResult{}, err
	}

	run.finish(&result, nil)
	return run.snapshot(), nil
}

// resumeJournalPath returns the journal of the run being resumed, which has to
// be an earlier run of this session. For any other id it returns "" and warns
// that nothing was replayed: the journal loader reads a missing journal as a
// cold start, and the model would take the run for a resume.
func resumeJournalPath(run *workflowRun, sessionDir string, runID string) string {
	// A run id names a file in the session's workflows directory. One with
	// path parts would read a journal from somewhere else.
	if runID != filepath.Base(runID) {
		run.addWarning(fmt.Sprintf("%q is not a workflow run id, so nothing was replayed and every node ran.", runID))
		return ""
	}
	path := journalPathForRun(sessionDir, runID)
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		run.addWarning(fmt.Sprintf("Run %s has no journal in this session, so nothing was replayed and every node ran.", runID))
		return ""
	}
	// Any other failure to reach the journal is openRunJournal's to report.
	return path
}

// openRunJournal opens run's journal, seeded with the records of the journal at
// resumePath. The run's results need neither, so a failure is recorded as a
// warning on the run instead of stopping it. A resume journal that cannot be
// read must not cost the run its own journal as well: the journal is then
// opened unseeded, and this run stays resumable itself.
func openRunJournal(run *workflowRun, resumePath string) *workflowpkg.Journal {
	journal, err := workflowpkg.OpenJournal(run.journalPath, resumePath)
	if err == nil {
		return journal
	}
	if resumePath == "" {
		run.addWarning(fmt.Sprintf("The run has no journal and cannot be resumed: %v", err))
		return nil
	}
	// OpenJournal fails as a whole when the resume journal cannot be read.
	// Opening without it tells that apart from a journal that cannot be
	// written.
	unseeded, unseededErr := workflowpkg.OpenJournal(run.journalPath, "")
	if unseededErr != nil {
		run.addWarning(fmt.Sprintf("The run has no journal, so nothing was replayed from run %s and this run cannot be resumed: %v", run.resumedFrom, unseededErr))
		return nil
	}
	run.addWarning(fmt.Sprintf("Could not read the journal of run %s, so nothing was replayed and every node ran again: %v", run.resumedFrom, err))
	return unseeded
}

// workflowNodeRunner turns each graph node into one child-agent invocation and
// carries the agent's identifiers back so a node's full transcript stays
// reachable from the run summary.
func workflowNodeRunner(runner toolpkg.AgentRunner) workflowpkg.NodeRunner {
	return func(ctx context.Context, req workflowpkg.NodeRequest) (workflowpkg.NodeResult, error) {
		agentResult, err := runner(ctx, toolpkg.AgentRunRequest{
			Description:       req.Description,
			Prompt:            req.Prompt,
			Role:              req.Agent.Role,
			WorkspaceStrategy: req.Agent.WorkspaceStrategy,
			SubagentType:      toolpkg.NormalizeSubagentType(req.Agent.SubagentType),
			Background:        false,
		})
		if err != nil {
			return workflowpkg.NodeResult{}, err
		}
		if strings.EqualFold(strings.TrimSpace(agentResult.Status), "failed") {
			reason := strings.TrimSpace(agentResult.Error)
			if reason == "" {
				reason = "child agent failed"
			}
			return workflowpkg.NodeResult{}, fmt.Errorf("%s", reason)
		}
		return workflowpkg.NodeResult{
			Output: agentResult.Summary,
			Metadata: map[string]string{
				"agent_id":        strings.TrimSpace(agentResult.AgentID),
				"session_id":      strings.TrimSpace(agentResult.SessionID),
				"transcript_path": strings.TrimSpace(agentResult.TranscriptPath),
			},
		}, nil
	}
}

func workflowProgressReporter(run *workflowRun, bridge *ipc.Bridge) func(workflowpkg.Progress) {
	return func(progress workflowpkg.Progress) {
		run.applyProgress(progress)
		if bridge == nil {
			return
		}
		_ = bridge.Emit(ipc.EventWorkflowProgress, ipc.WorkflowProgressPayload{
			RunID:       progress.RunID,
			Description: run.description,
			NodeID:      progress.Node.ID,
			NodeLabel:   progress.Node.Description,
			Status:      string(progress.Node.Status),
			Completed:   progress.Completed,
			Total:       progress.Total,
			DependsOn:   progress.Node.DependsOn,
			Message:     strings.TrimSpace(progress.Node.Error),
		})
	}
}

func (r *workflowRun) applyProgress(progress workflowpkg.Progress) {
	r.mu.Lock()
	idx, ok := r.nodeIndex[progress.Node.ID]
	if ok {
		node := &r.nodes[idx]
		node.Status = string(progress.Node.Status)
		node.Error = progress.Node.Error
		node.Output = progress.Node.Output
		node.DurationMillis = progress.Node.Duration().Milliseconds()
		node.AgentID = progress.Node.Metadata["agent_id"]
		node.SessionID = progress.Node.Metadata["session_id"]
		node.TranscriptPath = progress.Node.Metadata["transcript_path"]
	}
	waiters := r.updated
	r.updated = make(chan struct{})
	r.mu.Unlock()
	close(waiters)
}

func (r *workflowRun) addWarning(warning string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.warnings = append(r.warnings, warning)
}

func (r *workflowRun) finish(result *workflowpkg.Result, err error) {
	r.mu.Lock()
	r.result = result
	r.completedAt = time.Now()
	if err != nil {
		r.err = err.Error()
	}
	waiters := r.updated
	r.updated = make(chan struct{})
	r.mu.Unlock()
	close(waiters)
}

func (r *workflowRun) snapshot() toolpkg.WorkflowRunResult {
	r.mu.RLock()
	defer r.mu.RUnlock()

	snapshot := toolpkg.WorkflowRunResult{
		RunID:       r.id,
		Description: r.description,
		JournalPath: r.journalPath,
		ResumedFrom: r.resumedFrom,
		NodeCount:   len(r.nodes),
		Nodes:       append([]toolpkg.WorkflowNodeSnapshot(nil), r.nodes...),
		Error:       r.err,
		Warnings:    slices.Clone(r.warnings),
	}
	for _, node := range r.nodes {
		switch workflowpkg.Status(node.Status) {
		case workflowpkg.StatusSucceeded:
			snapshot.Succeeded++
		case workflowpkg.StatusCached:
			snapshot.Succeeded++
			snapshot.Cached++
		case workflowpkg.StatusFailed:
			snapshot.Failed++
		case workflowpkg.StatusSkipped:
			snapshot.Skipped++
		}
	}

	switch {
	case r.err != "":
		snapshot.Status = "failed"
	case r.result == nil:
		snapshot.Status = "running"
	case snapshot.Failed > 0 || snapshot.Skipped > 0:
		snapshot.Status = "failed"
	default:
		snapshot.Status = "completed"
	}
	if !r.completedAt.IsZero() {
		snapshot.DurationMillis = r.completedAt.Sub(r.startedAt).Milliseconds()
	}
	return snapshot
}

func (r *workflowRun) done() bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.result != nil || r.err != ""
}

func (r *workflowRun) waitChannel() <-chan struct{} {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.updated
}

func lookupWorkflowStatus(ctx context.Context, req toolpkg.WorkflowStatusRequest) (toolpkg.WorkflowRunResult, error) {
	run, err := getWorkflowRun(req.RunID)
	if err != nil {
		return toolpkg.WorkflowRunResult{}, err
	}
	if req.WaitMs <= 0 {
		return run.snapshot(), nil
	}

	deadline := time.NewTimer(time.Duration(req.WaitMs) * time.Millisecond)
	defer deadline.Stop()
	for {
		// Take the wake channel BEFORE testing done. Each transition swaps in a
		// fresh channel, so reading it after the test would hand back the next
		// one and miss the transition that just happened — including the run's
		// last, which would wait out the whole timeout on a finished run.
		wake := run.waitChannel()
		if run.done() {
			return run.snapshot(), nil
		}
		select {
		case <-ctx.Done():
			return run.snapshot(), ctx.Err()
		case <-deadline.C:
			return run.snapshot(), nil
		case <-wake:
		}
	}
}
