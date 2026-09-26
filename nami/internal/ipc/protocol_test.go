package ipc

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// The client treats these timestamps as optional. An unset time.Time must be
// left out rather than sent as year 1, which reads as a real date.
func TestUnsetTimestampsAreLeftOut(t *testing.T) {
	payloads := []any{
		BackgroundCommandUpdatedPayload{CommandID: "c1", Status: "running"},
		BackgroundCommandDetailPayload{CommandID: "c1", Status: "running"},
		SwarmHandoffPayload{ID: "h1", Status: "pending"},
	}
	for _, payload := range payloads {
		encoded, err := json.Marshal(payload)
		if err != nil {
			t.Fatalf("marshal %T: %v", payload, err)
		}
		if strings.Contains(string(encoded), "0001-01-01") {
			t.Errorf("%T encodes an unset time: %s", payload, encoded)
		}
	}

	set := time.Date(2026, 9, 26, 12, 0, 0, 0, time.UTC)
	encoded, err := json.Marshal(BackgroundCommandUpdatedPayload{StartedAt: set, UpdatedAt: set})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(encoded), `"started_at":"2026-09-26T12:00:00Z"`) {
		t.Errorf("a set time is missing: %s", encoded)
	}
}
