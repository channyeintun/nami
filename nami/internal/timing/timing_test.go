package timing

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func writeTimingLog(t *testing.T, records ...any) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "timings.ndjson")
	var builder strings.Builder
	for _, record := range records {
		data, err := json.Marshal(record)
		if err != nil {
			t.Fatalf("marshal record: %v", err)
		}
		builder.Write(data)
		builder.WriteByte('\n')
	}
	if err := os.WriteFile(path, []byte(builder.String()), 0o644); err != nil {
		t.Fatalf("write timing log: %v", err)
	}
	return path
}

func compactionRecord(turn int, status, reason, strategy string, tokensSaved int, durationMS int64) Record {
	return Record{
		Kind:       "compaction",
		Metric:     "compaction",
		TurnID:     turn,
		DurationMS: durationMS,
		Metadata: map[string]any{
			"status":       status,
			"reason":       reason,
			"strategy":     strategy,
			"tokens_saved": tokensSaved,
		},
	}
}

func TestCheckpointRecorderIgnoresRepeatedNames(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	recorder := NewCheckpointRecorder(start)

	if !recorder.MarkAt("first", start.Add(time.Second)) {
		t.Fatal("MarkAt returned false for a new checkpoint")
	}
	if recorder.MarkAt("first", start.Add(2*time.Second)) {
		t.Fatal("MarkAt overwrote an existing checkpoint")
	}
	if recorder.MarkAt("", start) {
		t.Fatal("MarkAt accepted an empty name")
	}

	snapshot := recorder.Snapshot()
	if !snapshot.StartedAt.Equal(start) {
		t.Fatalf("StartedAt = %v, want %v", snapshot.StartedAt, start)
	}
	if got := snapshot.Checkpoints["first"]; !got.Equal(start.Add(time.Second)) {
		t.Fatalf("checkpoint = %v, want the first mark", got)
	}

	// The snapshot is a copy: mutating it must not affect the recorder.
	snapshot.Checkpoints["injected"] = start
	if _, ok := recorder.Snapshot().Checkpoints["injected"]; ok {
		t.Fatal("Snapshot shares its map with the recorder")
	}
}

func TestCheckpointRecorderDefaultsZeroStart(t *testing.T) {
	recorder := NewCheckpointRecorder(time.Time{})
	if recorder.Snapshot().StartedAt.IsZero() {
		t.Fatal("a zero start time should fall back to now")
	}
}

func TestCheckpointRecorderIsSafeForConcurrentMarks(t *testing.T) {
	recorder := NewCheckpointRecorder(time.Now())
	var wg sync.WaitGroup
	for i := range 16 {
		wg.Go(func() {
			recorder.Mark(string(rune('a' + i)))
		})
	}
	wg.Wait()
	if got := len(recorder.Snapshot().Checkpoints); got != 16 {
		t.Fatalf("checkpoints = %d, want 16", got)
	}
}

func TestNilRecorderIsInert(t *testing.T) {
	var recorder *CheckpointRecorder
	if recorder.Mark("x") {
		t.Fatal("Mark on a nil recorder returned true")
	}
}

func TestLoggerAppendSnapshotWritesOneLine(t *testing.T) {
	dir := t.TempDir()
	logger := NewSessionLogger(dir)
	start := time.Date(2026, 5, 6, 7, 8, 9, 0, time.UTC)
	recorder := NewCheckpointRecorder(start)
	recorder.MarkAt("prepare", start.Add(150*time.Millisecond))
	recorder.MarkAt("finish", start.Add(400*time.Millisecond))

	if err := logger.AppendSnapshot("compaction", "compaction", "session-1", 3, recorder, map[string]any{"status": "completed"}); err != nil {
		t.Fatalf("AppendSnapshot: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dir, "timings.ndjson"))
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 1 {
		t.Fatalf("wrote %d lines, want 1", len(lines))
	}

	var record Record
	if err := json.Unmarshal([]byte(lines[0]), &record); err != nil {
		t.Fatalf("decode record: %v", err)
	}
	if record.SessionID != "session-1" || record.TurnID != 3 {
		t.Fatalf("record = %+v", record)
	}
	if record.DurationMS != 400 {
		t.Fatalf("DurationMS = %d, want the span to the last checkpoint", record.DurationMS)
	}
	if record.DurationsMS["prepare"] != 150 {
		t.Fatalf("durations = %+v", record.DurationsMS)
	}
}

func TestLoggerHandlesNilReceivers(t *testing.T) {
	var logger *Logger
	if err := logger.Append(Record{Kind: "compaction"}); err != nil {
		t.Fatalf("Append on a nil logger: %v", err)
	}
	if err := NewSessionLogger(t.TempDir()).AppendSnapshot("k", "m", "s", 1, nil, nil); err != nil {
		t.Fatalf("AppendSnapshot with a nil recorder: %v", err)
	}
}

func TestSummarizeFileAggregatesCompactions(t *testing.T) {
	path := writeTimingLog(t,
		compactionRecord(1, "completed", "auto", "sliding-window", 2000, 100),
		compactionRecord(2, "completed", "manual", "summarize", 5000, 300),
		compactionRecord(3, "failed", "auto", "summarize", 0, 50),
		Record{Kind: "turn", Metric: "turn", DurationMS: 900},
	)

	summary, err := SummarizeFile(path)
	if err != nil {
		t.Fatalf("SummarizeFile: %v", err)
	}
	if summary.Records != 4 {
		t.Errorf("Records = %d, want 4", summary.Records)
	}
	if summary.Compactions != 3 {
		t.Errorf("Compactions = %d, want 3", summary.Compactions)
	}
	if summary.CompletedCompactions != 2 || summary.FailedCompactions != 1 {
		t.Errorf("completed/failed = %d/%d", summary.CompletedCompactions, summary.FailedCompactions)
	}
	if summary.AutoCompactions != 2 || summary.ManualCompactions != 1 {
		t.Errorf("auto/manual = %d/%d", summary.AutoCompactions, summary.ManualCompactions)
	}
	if summary.TotalTokensSaved != 7000 {
		t.Errorf("TotalTokensSaved = %d, want 7000", summary.TotalTokensSaved)
	}
	if summary.MaxTokensSaved != 5000 || summary.MaxTokensSavedTurn != 2 {
		t.Errorf("max tokens = %d at turn %d", summary.MaxTokensSaved, summary.MaxTokensSavedTurn)
	}
	if summary.TotalDurationMS != 450 {
		t.Errorf("TotalDurationMS = %d, want 450", summary.TotalDurationMS)
	}
	if summary.StrategyCounts["summarize"] != 2 {
		t.Errorf("StrategyCounts = %+v", summary.StrategyCounts)
	}
}

func TestSummarizeFileSkipsBlankAndMalformedLines(t *testing.T) {
	path := filepath.Join(t.TempDir(), "timings.ndjson")
	content := "\n{not json}\n" + `{"kind":"compaction","metadata":{"status":"completed","tokens_saved":10}}` + "\n\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	summary, err := SummarizeFile(path)
	if err != nil {
		t.Fatalf("SummarizeFile: %v", err)
	}
	if summary.Records != 1 || summary.CompletedCompactions != 1 {
		t.Fatalf("summary = %+v, want only the valid record counted", summary)
	}
}

// A record with large metadata used to exceed bufio.Scanner's default limit and
// abort the whole summary.
func TestSummarizeFileReadsLongLines(t *testing.T) {
	record := compactionRecord(1, "completed", "auto", "summarize", 100, 10)
	record.Metadata["notes"] = strings.Repeat("x", 200_000)
	path := writeTimingLog(t, record)

	summary, err := SummarizeFile(path)
	if err != nil {
		t.Fatalf("SummarizeFile: %v", err)
	}
	if summary.Compactions != 1 {
		t.Fatalf("Compactions = %d, want the long record counted", summary.Compactions)
	}
}

func TestSummarizeFileMissingPath(t *testing.T) {
	if _, err := SummarizeFile(filepath.Join(t.TempDir(), "missing.ndjson")); err == nil {
		t.Fatal("SummarizeFile succeeded for a missing file")
	}
}

func TestSummarizeFileCountsBooleanMetadata(t *testing.T) {
	record := compactionRecord(1, "completed", "auto", "summarize", 100, 10)
	record.Metadata["has_fresh_session_memory"] = true
	record.Metadata["microcompact_applied"] = true
	path := writeTimingLog(t, record)

	summary, err := SummarizeFile(path)
	if err != nil {
		t.Fatalf("SummarizeFile: %v", err)
	}
	if summary.FreshSessionMemoryCompactions != 1 || summary.MicrocompactApplied != 1 {
		t.Fatalf("summary = %+v", summary)
	}
}

func TestRenderIncludesHeadlineNumbers(t *testing.T) {
	summary := Summary{
		Path:                          "/tmp/timings.ndjson",
		Records:                       5,
		Compactions:                   4,
		CompletedCompactions:          3,
		FailedCompactions:             1,
		AutoCompactions:               3,
		ManualCompactions:             1,
		FreshSessionMemoryCompactions: 3,
		MicrocompactApplied:           3,
		TotalDurationMS:               800,
		TotalTokensSaved:              9000,
		MaxTokensSaved:                5000,
		MaxTokensSavedTurn:            2,
		StrategyCounts:                map[string]int{"summarize": 3, "sliding-window": 1},
	}

	rendered := summary.Render()
	for _, want := range []string{
		"Timing summary for /tmp/timings.ndjson",
		"records: 5",
		"compactions: 3 completed, 1 failed",
		"reasons: auto=3 manual=1",
		"tokens saved: total=9000 avg=3000 max=5000 (turn 2)",
		"avg compaction duration: 200 ms",
		"- summarize: 3",
		"- sliding-window: 1",
	} {
		if !strings.Contains(rendered, want) {
			t.Errorf("render missing %q:\n%s", want, rendered)
		}
	}
}

func TestRenderRecommendsCollectingDataWhenEmpty(t *testing.T) {
	rendered := Summary{Path: "x", StrategyCounts: map[string]int{}}.Render()
	if !strings.Contains(rendered, "no compaction records found") {
		t.Fatalf("render = %s", rendered)
	}
}

func TestRecommendationsFlagWeakSignals(t *testing.T) {
	summary := Summary{
		Compactions:                   10,
		CompletedCompactions:          10,
		FreshSessionMemoryCompactions: 1,
		MicrocompactApplied:           1,
		TotalTokensSaved:              1000,
	}
	recommendations := strings.Join(summary.recommendations(), "\n")
	for _, want := range []string{"session memory is stale", "microcompaction rarely triggers", "average token savings are modest"} {
		if !strings.Contains(recommendations, want) {
			t.Errorf("recommendations missing %q:\n%s", want, recommendations)
		}
	}
}

func TestSortedStrategyCountsIsDeterministic(t *testing.T) {
	counts := map[string]int{"b": 2, "a": 2, "c": 5}
	for range 20 {
		items := sortedStrategyCounts(counts)
		if items[0].name != "c" || items[1].name != "a" || items[2].name != "b" {
			t.Fatalf("order = %+v, want c, a, b", items)
		}
	}
}

func TestMetadataAccessors(t *testing.T) {
	metadata := map[string]any{
		"text":    "  value  ",
		"int":     42,
		"int64":   int64(43),
		"float":   44.9,
		"boolean": true,
		"wrong":   []string{"x"},
	}
	if got := metadataString(metadata, "text"); got != "value" {
		t.Errorf("metadataString = %q", got)
	}
	if got := metadataString(metadata, "int"); got != "" {
		t.Errorf("metadataString on a non-string = %q", got)
	}
	if got := metadataString(nil, "text"); got != "" {
		t.Errorf("metadataString on nil = %q", got)
	}
	for key, want := range map[string]int{"int": 42, "int64": 43, "float": 44, "wrong": 0} {
		if got := metadataInt(metadata, key); got != want {
			t.Errorf("metadataInt(%q) = %d, want %d", key, got, want)
		}
	}
	if metadataInt(nil, "int") != 0 {
		t.Error("metadataInt on nil should be 0")
	}
	if !metadataBool(metadata, "boolean") || metadataBool(metadata, "text") || metadataBool(nil, "boolean") {
		t.Error("metadataBool mishandled its inputs")
	}
}

func TestSafeAverages(t *testing.T) {
	if got := safeAverageInt(10, 0); got != 0 {
		t.Errorf("safeAverageInt(10, 0) = %d, want 0", got)
	}
	if got := safeAverageInt(10, 4); got != 2 {
		t.Errorf("safeAverageInt(10, 4) = %d, want 2", got)
	}
	if got := safeAverageInt64(10, 0); got != 0 {
		t.Errorf("safeAverageInt64(10, 0) = %d, want 0", got)
	}
	if got := safeAverageInt64(9, 3); got != 3 {
		t.Errorf("safeAverageInt64(9, 3) = %d, want 3", got)
	}
}
