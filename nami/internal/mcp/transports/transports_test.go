package transports

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestBuildDispatchesByKind(t *testing.T) {
	for _, kind := range []string{"stdio", "sse", "http", "ws"} {
		transport, err := Build(Config{
			Kind:          kind,
			Command:       "echo",
			URL:           "https://example.com/mcp",
			ShutdownGrace: time.Second,
		})
		if err != nil {
			t.Errorf("Build(%q): %v", kind, err)
			continue
		}
		if transport == nil {
			t.Errorf("Build(%q) returned a nil transport", kind)
		}
	}

	if _, err := Build(Config{Kind: "carrier-pigeon"}); err == nil {
		t.Error("Build accepted an unsupported transport kind")
	}
}

func TestCloneHeaders(t *testing.T) {
	if got := cloneHeaders(nil); got != nil {
		t.Fatalf("cloneHeaders(nil) = %#v, want nil", got)
	}
	headers := cloneHeaders(map[string]string{"Authorization": "Bearer x", "x-trace": "1"})
	if got := headers.Get("Authorization"); got != "Bearer x" {
		t.Errorf("Authorization = %q", got)
	}
	// http.Header canonicalizes keys, so lookups are case-insensitive.
	if got := headers.Get("X-Trace"); got != "1" {
		t.Errorf("X-Trace = %q", got)
	}
}

func TestHeaderRoundTripperAppliesConfiguredHeaders(t *testing.T) {
	var seen http.Header
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = r.Header.Clone()
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	client := newHeaderHTTPClient(map[string]string{"Authorization": "Bearer token", "X-Trace": "abc"})
	req, err := http.NewRequest(http.MethodGet, server.URL, nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	// A pre-existing value must be replaced, not duplicated.
	req.Header.Set("Authorization", "Bearer stale")

	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer resp.Body.Close()

	if got := seen.Values("Authorization"); len(got) != 1 || got[0] != "Bearer token" {
		t.Fatalf("Authorization = %#v, want only the configured value", got)
	}
	if got := seen.Get("X-Trace"); got != "abc" {
		t.Fatalf("X-Trace = %q", got)
	}
}

func TestHeaderRoundTripperLeavesRequestUnmodified(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	client := newHeaderHTTPClient(map[string]string{"X-Added": "1"})
	req, err := http.NewRequest(http.MethodGet, server.URL, nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}

	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer resp.Body.Close()

	if req.Header.Get("X-Added") != "" {
		t.Fatal("the round tripper mutated the caller's request")
	}
}

func TestNewHeaderHTTPClientWithoutHeaders(t *testing.T) {
	client := newHeaderHTTPClient(nil)
	if client == nil {
		t.Fatal("newHeaderHTTPClient returned nil")
	}
	if client.Transport != nil {
		t.Fatal("a client without headers should keep the default transport")
	}
}

func TestMergeEnvOverlaysProcessEnvironment(t *testing.T) {
	t.Setenv("NAMI_TRANSPORT_TEST_BASE", "base-value")
	t.Setenv("NAMI_TRANSPORT_TEST_OVERRIDE", "original")

	if got := mergeEnv(nil); got != nil {
		t.Fatalf("mergeEnv(nil) = %#v, want nil so the child inherits the environment", got)
	}

	merged := mergeEnv(map[string]string{
		"NAMI_TRANSPORT_TEST_OVERRIDE": "replaced",
		"NAMI_TRANSPORT_TEST_NEW":      "added",
	})

	values := make(map[string]string, len(merged))
	for _, entry := range merged {
		key, value, ok := splitEnv(entry)
		if !ok {
			t.Fatalf("merged entry %q has no separator", entry)
		}
		values[key] = value
	}

	if values["NAMI_TRANSPORT_TEST_BASE"] != "base-value" {
		t.Errorf("inherited variable = %q", values["NAMI_TRANSPORT_TEST_BASE"])
	}
	if values["NAMI_TRANSPORT_TEST_OVERRIDE"] != "replaced" {
		t.Errorf("override = %q, want the configured value to win", values["NAMI_TRANSPORT_TEST_OVERRIDE"])
	}
	if values["NAMI_TRANSPORT_TEST_NEW"] != "added" {
		t.Errorf("new variable = %q", values["NAMI_TRANSPORT_TEST_NEW"])
	}
}

func TestSplitEnv(t *testing.T) {
	key, value, ok := splitEnv("KEY=value=with=equals")
	if !ok || key != "KEY" || value != "value=with=equals" {
		t.Fatalf("splitEnv = %q, %q, %v", key, value, ok)
	}
	if _, _, ok := splitEnv("no-separator"); ok {
		t.Fatal("splitEnv accepted an entry without a separator")
	}
	if key, value, ok := splitEnv("EMPTY="); !ok || key != "EMPTY" || value != "" {
		t.Fatalf("splitEnv = %q, %q, %v", key, value, ok)
	}
}

func TestBuildStdioCarriesCommandConfiguration(t *testing.T) {
	workingDir := t.TempDir()
	transport, err := Build(Config{
		Kind:          "stdio",
		Command:       "my-server",
		Args:          []string{"--stdio"},
		WorkingDir:    workingDir,
		Env:           map[string]string{"NAMI_TEST": "1"},
		ShutdownGrace: 2 * time.Second,
	})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	command, ok := transport.(*sdkmcp.CommandTransport)
	if !ok {
		t.Fatalf("transport = %T, want *sdkmcp.CommandTransport", transport)
	}
	if command.TerminateDuration != 2*time.Second {
		t.Errorf("TerminateDuration = %v", command.TerminateDuration)
	}
	if command.Command.Dir != workingDir {
		t.Errorf("Dir = %q, want %q", command.Command.Dir, workingDir)
	}
	if len(command.Command.Args) < 2 || command.Command.Args[1] != "--stdio" {
		t.Errorf("Args = %#v", command.Command.Args)
	}
	if len(command.Command.Env) == 0 {
		t.Error("configured env was not applied to the command")
	}
}
