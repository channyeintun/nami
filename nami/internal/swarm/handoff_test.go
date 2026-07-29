package swarm

import (
	"strings"
	"testing"
	"time"

	"github.com/channyeintun/nami/internal/session"
)

func testStore(t *testing.T) (*session.Store, string) {
	t.Helper()
	return session.NewStore(t.TempDir()), "session-under-test"
}

func sampleHandoff() Handoff {
	return Handoff{
		SourceRole:   "coder",
		TargetRole:   "reviewer",
		Summary:      "implemented the parser",
		Verification: "go test ./...",
	}
}

func TestNormalizeHandoffStatus(t *testing.T) {
	cases := map[string]HandoffStatus{
		"":            HandoffStatusPending,
		"pending":     HandoffStatusPending,
		" ACKED ":     HandoffStatusAcked,
		"in_progress": HandoffStatusInProgress,
		"in-progress": HandoffStatusInProgress,
		"completed":   HandoffStatusCompleted,
		"blocked":     HandoffStatusBlocked,
		"superseded":  HandoffStatusSuperseded,
		"nonsense":    HandoffStatus(""),
	}
	for input, want := range cases {
		if got := NormalizeHandoffStatus(input); got != want {
			t.Errorf("NormalizeHandoffStatus(%q) = %q, want %q", input, got, want)
		}
	}
	if IsValidHandoffStatus("nonsense") {
		t.Error("IsValidHandoffStatus accepted an unknown status")
	}
	if !IsValidHandoffStatus(HandoffStatusCompleted) {
		t.Error("IsValidHandoffStatus rejected a known status")
	}
}

func TestNewHandoffIDIsUnique(t *testing.T) {
	seen := make(map[string]struct{}, 100)
	for range 100 {
		id := NewHandoffID()
		if !strings.HasPrefix(id, "handoff-") {
			t.Fatalf("id = %q, want a handoff- prefix", id)
		}
		if _, ok := seen[id]; ok {
			t.Fatalf("id %q was generated twice", id)
		}
		seen[id] = struct{}{}
	}
}

func TestPrepareHandoffNormalizesFields(t *testing.T) {
	handoff, err := PrepareHandoff(Handoff{
		SourceRole:   "  Coder ",
		TargetRole:   "REVIEWER",
		Summary:      "  did the thing  ",
		ChangedFiles: []string{" a.go ", "a.go", "  ", "b.go"},
		CommandsRun:  []string{"go test", "go test"},
		Risks:        []string{" flaky "},
		Status:       "in-progress",
	})
	if err != nil {
		t.Fatalf("PrepareHandoff: %v", err)
	}
	if handoff.SourceRole != "coder" || handoff.TargetRole != "reviewer" {
		t.Fatalf("roles = %q -> %q", handoff.SourceRole, handoff.TargetRole)
	}
	if handoff.Summary != "did the thing" {
		t.Fatalf("summary = %q", handoff.Summary)
	}
	if len(handoff.ChangedFiles) != 2 || handoff.ChangedFiles[0] != "a.go" || handoff.ChangedFiles[1] != "b.go" {
		t.Fatalf("changed files = %#v, want deduplicated and trimmed", handoff.ChangedFiles)
	}
	if len(handoff.CommandsRun) != 1 {
		t.Fatalf("commands = %#v, want deduplicated", handoff.CommandsRun)
	}
	if handoff.Status != HandoffStatusInProgress {
		t.Fatalf("status = %q", handoff.Status)
	}
	if handoff.ID == "" || handoff.ArtifactID != handoff.ID {
		t.Fatalf("id = %q, artifact = %q", handoff.ID, handoff.ArtifactID)
	}
	if len(handoff.History) != 1 || handoff.History[0].Status != HandoffStatusInProgress {
		t.Fatalf("history = %#v", handoff.History)
	}
}

func TestPrepareHandoffRequiresCoreFields(t *testing.T) {
	cases := map[string]Handoff{
		"missing source":  {TargetRole: "reviewer", Summary: "s"},
		"missing target":  {SourceRole: "coder", Summary: "s"},
		"missing summary": {SourceRole: "coder", TargetRole: "reviewer"},
	}
	for name, handoff := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := PrepareHandoff(handoff); err == nil {
				t.Fatalf("PrepareHandoff(%+v) succeeded, want an error", handoff)
			}
		})
	}
}

func TestPrepareHandoffFallsBackToPendingForUnknownStatus(t *testing.T) {
	handoff, err := PrepareHandoff(Handoff{SourceRole: "a", TargetRole: "b", Summary: "s", Status: "nonsense"})
	if err != nil {
		t.Fatalf("PrepareHandoff: %v", err)
	}
	if handoff.Status != HandoffStatusPending {
		t.Fatalf("status = %q, want pending", handoff.Status)
	}
}

func resolvedTestSpec(t *testing.T) ResolvedSpec {
	t.Helper()
	spec, err := Spec{Roles: []RoleSpec{
		{
			Name:    "coder",
			Purpose: "writes code",
			Handoff: HandoffSpec{Required: true, Targets: []string{"reviewer"}, RequiredFields: []string{"summary", "verification", "changed_files"}},
		},
		{Name: "reviewer", Purpose: "reviews code"},
		{Name: "architect", Purpose: "plans"},
	}}.Resolve("/repo", "/repo/.nami/swarm.json")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	return spec
}

func TestValidateHandoffAgainstSpec(t *testing.T) {
	spec := resolvedTestSpec(t)

	valid := sampleHandoff()
	valid.ChangedFiles = []string{"parser.go"}
	if err := ValidateHandoffAgainstSpec(spec, valid); err != nil {
		t.Fatalf("ValidateHandoffAgainstSpec: %v", err)
	}

	missingField := sampleHandoff()
	err := ValidateHandoffAgainstSpec(spec, missingField)
	if err == nil || !strings.Contains(err.Error(), "changed_files") {
		t.Fatalf("error = %v, want the missing changed_files field", err)
	}

	wrongTarget := valid
	wrongTarget.TargetRole = "architect"
	if err := ValidateHandoffAgainstSpec(spec, wrongTarget); err == nil {
		t.Fatal("handoff to a role outside the allowed targets was accepted")
	}

	unknownSource := valid
	unknownSource.SourceRole = "ghost"
	if err := ValidateHandoffAgainstSpec(spec, unknownSource); err == nil {
		t.Fatal("handoff from an unknown role was accepted")
	}

	unknownTarget := valid
	unknownTarget.TargetRole = "ghost"
	if err := ValidateHandoffAgainstSpec(spec, unknownTarget); err == nil {
		t.Fatal("handoff to an unknown role was accepted")
	}
}

func TestValidateHandoffAgainstSpecSkipsUnconstrainedRoles(t *testing.T) {
	spec := resolvedTestSpec(t)
	handoff := Handoff{SourceRole: "reviewer", TargetRole: "coder"}
	if err := ValidateHandoffAgainstSpec(spec, handoff); err != nil {
		t.Fatalf("ValidateHandoffAgainstSpec: %v", err)
	}
}

func TestMissingRequiredFields(t *testing.T) {
	handoff := Handoff{Summary: "s"}
	missing := missingRequiredFields(handoff, []HandoffField{
		HandoffFieldSummary,
		HandoffFieldChangedFiles,
		HandoffFieldCommandsRun,
		HandoffFieldVerification,
		HandoffFieldRisks,
		HandoffFieldNextAction,
	})
	if len(missing) != 5 {
		t.Fatalf("missing = %v, want everything except summary", missing)
	}

	complete := Handoff{
		Summary:      "s",
		ChangedFiles: []string{"a"},
		CommandsRun:  []string{"go test"},
		Verification: "passed",
		Risks:        []string{"none"},
		NextAction:   "merge",
	}
	if got := missingRequiredFields(complete, []HandoffField{HandoffFieldSummary, HandoffFieldRisks}); len(got) != 0 {
		t.Fatalf("missing = %v, want none", got)
	}
}

func TestRenderHandoffMarkdown(t *testing.T) {
	created := time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC)
	handoff := Handoff{
		ID:           "handoff-1",
		SourceRole:   "coder",
		TargetRole:   "reviewer",
		Summary:      "did the work",
		ChangedFiles: []string{"a.go", "  "},
		CommandsRun:  []string{"go test ./..."},
		Verification: "tests pass",
		Risks:        []string{"none"},
		NextAction:   "review",
		StatusNote:   "ready",
		Status:       HandoffStatusPending,
		CreatedAt:    created,
		UpdatedAt:    created,
		History:      []HandoffStatusEntry{{Status: HandoffStatusPending, Note: "created", At: created}},
	}

	markdown := RenderHandoffMarkdown(handoff)
	for _, want := range []string{
		"# Handoff",
		"- ID: handoff-1",
		"- From: coder",
		"- To: reviewer",
		"- Status: pending",
		"2026-03-04T05:06:07Z",
		"## Summary",
		"## Changed Files",
		"- a.go",
		"## Commands Run",
		"## Verification",
		"## Risks",
		"## Next Action",
		"## Status Note",
		"## History",
		": pending - created",
	} {
		if !strings.Contains(markdown, want) {
			t.Errorf("markdown missing %q:\n%s", want, markdown)
		}
	}
	if !strings.HasSuffix(markdown, "\n") {
		t.Error("markdown should end with a newline")
	}
}

func TestRenderHandoffMarkdownOmitsEmptySections(t *testing.T) {
	markdown := RenderHandoffMarkdown(Handoff{ID: "h", SourceRole: "a", TargetRole: "b", Status: HandoffStatusPending})
	for _, unwanted := range []string{"## Summary", "## Changed Files", "## History"} {
		if strings.Contains(markdown, unwanted) {
			t.Errorf("markdown should omit %q:\n%s", unwanted, markdown)
		}
	}
}

func TestUpsertAndListHandoffs(t *testing.T) {
	store, sessionID := testStore(t)

	stored, err := UpsertHandoff(store, sessionID, sampleHandoff())
	if err != nil {
		t.Fatalf("UpsertHandoff: %v", err)
	}
	if stored.Status != HandoffStatusPending {
		t.Fatalf("status = %q, want pending", stored.Status)
	}

	listed, err := ListHandoffs(store, sessionID, "reviewer", nil)
	if err != nil {
		t.Fatalf("ListHandoffs: %v", err)
	}
	if len(listed) != 1 || listed[0].ID != stored.ID {
		t.Fatalf("listed = %+v", listed)
	}

	// The source role sees its own handoffs too.
	fromSource, err := ListHandoffs(store, sessionID, "coder", nil)
	if err != nil {
		t.Fatalf("ListHandoffs: %v", err)
	}
	if len(fromSource) != 1 {
		t.Fatalf("source-side listing = %+v", fromSource)
	}

	other, err := ListHandoffs(store, sessionID, "architect", nil)
	if err != nil {
		t.Fatalf("ListHandoffs: %v", err)
	}
	if len(other) != 0 {
		t.Fatalf("unrelated role saw %+v", other)
	}

	byStatus, err := ListHandoffs(store, sessionID, "", []HandoffStatus{HandoffStatusCompleted})
	if err != nil {
		t.Fatalf("ListHandoffs: %v", err)
	}
	if len(byStatus) != 0 {
		t.Fatalf("status filter returned %+v", byStatus)
	}
}

func TestUpsertHandoffUpdatesInPlace(t *testing.T) {
	store, sessionID := testStore(t)
	stored, err := UpsertHandoff(store, sessionID, sampleHandoff())
	if err != nil {
		t.Fatalf("UpsertHandoff: %v", err)
	}

	updated := stored
	updated.Summary = "revised summary"
	if _, err := UpsertHandoff(store, sessionID, updated); err != nil {
		t.Fatalf("UpsertHandoff: %v", err)
	}

	inbox, err := LoadInbox(store, sessionID)
	if err != nil {
		t.Fatalf("LoadInbox: %v", err)
	}
	if len(inbox.Handoffs) != 1 {
		t.Fatalf("inbox holds %d handoffs, want 1", len(inbox.Handoffs))
	}
	if inbox.Handoffs[0].Summary != "revised summary" {
		t.Fatalf("summary = %q", inbox.Handoffs[0].Summary)
	}
	if !inbox.Handoffs[0].CreatedAt.Equal(stored.CreatedAt) {
		t.Fatal("CreatedAt should survive an update")
	}
}

func TestUpdateHandoffStatus(t *testing.T) {
	store, sessionID := testStore(t)
	stored, err := UpsertHandoff(store, sessionID, sampleHandoff())
	if err != nil {
		t.Fatalf("UpsertHandoff: %v", err)
	}

	updated, err := UpdateHandoffStatus(store, sessionID, stored.ID, HandoffStatusCompleted, " merged ")
	if err != nil {
		t.Fatalf("UpdateHandoffStatus: %v", err)
	}
	if updated.Status != HandoffStatusCompleted || updated.StatusNote != "merged" {
		t.Fatalf("updated = %+v", updated)
	}
	if len(updated.History) < 2 {
		t.Fatalf("history = %+v, want the status change appended", updated.History)
	}

	if _, err := UpdateHandoffStatus(store, sessionID, "missing", HandoffStatusAcked, ""); err == nil {
		t.Fatal("updating an unknown handoff returned no error")
	}
	if _, err := UpdateHandoffStatus(store, sessionID, stored.ID, "nonsense", ""); err == nil {
		t.Fatal("updating to an invalid status returned no error")
	}
}

func TestDequeueHandoffsPolicies(t *testing.T) {
	store, sessionID := testStore(t)
	base := time.Now().UTC().Add(-time.Hour)
	for i, summary := range []string{"first", "second", "third"} {
		handoff := sampleHandoff()
		handoff.Summary = summary
		handoff.CreatedAt = base.Add(time.Duration(i) * time.Minute)
		if _, err := UpsertHandoff(store, sessionID, handoff); err != nil {
			t.Fatalf("UpsertHandoff: %v", err)
		}
	}

	fifo, err := DequeueHandoffs(store, sessionID, "reviewer", QueueFIFO)
	if err != nil {
		t.Fatalf("DequeueHandoffs: %v", err)
	}
	if len(fifo.Handoffs) != 1 || fifo.Handoffs[0].Summary != "first" {
		t.Fatalf("fifo = %+v, want the oldest handoff", fifo.Handoffs)
	}

	batch, err := DequeueHandoffs(store, sessionID, "reviewer", QueueBatchReview)
	if err != nil {
		t.Fatalf("DequeueHandoffs: %v", err)
	}
	if len(batch.Handoffs) != 3 {
		t.Fatalf("batch = %+v, want all pending handoffs", batch.Handoffs)
	}

	latest, err := DequeueHandoffs(store, sessionID, "reviewer", QueueLatestWins)
	if err != nil {
		t.Fatalf("DequeueHandoffs: %v", err)
	}
	if len(latest.Handoffs) != 1 || latest.Handoffs[0].Summary != "third" {
		t.Fatalf("latest = %+v, want the newest handoff", latest.Handoffs)
	}
	if len(latest.Superseded) != 2 {
		t.Fatalf("superseded = %+v, want the two older handoffs", latest.Superseded)
	}
	for _, superseded := range latest.Superseded {
		if superseded.Status != HandoffStatusSuperseded {
			t.Fatalf("superseded handoff has status %q", superseded.Status)
		}
	}

	// The superseded entries are persisted, so a later dequeue only sees the winner.
	after, err := DequeueHandoffs(store, sessionID, "reviewer", QueueBatchReview)
	if err != nil {
		t.Fatalf("DequeueHandoffs: %v", err)
	}
	if len(after.Handoffs) != 1 {
		t.Fatalf("remaining pending = %+v, want only the winner", after.Handoffs)
	}
}

func TestDequeueHandoffsRequiresRole(t *testing.T) {
	store, sessionID := testStore(t)
	if _, err := DequeueHandoffs(store, sessionID, "  ", QueueFIFO); err == nil {
		t.Fatal("DequeueHandoffs accepted a blank role")
	}
}

func TestDequeueHandoffsOnEmptyInbox(t *testing.T) {
	store, sessionID := testStore(t)
	result, err := DequeueHandoffs(store, sessionID, "reviewer", QueueFIFO)
	if err != nil {
		t.Fatalf("DequeueHandoffs: %v", err)
	}
	if len(result.Handoffs) != 0 {
		t.Fatalf("result = %+v, want nothing", result.Handoffs)
	}
}

func TestInboxOperationsRequireStore(t *testing.T) {
	if _, err := LoadInbox(nil, "session"); err == nil {
		t.Fatal("LoadInbox accepted a nil store")
	}
	if _, err := UpsertHandoff(nil, "session", sampleHandoff()); err == nil {
		t.Fatal("UpsertHandoff accepted a nil store")
	}
}

func TestMergeHistoryDropsRepeatedEntries(t *testing.T) {
	entry := HandoffStatusEntry{Status: HandoffStatusPending, Note: "created"}
	merged := mergeHistory([]HandoffStatusEntry{entry}, []HandoffStatusEntry{entry})
	if len(merged) != 1 {
		t.Fatalf("merged = %+v, want the duplicate dropped", merged)
	}

	changed := HandoffStatusEntry{Status: HandoffStatusCompleted, Note: "done"}
	merged = mergeHistory([]HandoffStatusEntry{entry}, []HandoffStatusEntry{changed})
	if len(merged) != 2 || merged[1].Status != HandoffStatusCompleted {
		t.Fatalf("merged = %+v, want the new entry appended", merged)
	}

	if got := mergeHistory(nil, []HandoffStatusEntry{entry}); len(got) != 1 {
		t.Fatalf("merged = %+v", got)
	}
	if got := mergeHistory([]HandoffStatusEntry{entry}, nil); len(got) != 1 {
		t.Fatalf("merged = %+v", got)
	}
}

func TestTrimUniqueListAndFirstNonEmpty(t *testing.T) {
	got := trimUniqueList([]string{" a ", "a", "", "  ", "b"})
	if len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Fatalf("trimUniqueList = %#v", got)
	}
	if firstNonEmpty("", "  ", "x", "y") != "x" {
		t.Fatal("firstNonEmpty should return the first non-blank value")
	}
	if firstNonEmpty("", "  ") != "" {
		t.Fatal("firstNonEmpty should return empty when nothing qualifies")
	}
}
