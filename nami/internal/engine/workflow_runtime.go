package engine

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
	"uuid"

	"github.com/channyeintun/nami/internal/config"
	"github.com/channyeintun/nami/internal/ipc"
	"github.com/channyeintun/nami/internal/session"
	toolpkg "github.com/channyeintun/nami/internal/tools"
	workflowpkg "github.com/channyeintun/nami/internal/workflow"
)

const (
	// workflowRunRetention is how long a finished run stays in memory for
	// workflow_status. After that its result.json answers instead.
	workflowRunRetention = 5 * time.Minute
	// workflowEmitInterval spaces workflow_updated events while a run goes
	// on. A run of hundreds of agents changes state far more often than a
	// person can read, and every event is a whole snapshot.
	workflowEmitInterval = 250 * time.Millisecond
	// workflowMaxLogs is how many log lines a run keeps.
	workflowMaxLogs = 50
)

// What a workflow_updated event carries of a run's logs, previews, and result.
const (
	workflowUpdateLogs         = 20
	workflowUpdatePreviewRunes = 300
	workflowUpdateResultRunes  = 2000
)

// A run's files, in <session dir>/workflows/<run id>/.
const (
	workflowRunsDirName     = "workflows"
	workflowScriptFileName  = "script.js"
	workflowJournalFileName = "journal.ndjson"
	workflowResultFileName  = "result.json"
)

var (
	workflowRunsMu sync.RWMutex
	workflowRuns   = map[string]*workflowRun{}

	// workflowAgentSlots bounds the child agents of every run together. Each
	// run taking its own DefaultConcurrency slots would let N runs start N
	// times as many children as the machine can serve.
	workflowAgentSlots = make(chan struct{}, workflowpkg.DefaultConcurrency())
)

// workflowLaunchDeps is what a run needs from the engine. The engine fills it
// in on the tool call's goroutine, so the run keeps the session, directory,
// and permissions it was launched with however long it goes on.
type workflowLaunchDeps struct {
	// runner runs one child agent to completion. Tests pass a fake.
	runner toolpkg.AgentRunner
	bridge *ipc.Bridge
	// ownerSessionID is the session that launched the run. Updates go to the
	// TUI only while that session is the active one.
	ownerSessionID string
	// sessionDir holds the run's files and the journals it may resume from.
	sessionDir string
	// cwd resolves relative script paths and the project's saved workflows
	// for workflow() calls inside the script.
	cwd string
	// modelState checks per-agent model overrides.
	modelState *ActiveModelState
	// slots is shared by every run. Nil gives the run slots of its own.
	slots chan struct{}
}

// workflowRun is one run's live state, kept for workflow_status and the TUI
// while it goes on and for a while after it finishes.
type workflowRun struct {
	// Set at launch and never changed.
	id             string
	name           string
	description    string
	ownerSessionID string
	scriptPath     string
	journalPath    string
	resumedFrom    string
	startedAt      time.Time
	bridge         *ipc.Bridge
	cancel         context.CancelFunc
	// done is closed once the run has settled and its final state, including
	// result.json, is recorded. It is closed exactly once and never replaced,
	// so a waiter that takes it before the run ends is always woken: unlike a
	// progress channel swapped on every change, there is no later channel for
	// a waiter to have missed.
	done chan struct{}

	mu           sync.Mutex
	status       string
	currentPhase string
	phases       []toolpkg.WorkflowPhase
	agents       []workflowAgentEntry
	logs         []string
	warnings     []string
	result       json.RawMessage
	err          string
	completedAt  time.Time
	// resultPath is where result.json goes, or empty when the run keeps no
	// files or writing it failed.
	resultPath    string
	emitScheduled bool
	lastEmitAt    time.Time

	// emitMu orders workflow_updated events, so an update built while the
	// run was going can never reach the TUI after the final one.
	emitMu       sync.Mutex
	finalEmitted bool
}

// workflowAgentEntry is one agent() call as a run keeps it. The agent's
// output is cut to a preview: the whole of it is in the journal and the
// agent's transcript, and a run of a thousand agents must not hold every
// output in memory.
type workflowAgentEntry struct {
	// record has its Text and Structured dropped.
	record  workflowpkg.AgentRecord
	preview string
}

// newWorkflowRunID returns an id no other process hands out. A run's files
// are named after its id, and a resumed session's runs share its workflows
// directory with the runs of earlier processes, so a counter that restarts
// with every process would give two runs one directory. Twelve hex digits of
// a version 4 UUID are random and still short enough to type; NewV4 is named
// because the first bytes of a time-ordered UUID are a timestamp, which two
// runs started in the same millisecond would share.
func newWorkflowRunID() string {
	id := uuid.NewV4()
	return "wf_" + hex.EncodeToString(id[:6])
}

func workflowRunDir(sessionDir string, runID string) string {
	return filepath.Join(sessionDir, workflowRunsDirName, runID)
}

func registerWorkflowRun(run *workflowRun) {
	workflowRunsMu.Lock()
	defer workflowRunsMu.Unlock()
	workflowRuns[run.id] = run
}

// hasRunningWorkflows reports whether any run is still going. Workflow agents
// resolve relative paths against the process's working directory, so nothing
// may move that directory while one runs.
func hasRunningWorkflows() bool {
	workflowRunsMu.RLock()
	defer workflowRunsMu.RUnlock()
	for _, run := range workflowRuns {
		if !run.finished() {
			return true
		}
	}
	return false
}

// errWorkflowsHoldTheDirectory refuses a working-directory switch while a
// workflow runs.
var errWorkflowsHoldTheDirectory = errors.New("cannot switch the working directory while a workflow is running: its agents resolve paths against it. Stop the workflow with workflow_stop or /workflows, or wait for it to finish")

// workflowsBlockDirectoryMove explains why /resume must wait, or returns ""
// when it may go ahead. Resuming a session that lives in another directory
// moves the process there, and a running workflow's agents would follow.
func workflowsBlockDirectoryMove(store *session.Store, targetSessionID string) string {
	if !hasRunningWorkflows() {
		return ""
	}
	target, err := store.LoadMetadata(targetSessionID)
	if err != nil || strings.TrimSpace(target.CWD) == "" {
		return ""
	}
	current, err := os.Getwd()
	if err != nil || filepath.Clean(current) == filepath.Clean(target.CWD) {
		return ""
	}
	return fmt.Sprintf("Not resuming yet: that session works in %s, and a workflow is still running here. Its agents resolve paths against the working directory, so moving now would point them at the other project. Stop the workflow from /workflows or wait for it to finish, then resume.", target.CWD)
}

func findWorkflowRun(runID string) (*workflowRun, bool) {
	workflowRunsMu.RLock()
	defer workflowRunsMu.RUnlock()
	run, ok := workflowRuns[strings.TrimSpace(runID)]
	return run, ok
}

// evictWorkflowRun drops a finished run from the registry. Without this the
// registry would hold every run of the session, each with all its agents.
// It deletes only this very run, never a newer one registered under the
// same id, and leaves a run that is still going alone.
func evictWorkflowRun(run *workflowRun) {
	if !run.finished() {
		return
	}
	workflowRunsMu.Lock()
	defer workflowRunsMu.Unlock()
	if current, ok := workflowRuns[run.id]; ok && current == run {
		delete(workflowRuns, run.id)
	}
}

// launchWorkflowRun starts a run in the background and returns at once. The
// run lives on context.Background: it outlives the tool call and the turn,
// and only workflow_stop or the TUI stops it.
func launchWorkflowRun(deps workflowLaunchDeps, req toolpkg.WorkflowLaunchRequest) (toolpkg.WorkflowLaunchResult, error) {
	if deps.runner == nil {
		return toolpkg.WorkflowLaunchResult{}, errors.New("workflow launcher is not configured: it has no agent runner")
	}

	meta := req.Script.Meta
	run := &workflowRun{
		id:             newWorkflowRunID(),
		name:           meta.Name,
		description:    meta.Description,
		ownerSessionID: deps.ownerSessionID,
		startedAt:      time.Now(),
		bridge:         deps.bridge,
		done:           make(chan struct{}),
		status:         toolpkg.WorkflowStatusRunning,
	}
	for _, phase := range meta.Phases {
		run.phases = append(run.phases, toolpkg.WorkflowPhase{Title: phase.Title, Detail: phase.Detail})
	}
	run.saveScript(deps.sessionDir, req.Script.Source)
	journal := run.openJournal(deps.sessionDir, req.ResumeFromRunID)

	ctx, cancel := context.WithCancel(context.Background())
	run.cancel = cancel
	registerWorkflowRun(run)

	opts := workflowpkg.Options{
		Script:    req.Script,
		Args:      req.Args,
		Run:       workflowAgentRunner(deps.runner),
		CheckCall: checkWorkflowAgentCall(deps.modelState),
		Load: func(ref workflowpkg.Ref) (workflowpkg.Script, error) {
			return loadWorkflowRef(ref, deps.cwd)
		},
		Journal:     journal,
		Slots:       deps.slots,
		TokenBudget: req.TokenBudget,
		OnEvent:     run.applyEvent,
	}
	go run.execute(ctx, opts)
	// Shown in the TUI right away, before any agent has started.
	run.requestEmit()
	return run.launchResult(), nil
}

// saveScript gives the run a directory and keeps a copy of its script there,
// so the script can be edited and resumed even when it was passed inline.
// Failing to keep files costs the run its resumability, not its result, so
// it becomes a warning.
func (r *workflowRun) saveScript(sessionDir string, source string) {
	if strings.TrimSpace(sessionDir) == "" {
		r.addWarning("The run has no session directory, so it keeps no files: it cannot be resumed, and its result cannot be read once it leaves memory.")
		return
	}
	runDir := workflowRunDir(sessionDir, r.id)
	if err := os.MkdirAll(runDir, sessionDataDirMode); err != nil {
		r.addWarning(fmt.Sprintf("Could not create the run's directory, so the run cannot be resumed and its result cannot be read once it leaves memory: %v", err))
		return
	}
	r.journalPath = filepath.Join(runDir, workflowJournalFileName)
	r.resultPath = filepath.Join(runDir, workflowResultFileName)
	scriptPath := filepath.Join(runDir, workflowScriptFileName)
	if err := os.WriteFile(scriptPath, []byte(source), sessionDataFileMode); err != nil {
		r.addWarning(fmt.Sprintf("Could not save a copy of the script: %v", err))
		return
	}
	r.scriptPath = scriptPath
}

// openJournal opens the run's journal, seeded with the journal of the run it
// resumes. The run's results need neither, so a failure is a warning on the
// run instead of a failed launch.
// A run left without a journal reports no journal path, so nobody looks for
// a file that is not there.
func (r *workflowRun) openJournal(sessionDir string, resumeFrom string) *workflowpkg.Journal {
	resumePath := ""
	if resumeFrom = strings.TrimSpace(resumeFrom); resumeFrom != "" {
		r.resumedFrom = resumeFrom
		resumePath = r.resumeJournalPath(sessionDir, resumeFrom)
	}
	if r.journalPath == "" {
		// saveScript has said the run keeps no files; the model asked to
		// resume, so it also has to hear that nothing was replayed.
		if resumePath != "" {
			r.addWarning(fmt.Sprintf("This run keeps no journal, so nothing was replayed from run %s and every agent runs live.", resumeFrom))
		}
		return nil
	}

	journal, err := workflowpkg.OpenJournal(r.journalPath, resumePath)
	if err == nil {
		return journal
	}
	if resumePath == "" {
		r.addWarning(fmt.Sprintf("The run has no journal and cannot be resumed: %v", err))
		r.journalPath = ""
		return nil
	}
	// OpenJournal fails as a whole when the resume journal cannot be read.
	// A resume journal that cannot be read must not cost the run its own
	// journal too, so the journal is opened again unseeded: that tells a
	// journal that cannot be read apart from one that cannot be written, and
	// keeps this run resumable itself.
	unseeded, unseededErr := workflowpkg.OpenJournal(r.journalPath, "")
	if unseededErr != nil {
		r.addWarning(fmt.Sprintf("The run has no journal, so nothing was replayed from run %s and this run cannot be resumed: %v", resumeFrom, unseededErr))
		r.journalPath = ""
		return nil
	}
	r.addWarning(fmt.Sprintf("Could not read the journal of run %s, so nothing was replayed and every agent runs live: %v", resumeFrom, err))
	return unseeded
}

// resumeJournalPath returns the journal of the run being resumed, which has to
// be a run of this session. For any other id it returns "" and warns that
// nothing was replayed: the journal loader reads a missing journal as a cold
// start, and the model would take the run for a resume.
func (r *workflowRun) resumeJournalPath(sessionDir string, runID string) string {
	// A run id names a directory in the session's workflows directory. One
	// with path parts would read a journal from somewhere else.
	if !toolpkg.ValidWorkflowRunID(runID) {
		r.addWarning(fmt.Sprintf("%q is not a workflow run id, so nothing was replayed and every agent runs live.", runID))
		return ""
	}
	if strings.TrimSpace(sessionDir) == "" {
		r.addWarning(fmt.Sprintf("There is no session directory to find run %s in, so nothing was replayed and every agent runs live.", runID))
		return ""
	}
	path := filepath.Join(workflowRunDir(sessionDir, runID), workflowJournalFileName)
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		r.addWarning(fmt.Sprintf("Run %s has no journal in this session, so nothing was replayed and every agent runs live.", runID))
		return ""
	}
	// Any other failure to reach the journal is openJournal's to report.
	return path
}

func (r *workflowRun) execute(ctx context.Context, opts workflowpkg.Options) {
	defer r.cancel()
	result, err := workflowpkg.Run(ctx, opts)
	// Closed before the run is marked finished, so a failure to flush the
	// journal reaches the run's warnings.
	closeErr := opts.Journal.Close()
	r.finish(result, err, closeErr)
}

// finish records how the run ended, writes result.json, and tells everyone
// waiting. A run whose script returned is completed even when some of its
// agents failed: the counts show the failures, and the script decides what
// they mean.
func (r *workflowRun) finish(result workflowpkg.Result, runErr error, closeErr error) {
	r.mu.Lock()
	if len(result.Agents) > 0 {
		r.agents = r.agents[:0]
		for _, record := range result.Agents {
			r.agents = append(r.agents, newWorkflowAgentEntry(record))
		}
	}
	r.warnings = append(r.warnings, result.Warnings...)
	if closeErr != nil {
		r.warnings = append(r.warnings, fmt.Sprintf("The journal may be missing results, so resuming this run could run those agents again: %v", closeErr))
	}
	switch {
	case runErr == nil:
		r.status = toolpkg.WorkflowStatusCompleted
		r.result = result.Value
	case errors.Is(runErr, workflowpkg.ErrStopped):
		r.status = toolpkg.WorkflowStatusStopped
	default:
		r.status = toolpkg.WorkflowStatusFailed
		r.err = runErr.Error()
	}
	r.completedAt = time.Now()
	// Written under the lock, so nobody sees the final status before
	// result_path holds it. Nothing else changes the run any more, and a
	// status call waits no longer than one file write.
	if err := r.writeResultFileLocked(); err != nil {
		r.warnings = append(r.warnings, fmt.Sprintf("Could not save the result, so it cannot be read once the run leaves memory: %v", err))
		r.resultPath = ""
	}
	r.mu.Unlock()

	// The final update goes out before waiters wake, so whoever saw the run
	// end can count on the TUI having been told.
	r.emitUpdate()
	close(r.done)
	time.AfterFunc(workflowRunRetention, func() { evictWorkflowRun(r) })
}

// writeResultFileLocked writes the final snapshot, with the whole result, to
// result.json. It goes through a temporary file and a rename, so a reader
// never sees half a file. CreateTemp makes the file 0600, private like the
// agents' output it holds.
func (r *workflowRun) writeResultFileLocked() error {
	if r.resultPath == "" {
		return nil
	}
	encoded, err := json.MarshalIndent(r.snapshotLocked(time.Now()), "", "  ")
	if err != nil {
		return fmt.Errorf("encode the result: %w", err)
	}
	temp, err := os.CreateTemp(filepath.Dir(r.resultPath), workflowResultFileName+".*.tmp")
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	_, writeErr := temp.Write(encoded)
	closeErr := temp.Close()
	if err := errors.Join(writeErr, closeErr); err != nil {
		return errors.Join(err, os.Remove(tempPath))
	}
	if err := os.Rename(tempPath, r.resultPath); err != nil {
		return errors.Join(err, os.Remove(tempPath))
	}
	return nil
}

func (r *workflowRun) stop() {
	r.cancel()
}

func (r *workflowRun) finished() bool {
	select {
	case <-r.done:
		return true
	default:
		return false
	}
}

func (r *workflowRun) addWarning(warning string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.warnings = append(r.warnings, warning)
}

// applyEvent is the run's OnEvent. The workflow package calls it from the
// goroutine running the script, while status calls read the run from others.
func (r *workflowRun) applyEvent(event workflowpkg.Event) {
	r.mu.Lock()
	switch event.Kind {
	case workflowpkg.EventPhase:
		r.notePhaseLocked(event.Phase, event.Workflow)
		r.currentPhase = event.Phase
	case workflowpkg.EventLog:
		line := event.Message
		if event.Workflow != "" {
			line = event.Workflow + ": " + line
		}
		r.logs = append(r.logs, line)
		if len(r.logs) > workflowMaxLogs {
			r.logs = r.logs[len(r.logs)-workflowMaxLogs:]
		}
	case workflowpkg.EventAgent:
		entry := newWorkflowAgentEntry(event.Agent)
		// Calls are numbered from 1 in the order the script made them, and
		// a call's first event comes before any later call's, so a new
		// index is always the next one.
		if index := event.Agent.Index - 1; index >= 0 && index < len(r.agents) {
			r.agents[index] = entry
		} else {
			r.agents = append(r.agents, entry)
		}
		// agent()'s phase option names a phase that phase() never started.
		if event.Agent.Phase != "" {
			r.notePhaseLocked(event.Agent.Phase, event.Agent.Workflow)
		}
	}
	r.mu.Unlock()
	r.requestEmit()
}

// notePhaseLocked adds a phase the first time it is seen, keeping the order
// meta.phases declared and the order the script reached the rest.
func (r *workflowRun) notePhaseLocked(title string, workflow string) {
	for _, phase := range r.phases {
		if phase.Title == title && phase.Workflow == workflow {
			return
		}
	}
	r.phases = append(r.phases, toolpkg.WorkflowPhase{Title: title, Workflow: workflow})
}

func newWorkflowAgentEntry(record workflowpkg.AgentRecord) workflowAgentEntry {
	output := record.Text
	if len(record.Structured) > 0 {
		output = string(record.Structured)
	}
	record.Text = ""
	record.Structured = nil
	record.Error = toolpkg.TruncateWorkflowText(record.Error, toolpkg.WorkflowAgentPreviewRunes)
	return workflowAgentEntry{
		record:  record,
		preview: toolpkg.TruncateWorkflowText(output, toolpkg.WorkflowAgentPreviewRunes),
	}
}

func (e workflowAgentEntry) snapshot(now time.Time) toolpkg.WorkflowAgentSnapshot {
	var duration time.Duration
	switch {
	case e.record.StartedAt.IsZero():
		// Queued, replayed, or stopped before it started: it never ran.
	case e.record.CompletedAt.IsZero():
		duration = now.Sub(e.record.StartedAt)
	default:
		duration = e.record.CompletedAt.Sub(e.record.StartedAt)
	}
	return toolpkg.WorkflowAgentSnapshot{
		Index:          e.record.Index,
		Label:          e.record.Label,
		Phase:          e.record.Phase,
		Workflow:       e.record.Workflow,
		Status:         string(e.record.Status),
		Error:          e.record.Error,
		DurationMs:     duration.Milliseconds(),
		AgentID:        e.record.AgentID,
		TranscriptPath: e.record.TranscriptPath,
		OutputPreview:  e.preview,
	}
}

func (r *workflowRun) snapshot() toolpkg.WorkflowRunSnapshot {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.snapshotLocked(time.Now())
}

func (r *workflowRun) snapshotLocked(now time.Time) toolpkg.WorkflowRunSnapshot {
	snapshot := toolpkg.WorkflowRunSnapshot{
		Status:       r.status,
		RunID:        r.id,
		Name:         r.name,
		Description:  r.description,
		CurrentPhase: r.currentPhase,
		Phases:       slices.Clone(r.phases),
		Logs:         slices.Clone(r.logs),
		Result:       r.result,
		Error:        r.err,
		Warnings:     slices.Clone(r.warnings),
		ScriptPath:   r.scriptPath,
		JournalPath:  r.journalPath,
		ResumedFrom:  r.resumedFrom,
		StartedAt:    r.startedAt,
		CompletedAt:  r.completedAt,
		Agents:       make([]toolpkg.WorkflowAgentSnapshot, 0, len(r.agents)),
	}
	// result.json is written only when the run ends, so a running run has
	// no result path to offer yet.
	if r.status != toolpkg.WorkflowStatusRunning {
		snapshot.ResultPath = r.resultPath
	}
	end := now
	if !r.completedAt.IsZero() {
		end = r.completedAt
	}
	snapshot.DurationMs = end.Sub(r.startedAt).Milliseconds()

	snapshot.Counts.Agents = len(r.agents)
	for _, entry := range r.agents {
		snapshot.Agents = append(snapshot.Agents, entry.snapshot(now))
		snapshot.TotalCostUSD += entry.record.CostUSD
		snapshot.InputTokens += entry.record.InputTokens
		snapshot.OutputTokens += entry.record.OutputTokens
		switch entry.record.Status {
		case workflowpkg.AgentQueued:
			snapshot.Counts.Queued++
		case workflowpkg.AgentRunning:
			snapshot.Counts.Running++
		case workflowpkg.AgentSucceeded:
			snapshot.Counts.Succeeded++
		case workflowpkg.AgentCached:
			snapshot.Counts.Cached++
		case workflowpkg.AgentFailed:
			snapshot.Counts.Failed++
		case workflowpkg.AgentStopped:
			snapshot.Counts.Stopped++
		}
	}
	return snapshot
}

func (r *workflowRun) launchResult() toolpkg.WorkflowLaunchResult {
	r.mu.Lock()
	defer r.mu.Unlock()
	return toolpkg.WorkflowLaunchResult{
		Status:      "launched",
		RunID:       r.id,
		Name:        r.name,
		Description: r.description,
		ScriptPath:  r.scriptPath,
		JournalPath: r.journalPath,
		ResumedFrom: r.resumedFrom,
		Warnings:    slices.Clone(r.warnings),
		Message: fmt.Sprintf("Workflow %q is running in the background as run %s. A <task-notification> arrives when it finishes. Meanwhile, check on it with workflow_status (wait_ms waits for it to finish) or stop it with workflow_stop.",
			r.name, r.id),
	}
}

// requestEmit sends a workflow_updated event now if none went out in the
// last interval, or else once the interval is up. The event is built when it
// is sent, so it carries every change made while it waited.
func (r *workflowRun) requestEmit() {
	if r.bridge == nil {
		return
	}
	r.mu.Lock()
	if r.emitScheduled {
		r.mu.Unlock()
		return
	}
	r.emitScheduled = true
	delay := max(workflowEmitInterval-time.Since(r.lastEmitAt), 0)
	r.mu.Unlock()

	// Sent from a timer goroutine even without a delay, so writing to the
	// TUI never holds up the script.
	time.AfterFunc(delay, func() {
		r.mu.Lock()
		r.emitScheduled = false
		r.lastEmitAt = time.Now()
		r.mu.Unlock()
		r.emitUpdate()
	})
}

// emitUpdate sends the run's current state to the TUI. Once a final state
// has gone out nothing more is sent, so a coalesced update that was built
// while the run was going cannot arrive after it and make the TUI show a
// finished run as running.
func (r *workflowRun) emitUpdate() {
	r.emit(false)
}

// announce sends the run's current state even when its final state has
// already gone out. The TUI forgets runs when it loads a conversation, so a
// session that becomes active again re-announces the runs it owns.
func (r *workflowRun) announce() {
	r.emit(true)
}

func (r *workflowRun) emit(again bool) {
	if r.bridge == nil {
		return
	}
	r.emitMu.Lock()
	defer r.emitMu.Unlock()
	if r.finalEmitted && !again {
		return
	}
	// The TUI files every update under the session it shows. A run whose
	// session was left keeps going, and workflow_status still reports it,
	// but it stays out of the session that replaced its own. A final state
	// that could not be sent then stays unsent, so announce can deliver it,
	// and its notification, once the session is back.
	if r.ownerSessionID != activeSessionID() {
		return
	}
	snapshot := r.snapshot()
	if snapshot.Status != toolpkg.WorkflowStatusRunning {
		r.finalEmitted = true
	}
	_ = r.bridge.Emit(ipc.EventWorkflowUpdated, workflowUpdatedPayload(snapshot))
}

// announceWorkflowRuns re-sends the state of every run the session owns.
func announceWorkflowRuns(sessionID string) {
	workflowRunsMu.RLock()
	owned := make([]*workflowRun, 0, len(workflowRuns))
	for _, run := range workflowRuns {
		if run.ownerSessionID == sessionID {
			owned = append(owned, run)
		}
	}
	workflowRunsMu.RUnlock()
	for _, run := range owned {
		run.announce()
	}
}

// workflowUpdatedPayload trims a snapshot for the TUI: at most 200 agents,
// running ones first, with short previews.
func workflowUpdatedPayload(snapshot toolpkg.WorkflowRunSnapshot) ipc.WorkflowUpdatedPayload {
	payload := ipc.WorkflowUpdatedPayload{
		RunID:        snapshot.RunID,
		Name:         snapshot.Name,
		Description:  snapshot.Description,
		Status:       snapshot.Status,
		CurrentPhase: snapshot.CurrentPhase,
		AgentCount:   snapshot.Counts.Agents,
		Running:      snapshot.Counts.Running,
		Queued:       snapshot.Counts.Queued,
		Succeeded:    snapshot.Counts.Succeeded,
		Cached:       snapshot.Counts.Cached,
		Failed:       snapshot.Counts.Failed,
		Stopped:      snapshot.Counts.Stopped,
		Logs:         snapshot.Logs[max(len(snapshot.Logs)-workflowUpdateLogs, 0):],
		Error:        snapshot.Error,
		Warnings:     snapshot.Warnings,
		ScriptPath:   snapshot.ScriptPath,
		JournalPath:  snapshot.JournalPath,
		ResultPath:   snapshot.ResultPath,
		StartedAt:    snapshot.StartedAt,
		CompletedAt:  snapshot.CompletedAt,
		DurationMs:   snapshot.DurationMs,
		TotalCostUSD: snapshot.TotalCostUSD,
		InputTokens:  snapshot.InputTokens,
		OutputTokens: snapshot.OutputTokens,
	}
	for _, phase := range snapshot.Phases {
		payload.Phases = append(payload.Phases, ipc.WorkflowPhasePayload{
			Title:    phase.Title,
			Detail:   phase.Detail,
			Workflow: phase.Workflow,
		})
	}
	for _, agent := range toolpkg.SelectWorkflowAgents(snapshot.Agents, toolpkg.MaxWorkflowDisplayAgents) {
		payload.Agents = append(payload.Agents, ipc.WorkflowAgentPayload{
			Index:          agent.Index,
			Label:          agent.Label,
			Phase:          agent.Phase,
			Workflow:       agent.Workflow,
			Status:         agent.Status,
			Error:          toolpkg.TruncateWorkflowText(agent.Error, workflowUpdatePreviewRunes),
			DurationMs:     agent.DurationMs,
			AgentID:        agent.AgentID,
			TranscriptPath: agent.TranscriptPath,
			OutputPreview:  toolpkg.TruncateWorkflowText(agent.OutputPreview, workflowUpdatePreviewRunes),
		})
	}
	if len(snapshot.Result) > 0 {
		payload.ResultPreview = toolpkg.TruncateWorkflowText(string(snapshot.Result), workflowUpdateResultRunes)
	}
	return payload
}

// workflowAgentRunner adapts the engine's child-agent runner to the workflow
// package. Every call runs synchronously: the script owns scheduling, so
// handing calls to the background agent machinery as well would schedule
// the same work twice.
func workflowAgentRunner(runner toolpkg.AgentRunner) workflowpkg.AgentRunner {
	return func(ctx context.Context, call workflowpkg.AgentCall) (workflowpkg.AgentResult, error) {
		result, err := runner(ctx, toolpkg.AgentRunRequest{
			Description:     call.Label,
			Prompt:          call.Prompt,
			SubagentType:    call.AgentType,
			Model:           call.Model,
			ReasoningEffort: call.Effort,
			OutputSchema:    call.Schema,
			ForWorkflow:     true,
			Background:      false,
		})
		agentResult := workflowpkg.AgentResult{
			Text:           result.Summary,
			Structured:     result.Structured,
			AgentID:        strings.TrimSpace(result.AgentID),
			SessionID:      strings.TrimSpace(result.SessionID),
			TranscriptPath: strings.TrimSpace(result.TranscriptPath),
			InputTokens:    result.InputTokens,
			OutputTokens:   result.OutputTokens,
			CostUSD:        result.TotalCostUSD,
		}
		// A child that failed partway comes back with what it spent, which
		// still counts against the budget; its text is not an answer.
		if err != nil {
			agentResult.Text = ""
			agentResult.Structured = nil
			return agentResult, err
		}
		// A child that did not complete has to fail its call, or the script
		// would read an error message as the agent's answer. Its ids and
		// usage still go back, so the failure's transcript is reachable and
		// its tokens count against the budget.
		if status := strings.TrimSpace(result.Status); status != "completed" {
			reason := strings.TrimSpace(result.Error)
			if reason == "" {
				reason = fmt.Sprintf("child agent ended with status %q", status)
			}
			return agentResult, errors.New(reason)
		}
		return agentResult, nil
	}
}

// checkWorkflowAgentCall validates an agent() call's options and normalizes
// them. It runs before the call is keyed for resume, so two spellings of one
// option replay the same result.
func checkWorkflowAgentCall(modelState *ActiveModelState) func(*workflowpkg.AgentCall) error {
	return func(call *workflowpkg.AgentCall) error {
		if strings.TrimSpace(call.AgentType) == "" {
			// NormalizeSubagentType reads an empty type as Explore, the agent
			// tool's default; a workflow step defaults to the type that can
			// do any kind of work.
			call.AgentType = generalPurposeSubagentType
		} else {
			agentType := toolpkg.NormalizeSubagentType(call.AgentType)
			if !toolpkg.IsSupportedSubagentType(agentType) {
				return fmt.Errorf("agentType %q is not supported; use general-purpose, Explore, or verification", call.AgentType)
			}
			call.AgentType = agentType
		}
		if call.Isolation == "worktree" {
			return errors.New("isolation 'worktree' is not supported in workflows: a worktree agent changes the working directory of the whole engine, which is unsafe while a workflow runs alongside the conversation")
		}
		if call.Model != "" {
			model, err := checkSubagentModelOverride(call.Model, modelState)
			if err != nil {
				return err
			}
			call.Model = model
		}
		if call.Effort != "" {
			effort, err := normalizeReasoningEffortOverride(call.Effort)
			if err != nil {
				return err
			}
			call.Effort = effort
		}
		return nil
	}
}

// savedWorkflowDirs returns where saved workflows live: .nami/workflows at
// the project's git root, and the user's workflows directory. Outside a git
// repository there is no project directory, so a stray .nami directory in
// some parent cannot inject workflows.
func savedWorkflowDirs(cwd string) (projectDir string, userDir string) {
	if root := config.FindProjectRoot(cwd); root != "" {
		projectDir = filepath.Join(root, ".nami", "workflows")
	}
	return projectDir, config.GlobalWorkflowDir()
}

func loadSavedWorkflows(cwd string) ([]workflowpkg.Saved, error) {
	projectDir, userDir := savedWorkflowDirs(cwd)
	return workflowpkg.LoadSaved(projectDir, userDir)
}

// loadWorkflowRef resolves a saved workflow's name or a script path, for the
// workflow tool and for workflow() calls inside a script. A relative path
// resolves against cwd.
func loadWorkflowRef(ref workflowpkg.Ref, cwd string) (workflowpkg.Script, error) {
	if path := strings.TrimSpace(ref.ScriptPath); path != "" {
		if !filepath.IsAbs(path) {
			path = filepath.Join(cwd, path)
		}
		return workflowpkg.ReadScript(path)
	}

	name := strings.TrimSpace(ref.Name)
	saved, loadErr := loadSavedWorkflows(cwd)
	if found, ok := workflowpkg.FindSaved(saved, name); ok {
		return workflowpkg.ReadScript(found.Path)
	}
	message := fmt.Sprintf("no saved workflow is named %q", name)
	if len(saved) > 0 {
		names := make([]string, 0, len(saved))
		for _, entry := range saved {
			names = append(names, entry.Name)
		}
		message += "; saved workflows are " + strings.Join(names, ", ")
	}
	if loadErr != nil {
		// The workflow asked for may be one of those that failed to load.
		return workflowpkg.Script{}, fmt.Errorf("%s; some could not be loaded: %w", message, loadErr)
	}
	return workflowpkg.Script{}, errors.New(message)
}

// waitForWorkflowRun blocks until run finishes, waitMs passes, or ctx ends.
func waitForWorkflowRun(ctx context.Context, run *workflowRun, waitMs int) error {
	if waitMs <= 0 {
		return nil
	}
	timer := time.NewTimer(time.Duration(waitMs) * time.Millisecond)
	defer timer.Stop()
	select {
	case <-run.done:
		return nil
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// lookupWorkflowRunStatus reports on a run, from memory while it is there and
// from its result.json after it has been evicted.
func lookupWorkflowRunStatus(ctx context.Context, sessionDir string, req toolpkg.WorkflowStatusRequest) (toolpkg.WorkflowRunSnapshot, error) {
	run, ok := findWorkflowRun(req.RunID)
	if !ok {
		return readWorkflowRunResult(sessionDir, req.RunID)
	}
	if err := waitForWorkflowRun(ctx, run, req.WaitMs); err != nil {
		return run.snapshot(), err
	}
	return run.snapshot(), nil
}

// stopWorkflowRun stops a run and waits for it to settle. Stopping a run that
// has finished is not an error: the caller gets its final state.
func stopWorkflowRun(ctx context.Context, sessionDir string, req toolpkg.WorkflowStopRequest) (toolpkg.WorkflowRunSnapshot, error) {
	run, ok := findWorkflowRun(req.RunID)
	if !ok {
		return readWorkflowRunResult(sessionDir, req.RunID)
	}
	run.stop()
	if err := waitForWorkflowRun(ctx, run, req.WaitMs); err != nil {
		return run.snapshot(), err
	}
	return run.snapshot(), nil
}

// readWorkflowRunResult reads the snapshot a finished run left in
// result.json.
func readWorkflowRunResult(sessionDir string, runID string) (toolpkg.WorkflowRunSnapshot, error) {
	runID = strings.TrimSpace(runID)
	if !toolpkg.ValidWorkflowRunID(runID) || strings.TrimSpace(sessionDir) == "" {
		return toolpkg.WorkflowRunSnapshot{}, fmt.Errorf("unknown workflow run %q", runID)
	}
	runDir := workflowRunDir(sessionDir, runID)
	data, err := os.ReadFile(filepath.Join(runDir, workflowResultFileName))
	if errors.Is(err, os.ErrNotExist) {
		// A journal without a result is a run this process never finished,
		// such as one cut off when nami exited.
		if _, statErr := os.Stat(filepath.Join(runDir, workflowJournalFileName)); statErr == nil {
			return toolpkg.WorkflowRunSnapshot{}, fmt.Errorf("workflow run %s is not running and saved no result, so it was interrupted; pass it as resume_from_run_id to replay the agents it finished", runID)
		}
		return toolpkg.WorkflowRunSnapshot{}, fmt.Errorf("unknown workflow run %q", runID)
	}
	if err != nil {
		return toolpkg.WorkflowRunSnapshot{}, fmt.Errorf("read the result of workflow run %s: %w", runID, err)
	}
	var snapshot toolpkg.WorkflowRunSnapshot
	if err := json.Unmarshal(data, &snapshot); err != nil {
		return toolpkg.WorkflowRunSnapshot{}, fmt.Errorf("decode the result of workflow run %s: %w", runID, err)
	}
	// The file is indented for people who open it, which re-indented the
	// result as well. Compacted again, the result is what the run itself
	// reported, and it is cut for display at the same place.
	if len(snapshot.Result) > 0 {
		var compact bytes.Buffer
		if err := json.Compact(&compact, snapshot.Result); err != nil {
			return toolpkg.WorkflowRunSnapshot{}, fmt.Errorf("decode the result of workflow run %s: %w", runID, err)
		}
		snapshot.Result = compact.Bytes()
	}
	return snapshot, nil
}

// handleWorkflowStopMessage stops a run the user picked in the workflows
// dialog. It does not wait for the run to settle, since the main loop would
// stall meanwhile; the run's final workflow_updated reports how it ended. A
// malformed request becomes a notice rather than ending the session, so only
// a failure to reach the TUI is returned.
func handleWorkflowStopMessage(bridge *ipc.Bridge, raw json.RawMessage) error {
	var payload ipc.WorkflowStopPayload
	if err := json.Unmarshal(raw, &payload); err != nil {
		return bridge.EmitNotice(fmt.Sprintf("Stop workflow failed: the request could not be read: %v", err))
	}
	runID := strings.TrimSpace(payload.RunID)
	if runID == "" {
		return bridge.EmitNotice("Stop workflow requires run_id.")
	}
	run, ok := findWorkflowRun(runID)
	if !ok {
		return bridge.EmitNotice(fmt.Sprintf("Stop workflow failed: no workflow run %q is in memory.", runID))
	}
	run.stop()
	return nil
}
