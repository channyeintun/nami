package commands

import (
	"strings"
	"testing"
	"time"

	"github.com/channyeintun/nami/internal/session"
)

func TestFormatSessionListShortensIDs(t *testing.T) {
	sessions := []session.Metadata{{
		SessionID: "0f8c2d1e-aaaa-bbbb-cccc-000000000000",
		UpdatedAt: time.Date(2026, 1, 2, 3, 4, 0, 0, time.UTC),
		Model:     "anthropic/claude-sonnet-5",
		Title:     "Fix the build",
	}}
	listing := FormatSessionList(sessions, "0f8c2d1e-aaaa-bbbb-cccc-000000000000")
	if !strings.Contains(listing, "* 0f8c2d1e  ") || strings.Contains(listing, "aaaa") {
		t.Fatalf("listing = %q, want the current session marked with its 8-character id", listing)
	}
}

// The list is built from whatever metadata.json files are on disk. One with a
// short or missing session id must not panic the /sessions command.
func TestFormatSessionListToleratesShortIDs(t *testing.T) {
	sessions := []session.Metadata{
		{SessionID: "abc", Title: "short"},
		{SessionID: "", Title: "missing"},
	}
	listing := FormatSessionList(sessions, "")
	for _, want := range []string{"abc", "short", "missing"} {
		if !strings.Contains(listing, want) {
			t.Fatalf("listing %q is missing %q", listing, want)
		}
	}
}
