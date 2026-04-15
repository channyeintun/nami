package agent

import (
	"strings"
	"time"
)

const SessionMemoryFreshnessWindow = 20 * time.Minute

// SessionMemorySnapshot holds the current extracted session working state.
type SessionMemorySnapshot struct {
	ArtifactID               string
	Title                    string
	Content                  string
	Version                  int
	UpdatedAt                time.Time
	SourceConversationTokens int
	SourceToolCallCount      int
}

func (s SessionMemorySnapshot) HasContent() bool {
	return strings.TrimSpace(s.Content) != ""
}

func (s SessionMemorySnapshot) IsFresh(now time.Time) bool {
	if !s.HasContent() || s.UpdatedAt.IsZero() {
		return false
	}
	if now.IsZero() {
		now = time.Now()
	}
	return now.Sub(s.UpdatedAt) <= SessionMemoryFreshnessWindow
}

// FormatSessionMemorySection renders extracted session continuity into the prompt.
func FormatSessionMemorySection(snapshot SessionMemorySnapshot) string {
	content := strings.TrimSpace(snapshot.Content)
	if content == "" {
		return ""
	}

	var b strings.Builder
	b.WriteString("<session_memory>\n")
	b.WriteString("This is extracted session working state for continuity across long turns and compaction. Treat it as session-scoped working memory, not durable project or user memory. Prefer it when reconstructing the active objective, current state, and pending work.\n\n")
	b.WriteString(content)
	if !strings.HasSuffix(content, "\n") {
		b.WriteString("\n")
	}
	b.WriteString("</session_memory>")
	return b.String()
}
