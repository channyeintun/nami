package debuglog

import (
	"bytes"
	"encoding/json"
	"io"
	"os/exec"
	"runtime"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestRedactSecretsKeepsShapeButHidesValue(t *testing.T) {
	input := `{"access_token":"sk-abcdefghijklmnopqrstuvwxyz","user":"nami"}`
	got := RedactSecrets(input)

	if strings.Contains(got, "sk-abcdefghijklmnopqrstuvwxyz") {
		t.Fatalf("secret survived redaction: %s", got)
	}
	if !strings.Contains(got, "sk-abc") || !strings.Contains(got, "wxyz") {
		t.Fatalf("redacted value lost its head and tail: %s", got)
	}
	if !strings.Contains(got, `"user":"nami"`) {
		t.Fatalf("non-secret fields were altered: %s", got)
	}
}

func TestRedactSecretsFullyHidesShortValues(t *testing.T) {
	got := RedactSecrets(`{"secret":"short"}`)
	if !strings.Contains(got, "[REDACTED]") || strings.Contains(got, "short") {
		t.Fatalf("short secret was not fully redacted: %s", got)
	}
}

func TestRedactSecretsCoversKnownFieldNames(t *testing.T) {
	for _, field := range []string{"session_ingress_token", "environment_secret", "access_token", "authorization", "secret", "token"} {
		input := `{"` + field + `":"abcdefghijklmnopqrstuvwxyz"}`
		if got := RedactSecrets(input); strings.Contains(got, "abcdefghijklmnopqrstuvwxyz") {
			t.Errorf("field %q was not redacted: %s", field, got)
		}
	}
}

func TestRedactSecretsLeavesOtherContentAlone(t *testing.T) {
	for _, input := range []string{"", "   ", `{"model":"claude-opus-5"}`, "plain text"} {
		if got := RedactSecrets(input); got != input {
			t.Errorf("RedactSecrets(%q) = %q, want it unchanged", input, got)
		}
	}
}

func TestRedactSecretsProducesValidUTF8(t *testing.T) {
	input := `{"token":"日本語のトークンですこれは長い値"}`
	got := RedactSecrets(input)
	if !utf8.ValidString(got) {
		t.Fatalf("redaction produced invalid UTF-8: %q", got)
	}
}

func TestTruncate(t *testing.T) {
	if got := Truncate("short", 100); got != "short" {
		t.Errorf("Truncate = %q, want it unchanged", got)
	}
	if got := Truncate("abcdefgh", 0); got != "abcdefgh" {
		t.Errorf("Truncate with a zero limit = %q, want it unchanged", got)
	}
	got := Truncate("abcdefgh", 4)
	if got != "abcd...(truncated)" {
		t.Errorf("Truncate = %q", got)
	}
	if !utf8.ValidString(Truncate("héllo wörld", 2)) {
		t.Error("Truncate produced invalid UTF-8")
	}
}

func TestNormalizeFieldsSplitsDataMetricsAndError(t *testing.T) {
	data, metrics, eventErr := normalizeFields(map[string]any{
		"tool_name":   "bash",
		"bytes":       128,
		"duration_ms": 42,
		"error":       "boom",
		"error_kind":  "timeout",
	})

	if data["tool_name"] != "bash" {
		t.Errorf("data = %+v", data)
	}
	if _, ok := data["bytes"]; ok {
		t.Error("metric keys must not stay in data")
	}
	if metrics["bytes"] != 128 || metrics["duration_ms"] != 42 {
		t.Errorf("metrics = %+v", metrics)
	}
	if eventErr == nil {
		t.Fatal("error fields produced no EventError")
	}
	if eventErr.Message != "boom" || eventErr.Kind != "timeout" {
		t.Fatalf("event error = %+v, want both message and kind", eventErr)
	}
}

// error and error_kind arrive in a map, so whichever the runtime yields first
// must not discard the other.
func TestNormalizeFieldsKeepsBothErrorFieldsRegardlessOfMapOrder(t *testing.T) {
	for range 200 {
		_, _, eventErr := normalizeFields(map[string]any{
			"error":      "boom",
			"error_kind": "timeout",
		})
		if eventErr == nil || eventErr.Message != "boom" || eventErr.Kind != "timeout" {
			t.Fatalf("event error = %+v, want message and kind preserved", eventErr)
		}
	}
}

func TestNormalizeFieldsIgnoresBlankErrors(t *testing.T) {
	_, _, eventErr := normalizeFields(map[string]any{"error": "   ", "error_kind": "  "})
	if eventErr != nil {
		t.Fatalf("event error = %+v, want nil for blank values", eventErr)
	}
}

func TestNormalizeFieldsOnEmptyInput(t *testing.T) {
	data, metrics, eventErr := normalizeFields(nil)
	if data != nil || metrics != nil || eventErr != nil {
		t.Fatalf("normalizeFields(nil) = %+v, %+v, %+v", data, metrics, eventErr)
	}
}

func TestLogLevelForError(t *testing.T) {
	if got := logLevelForError(nil); got != "debug" {
		t.Errorf("level = %q, want debug", got)
	}
	if got := logLevelForError(&EventError{Kind: "timeout"}); got != "debug" {
		t.Errorf("level = %q, want debug when only a kind is set", got)
	}
	if got := logLevelForError(&EventError{Message: "boom"}); got != "error" {
		t.Errorf("level = %q, want error", got)
	}
}

func TestExtractIPCType(t *testing.T) {
	cases := map[string]string{
		`{"type":"token_delta","payload":{}}`: "token_delta",
		`{"payload":{}}`:                      "unknown",
		`not json`:                            "unknown",
		``:                                    "unknown",
		`{"type":""}`:                         "unknown",
	}
	for input, want := range cases {
		if got := extractIPCType(input); got != want {
			t.Errorf("extractIPCType(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestIPCWriterPassesBytesThrough(t *testing.T) {
	var sink bytes.Buffer
	writer := NewIPCWriter(&sink)

	payload := []byte(`{"type":"ready"}` + "\n")
	n, err := writer.Write(payload)
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if n != len(payload) {
		t.Fatalf("n = %d, want %d", n, len(payload))
	}
	if sink.String() != string(payload) {
		t.Fatalf("sink = %q, want the payload unchanged", sink.String())
	}
}

func TestIPCReaderPassesBytesThrough(t *testing.T) {
	source := strings.NewReader(`{"type":"user_input"}` + "\n")
	reader := NewIPCReader(source)

	got, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if string(got) != `{"type":"user_input"}`+"\n" {
		t.Fatalf("read %q", got)
	}
}

func TestMatchesFilters(t *testing.T) {
	envelope := Envelope{Level: "error", Component: "ipc", Event: "emit"}
	cases := []struct {
		name    string
		options MonitorOptions
		want    bool
	}{
		{"no filters", MonitorOptions{}, true},
		{"matching level", MonitorOptions{Level: "ERROR"}, true},
		{"other level", MonitorOptions{Level: "debug"}, false},
		{"matching component", MonitorOptions{Component: "IPC"}, true},
		{"other component", MonitorOptions{Component: "engine"}, false},
		{"matching event", MonitorOptions{Event: "emit"}, true},
		{"other event", MonitorOptions{Event: "recv"}, false},
		{"all matching", MonitorOptions{Level: "error", Component: "ipc", Event: "emit"}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := matchesFilters(envelope, tc.options); got != tc.want {
				t.Fatalf("matchesFilters = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestSummarizeEnvelopePrefersKnownFields(t *testing.T) {
	summary := summarizeEnvelope(Envelope{
		Data:    map[string]any{"type": "tool_use", "tool_name": "bash", "ignored": "x"},
		Metrics: map[string]any{"bytes": 12, "duration_ms": 40},
	})
	for _, want := range []string{"type=tool_use", "tool_name=bash", "bytes=12", "duration_ms=40"} {
		if !strings.Contains(summary, want) {
			t.Errorf("summary %q missing %q", summary, want)
		}
	}
	if strings.Contains(summary, "ignored") {
		t.Errorf("summary included an unlisted field: %q", summary)
	}
}

func TestSummarizeEnvelopeFallsBackToPayload(t *testing.T) {
	summary := summarizeEnvelope(Envelope{Data: map[string]any{"unexpected": "value"}})
	if !strings.Contains(summary, "unexpected") {
		t.Fatalf("summary = %q, want the raw payload", summary)
	}

	long := strings.Repeat("é", 400)
	summary = summarizeEnvelope(Envelope{Data: map[string]any{"long": long}})
	if !utf8.ValidString(summary) {
		t.Fatal("summary truncation produced invalid UTF-8")
	}
	if len(summary) > maxSummaryChars+len("...(truncated)") {
		t.Fatalf("summary is %d bytes, want it capped", len(summary))
	}

	if got := summarizeEnvelope(Envelope{}); got != "-" {
		t.Fatalf("summary = %q, want the placeholder", got)
	}
}

func TestShellQuote(t *testing.T) {
	cases := map[string]string{
		"":           "''",
		"/usr/bin/x": "'/usr/bin/x'",
		"a b":        "'a b'",
		"it's":       "'it'\"'\"'s'",
		"/tmp/a'b/c": "'/tmp/a'\"'\"'b/c'",
	}
	for input, want := range cases {
		if got := shellQuote(input); got != want {
			t.Errorf("shellQuote(%q) = %q, want %q", input, got, want)
		}
	}
}

// The quoted value has to survive an actual shell, which is the property that
// matters: a path with an apostrophe previously produced an unterminated
// string and broke the command.
func TestShellQuoteRoundTripsThroughSh(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no POSIX shell available")
	}
	for _, value := range []string{
		"plain",
		"with space",
		"it's",
		`/Users/me/My 'Docs'/debug.log`,
		`quotes "and" 'both'`,
		"semi;colon && pipe|",
		"$HOME `id`",
		"tab\tand\nnewline",
	} {
		out, err := exec.Command("sh", "-c", "printf %s "+shellQuote(value)).Output()
		if err != nil {
			t.Fatalf("sh rejected the quoted value %q: %v", value, err)
		}
		if string(out) != value {
			t.Fatalf("shell produced %q, want %q", out, value)
		}
	}
}

func TestDebugViewCommandLineQuotesBothPaths(t *testing.T) {
	got := debugViewCommandLine("/opt/my apps/nami", "/tmp/logs/debug.log")
	if !strings.Contains(got, `'/opt/my apps/nami'`) || !strings.Contains(got, `'/tmp/logs/debug.log'`) {
		t.Fatalf("command = %q, want both paths quoted", got)
	}
	if !strings.Contains(got, "debug-view --file") {
		t.Fatalf("command = %q", got)
	}
}

func TestFlattenAppleScript(t *testing.T) {
	got := flattenAppleScript([]string{"tell app", "end tell"})
	want := []string{"-e", "tell app", "-e", "end tell"}
	if len(got) != len(want) {
		t.Fatalf("flattenAppleScript = %#v, want %#v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("flattenAppleScript = %#v, want %#v", got, want)
		}
	}
}

func TestFilterValue(t *testing.T) {
	if got := filterValue("  "); got != "*" {
		t.Errorf("filterValue = %q, want *", got)
	}
	if got := filterValue(" error "); got != "error" {
		t.Errorf("filterValue = %q, want the trimmed value", got)
	}
}

func TestEmitIsInertWhenDisabled(t *testing.T) {
	// Emit must be safe to call before any session is configured.
	Emit(Envelope{Component: "test", Event: "noop"})
	Log("test", "noop", map[string]any{"error": "ignored"})
	if IsEnabled() {
		t.Fatal("debug logging should be disabled by default in tests")
	}
	if CurrentPath() != "" {
		t.Fatalf("CurrentPath = %q, want empty", CurrentPath())
	}
}

func TestEnvelopeMarshalsToJSON(t *testing.T) {
	data, err := json.Marshal(Envelope{
		SchemaVersion: SchemaVersion,
		Component:     "ipc",
		Event:         "emit",
		Level:         "debug",
		Error:         &EventError{Message: "boom", Kind: "timeout"},
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(data), `"error":{"message":"boom","kind":"timeout"}`) {
		t.Fatalf("json = %s", data)
	}
}
