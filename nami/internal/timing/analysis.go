package timing

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
)

const maxTimingLineBytes = 4 * 1024 * 1024

type Summary struct {
	Path                          string
	Records                       int
	Compactions                   int
	CompletedCompactions          int
	FailedCompactions             int
	AutoCompactions               int
	ManualCompactions             int
	FreshSessionMemoryCompactions int
	MicrocompactApplied           int
	TotalDurationMS               int64
	TotalTokensSaved              int
	MaxTokensSaved                int
	MaxTokensSavedTurn            int
	StrategyCounts                map[string]int
}

func SummarizeFile(path string) (Summary, error) {
	file, err := os.Open(path)
	if err != nil {
		return Summary{}, fmt.Errorf("open timing log: %w", err)
	}
	defer file.Close()

	summary := Summary{
		Path:           path,
		StrategyCounts: map[string]int{},
	}

	scanner := bufio.NewScanner(file)
	// Records carry arbitrary metadata, so a single line can exceed the
	// scanner's 64 KiB default and would otherwise abort the whole summary.
	scanner.Buffer(make([]byte, 0, 64*1024), maxTimingLineBytes)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var record Record
		if err := unmarshalRecord(line, &record); err != nil {
			continue
		}
		summary.Records++
		if record.Kind != "compaction" {
			continue
		}
		summary.Compactions++
		summary.TotalDurationMS += record.DurationMS

		status := metadataString(record.Metadata, "status")
		reason := metadataString(record.Metadata, "reason")
		strategy := metadataString(record.Metadata, "strategy")
		tokensSaved := metadataInt(record.Metadata, "tokens_saved")
		if status == "completed" {
			summary.CompletedCompactions++
		} else if status == "failed" {
			summary.FailedCompactions++
		}
		if reason == "auto" {
			summary.AutoCompactions++
		} else if reason == "manual" {
			summary.ManualCompactions++
		}
		if strategy != "" {
			summary.StrategyCounts[strategy]++
		}
		if metadataBool(record.Metadata, "has_fresh_session_memory") {
			summary.FreshSessionMemoryCompactions++
		}
		if metadataBool(record.Metadata, "microcompact_applied") {
			summary.MicrocompactApplied++
		}
		summary.TotalTokensSaved += tokensSaved
		if tokensSaved > summary.MaxTokensSaved {
			summary.MaxTokensSaved = tokensSaved
			summary.MaxTokensSavedTurn = record.TurnID
		}
	}
	if err := scanner.Err(); err != nil {
		return Summary{}, fmt.Errorf("scan timing log: %w", err)
	}
	return summary, nil
}

func (s Summary) Render() string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("Timing summary for %s\n", s.Path))
	b.WriteString(fmt.Sprintf("records: %d\n", s.Records))
	b.WriteString(fmt.Sprintf("compactions: %d completed, %d failed\n", s.CompletedCompactions, s.FailedCompactions))
	b.WriteString(fmt.Sprintf("reasons: auto=%d manual=%d\n", s.AutoCompactions, s.ManualCompactions))
	b.WriteString(fmt.Sprintf("fresh session memory at compaction: %d/%d\n", s.FreshSessionMemoryCompactions, max(s.Compactions, 1)))
	b.WriteString(fmt.Sprintf("microcompact applied: %d/%d\n", s.MicrocompactApplied, max(s.CompletedCompactions, 1)))
	b.WriteString(fmt.Sprintf("tokens saved: total=%d avg=%d max=%d", s.TotalTokensSaved, safeAverageInt(s.TotalTokensSaved, s.CompletedCompactions), s.MaxTokensSaved))
	if s.MaxTokensSavedTurn > 0 {
		b.WriteString(fmt.Sprintf(" (turn %d)", s.MaxTokensSavedTurn))
	}
	b.WriteString("\n")
	b.WriteString(fmt.Sprintf("avg compaction duration: %d ms\n", safeAverageInt64(s.TotalDurationMS, s.Compactions)))
	b.WriteString("strategies:\n")
	for _, item := range sortedStrategyCounts(s.StrategyCounts) {
		b.WriteString(fmt.Sprintf("- %s: %d\n", item.name, item.count))
	}
	for _, recommendation := range s.recommendations() {
		b.WriteString(fmt.Sprintf("- recommendation: %s\n", recommendation))
	}
	return b.String()
}

func (s Summary) recommendations() []string {
	recommendations := make([]string, 0, 3)
	if s.Compactions == 0 {
		return []string{"no compaction records found; capture a longer session before tuning thresholds"}
	}
	if s.FreshSessionMemoryCompactions*2 < s.Compactions {
		recommendations = append(recommendations, "session memory is stale for many compactions; inspect extraction cadence before lowering compaction thresholds further")
	}
	if s.CompletedCompactions > 0 && s.MicrocompactApplied*2 < s.CompletedCompactions {
		recommendations = append(recommendations, "microcompaction rarely triggers; inspect whether preserved file or command markers still leave too much low-signal output in the transcript")
	}
	if s.CompletedCompactions > 0 && safeAverageInt(s.TotalTokensSaved, s.CompletedCompactions) < 1500 {
		recommendations = append(recommendations, "average token savings are modest; review the largest compaction turns and consider tighter pre-summary reduction")
	}
	return recommendations
}

type strategyCount struct {
	name  string
	count int
}

func sortedStrategyCounts(counts map[string]int) []strategyCount {
	items := make([]strategyCount, 0, len(counts))
	for name, count := range counts {
		items = append(items, strategyCount{name: name, count: count})
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].count == items[j].count {
			return items[i].name < items[j].name
		}
		return items[i].count > items[j].count
	})
	return items
}

func safeAverageInt(total, count int) int {
	if count <= 0 {
		return 0
	}
	return total / count
}

func safeAverageInt64(total int64, count int) int64 {
	if count <= 0 {
		return 0
	}
	return total / int64(count)
}

func unmarshalRecord(line string, record *Record) error {
	return json.Unmarshal([]byte(line), record)
}

func metadataString(metadata map[string]any, key string) string {
	if metadata == nil {
		return ""
	}
	if value, ok := metadata[key].(string); ok {
		return strings.TrimSpace(value)
	}
	return ""
}

func metadataInt(metadata map[string]any, key string) int {
	if metadata == nil {
		return 0
	}
	switch value := metadata[key].(type) {
	case int:
		return value
	case int64:
		return int(value)
	case float64:
		return int(value)
	default:
		return 0
	}
}

func metadataBool(metadata map[string]any, key string) bool {
	if metadata == nil {
		return false
	}
	if value, ok := metadata[key].(bool); ok {
		return value
	}
	return false
}
