package commands

import (
	"strings"
	"testing"

	configpkg "github.com/channyeintun/nami/internal/config"
	mcppkg "github.com/channyeintun/nami/internal/mcp"
)

func TestValidateMCPServerName(t *testing.T) {
	valid := []string{"github", "my-server", "my_server", "srv2", "A-b_9"}
	for _, name := range valid {
		if err := validateMCPServerName(name); err != nil {
			t.Errorf("validateMCPServerName(%q): %v", name, err)
		}
	}
	invalid := []string{"", "has space", "dots.are.out", "slash/name", "quote\"name", "emoji😀"}
	for _, name := range invalid {
		if err := validateMCPServerName(name); err == nil {
			t.Errorf("validateMCPServerName(%q) = nil, want an error", name)
		}
	}
}

func TestParseMCPTransport(t *testing.T) {
	cases := map[string]mcppkg.TransportKind{
		"":       mcppkg.TransportStdio,
		"  ":     mcppkg.TransportStdio,
		"stdio":  mcppkg.TransportStdio,
		"STDIO":  mcppkg.TransportStdio,
		" http ": mcppkg.TransportHTTP,
		"sse":    mcppkg.TransportSSE,
		"ws":     mcppkg.TransportWS,
	}
	for raw, want := range cases {
		got, err := parseMCPTransport(raw)
		if err != nil {
			t.Errorf("parseMCPTransport(%q): %v", raw, err)
			continue
		}
		if got != want {
			t.Errorf("parseMCPTransport(%q) = %q, want %q", raw, got, want)
		}
	}
	if _, err := parseMCPTransport("carrier-pigeon"); err == nil {
		t.Error("parseMCPTransport accepted an unknown transport")
	}
}

func TestBuildMCPServerConfigStdio(t *testing.T) {
	server, warning, summary, err := buildMCPServerConfig(
		mcppkg.TransportStdio,
		"my-server",
		[]string{"--stdio", "--verbose"},
		MCPAddOptions{Env: []string{"API_KEY=abc", "MODE=fast"}, Trust: true, Disabled: true, StartupMS: 1500},
	)
	if err != nil {
		t.Fatalf("buildMCPServerConfig: %v", err)
	}
	if warning != "" {
		t.Errorf("warning = %q, want none", warning)
	}
	if summary != "my-server --stdio --verbose" {
		t.Errorf("summary = %q", summary)
	}
	if server.Command == nil || *server.Command != "my-server" {
		t.Fatalf("command = %v", server.Command)
	}
	if len(server.Args) != 2 {
		t.Errorf("args = %#v", server.Args)
	}
	if server.Env["API_KEY"] != "abc" || server.Env["MODE"] != "fast" {
		t.Errorf("env = %#v", server.Env)
	}
	if server.Trust == nil || !*server.Trust {
		t.Error("trust flag was not applied")
	}
	if server.Enabled == nil || *server.Enabled {
		t.Error("disabled flag was not applied")
	}
	if server.StartupTimeoutMS == nil || *server.StartupTimeoutMS != 1500 {
		t.Errorf("startup timeout = %v", server.StartupTimeoutMS)
	}
}

// A URL passed as a stdio command is almost always a mistake, so the caller
// gets a warning instead of a server that can never start.
func TestBuildMCPServerConfigWarnsAboutURLAsCommand(t *testing.T) {
	_, warning, _, err := buildMCPServerConfig(mcppkg.TransportStdio, "https://example.com/mcp", nil, MCPAddOptions{})
	if err != nil {
		t.Fatalf("buildMCPServerConfig: %v", err)
	}
	if !strings.Contains(warning, "looks like a URL") {
		t.Fatalf("warning = %q", warning)
	}
}

func TestBuildMCPServerConfigHTTP(t *testing.T) {
	server, warning, summary, err := buildMCPServerConfig(
		mcppkg.TransportHTTP,
		"https://example.com/mcp",
		nil,
		MCPAddOptions{Headers: []string{"Authorization: Bearer token", "X-Trace:  abc  "}},
	)
	if err != nil {
		t.Fatalf("buildMCPServerConfig: %v", err)
	}
	if warning != "" {
		t.Errorf("warning = %q", warning)
	}
	if summary != "https://example.com/mcp" {
		t.Errorf("summary = %q", summary)
	}
	if server.URL == nil || *server.URL != "https://example.com/mcp" {
		t.Fatalf("url = %v", server.URL)
	}
	if server.Headers["Authorization"] != "Bearer token" {
		t.Errorf("headers = %#v", server.Headers)
	}
	if server.Headers["X-Trace"] != "abc" {
		t.Errorf("header value should be trimmed: %#v", server.Headers)
	}
}

func TestBuildMCPServerConfigRejectsMixedOptions(t *testing.T) {
	cases := []struct {
		name      string
		transport mcppkg.TransportKind
		target    string
		args      []string
		options   MCPAddOptions
	}{
		{"stdio with headers", mcppkg.TransportStdio, "cmd", nil, MCPAddOptions{Headers: []string{"A: b"}}},
		{"http with args", mcppkg.TransportHTTP, "https://example.com", []string{"extra"}, MCPAddOptions{}},
		{"http with env", mcppkg.TransportHTTP, "https://example.com", nil, MCPAddOptions{Env: []string{"A=b"}}},
		{"bad env assignment", mcppkg.TransportStdio, "cmd", nil, MCPAddOptions{Env: []string{"no-equals"}}},
		{"blank env key", mcppkg.TransportStdio, "cmd", nil, MCPAddOptions{Env: []string{"  =value"}}},
		{"bad header", mcppkg.TransportHTTP, "https://example.com", nil, MCPAddOptions{Headers: []string{"no-colon"}}},
		{"blank header key", mcppkg.TransportHTTP, "https://example.com", nil, MCPAddOptions{Headers: []string{"  : value"}}},
		{"unknown transport", mcppkg.TransportKind("carrier"), "x", nil, MCPAddOptions{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, _, _, err := buildMCPServerConfig(tc.transport, tc.target, tc.args, tc.options); err == nil {
				t.Fatal("buildMCPServerConfig accepted an invalid combination")
			}
		})
	}
}

func TestParseEnvAssignments(t *testing.T) {
	parsed, err := parseEnvAssignments([]string{" KEY =value with = signs", "EMPTY="})
	if err != nil {
		t.Fatalf("parseEnvAssignments: %v", err)
	}
	if parsed["KEY"] != "value with = signs" {
		t.Errorf("parsed = %#v, want the value after the first =", parsed)
	}
	if _, ok := parsed["EMPTY"]; !ok {
		t.Errorf("parsed = %#v, want an empty value to be kept", parsed)
	}
	if got, err := parseEnvAssignments(nil); got != nil || err != nil {
		t.Fatalf("parseEnvAssignments(nil) = %#v, %v", got, err)
	}
}

func TestParseHeaderAssignments(t *testing.T) {
	parsed, err := parseHeaderAssignments([]string{"Authorization: Bearer abc:def"})
	if err != nil {
		t.Fatalf("parseHeaderAssignments: %v", err)
	}
	if parsed["Authorization"] != "Bearer abc:def" {
		t.Errorf("parsed = %#v, want the value after the first colon", parsed)
	}
	if got, err := parseHeaderAssignments(nil); got != nil || err != nil {
		t.Fatalf("parseHeaderAssignments(nil) = %#v, %v", got, err)
	}
}

func TestLooksLikeURL(t *testing.T) {
	for _, value := range []string{"http://x", "HTTPS://x", " ws://x ", "wss://x"} {
		if !looksLikeURL(value) {
			t.Errorf("looksLikeURL(%q) = false, want true", value)
		}
	}
	for _, value := range []string{"", "npx", "/usr/bin/server", "file:///x"} {
		if looksLikeURL(value) {
			t.Errorf("looksLikeURL(%q) = true, want false", value)
		}
	}
}

func TestValidateMCPServerConfigRejectsIncompleteServers(t *testing.T) {
	cwd := t.TempDir()
	transport := "stdio"
	if err := validateMCPServerConfig(cwd, "srv", configpkg.MCPServerConfig{Transport: &transport}); err == nil {
		t.Fatal("validateMCPServerConfig accepted a stdio server without a command")
	}

	command := "my-server"
	if err := validateMCPServerConfig(cwd, "srv", configpkg.MCPServerConfig{Transport: &transport, Command: &command}); err != nil {
		t.Fatalf("validateMCPServerConfig: %v", err)
	}
}

func TestFormatSortedKeyValueIsDeterministic(t *testing.T) {
	values := map[string]string{"b": "2", "a": "1", "c": "3"}
	for range 20 {
		got := formatSortedKeyValue(values, "%s=%s")
		if len(got) != 3 || got[0] != "a=1" || got[1] != "b=2" || got[2] != "c=3" {
			t.Fatalf("formatSortedKeyValue = %#v, want key order", got)
		}
	}
	if got := formatSortedKeyValue(nil, "%s=%s"); len(got) != 0 {
		t.Fatalf("formatSortedKeyValue(nil) = %#v", got)
	}
}

func TestRenderServerSummary(t *testing.T) {
	transport := "stdio"
	command := "my-server"
	server := configpkg.MCPServerConfig{
		Transport: &transport,
		Command:   &command,
		Args:      []string{"--stdio"},
		Env:       map[string]string{"KEY": "value"},
	}
	summary := renderServerSummary(server)
	if !strings.Contains(summary, "my-server") {
		t.Fatalf("summary = %q", summary)
	}
}

func TestEffectiveTransportLabel(t *testing.T) {
	explicit := "http"
	url := "https://example.com"
	command := "cmd"
	if got := effectiveTransportLabel(configpkg.MCPServerConfig{Transport: &explicit}); got != "http" {
		t.Errorf("label = %q, want the explicit transport", got)
	}
	if got := effectiveTransportLabel(configpkg.MCPServerConfig{URL: &url}); got == "" {
		t.Error("a server with only a url should still get a transport label")
	}
	if got := effectiveTransportLabel(configpkg.MCPServerConfig{Command: &command}); got == "" {
		t.Error("a server with only a command should still get a transport label")
	}
}
