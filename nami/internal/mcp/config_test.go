package mcp

import (
	"strings"
	"testing"
	"time"

	configpkg "github.com/channyeintun/nami/internal/config"
)

func ptr[T any](value T) *T {
	return &value
}

func stdioServer() configpkg.MCPServerConfig {
	return configpkg.MCPServerConfig{
		Transport: ptr("stdio"),
		Command:   ptr("my-server"),
	}
}

func httpServer() configpkg.MCPServerConfig {
	return configpkg.MCPServerConfig{
		Transport: ptr("http"),
		URL:       ptr("https://example.com/mcp"),
	}
}

func resolveOne(t *testing.T, name string, cfg configpkg.MCPServerConfig) (ServerDefinition, []ConfigProblem) {
	t.Helper()
	resolved := ResolveConfig(t.TempDir(), configpkg.MCPConfig{
		Servers: map[string]configpkg.MCPServerConfig{name: cfg},
	})
	if len(resolved.Servers) == 1 {
		return resolved.Servers[0], resolved.Problems
	}
	return ServerDefinition{}, resolved.Problems
}

func TestResolveConfigEmpty(t *testing.T) {
	resolved := ResolveConfig(t.TempDir(), configpkg.MCPConfig{})
	if len(resolved.Servers) != 0 || len(resolved.Problems) != 0 {
		t.Fatalf("resolved = %+v, want empty", resolved)
	}
}

func TestResolveConfigSortsServersByName(t *testing.T) {
	resolved := ResolveConfig(t.TempDir(), configpkg.MCPConfig{
		Servers: map[string]configpkg.MCPServerConfig{
			"zeta":  stdioServer(),
			"alpha": stdioServer(),
			"mid":   stdioServer(),
		},
	})
	if len(resolved.Servers) != 3 {
		t.Fatalf("servers = %d, want 3", len(resolved.Servers))
	}
	for i, want := range []string{"alpha", "mid", "zeta"} {
		if resolved.Servers[i].Name != want {
			t.Fatalf("servers = %v, want alphabetical order", []string{
				resolved.Servers[0].Name, resolved.Servers[1].Name, resolved.Servers[2].Name,
			})
		}
	}
}

func TestResolveStdioDefaults(t *testing.T) {
	definition, problems := resolveOne(t, "local", stdioServer())
	if len(problems) != 0 {
		t.Fatalf("problems = %+v", problems)
	}
	if definition.Transport != TransportStdio {
		t.Errorf("transport = %q", definition.Transport)
	}
	if !definition.Enabled {
		t.Error("servers should default to enabled")
	}
	if definition.Trusted {
		t.Error("servers should default to untrusted")
	}
	if definition.ConnectTimeout != defaultStdioStartupTimeout {
		t.Errorf("connect timeout = %v, want the stdio default", definition.ConnectTimeout)
	}
	if definition.ShutdownGrace != defaultShutdownGrace {
		t.Errorf("shutdown grace = %v", definition.ShutdownGrace)
	}
	if definition.WorkingDir == "" {
		t.Error("working dir should be resolved")
	}
}

func TestResolveHTTPDefaults(t *testing.T) {
	definition, problems := resolveOne(t, "remote", httpServer())
	if len(problems) != 0 {
		t.Fatalf("problems = %+v", problems)
	}
	if definition.Transport != TransportHTTP {
		t.Errorf("transport = %q", definition.Transport)
	}
	if definition.URL != "https://example.com/mcp" {
		t.Errorf("url = %q", definition.URL)
	}
	if definition.ConnectTimeout != defaultDiscoveryTimeout {
		t.Errorf("connect timeout = %v, want the discovery default", definition.ConnectTimeout)
	}
}

func TestResolveHonoursExplicitFlags(t *testing.T) {
	cfg := stdioServer()
	cfg.Enabled = ptr(false)
	cfg.Trust = ptr(true)
	cfg.StartupTimeoutMS = ptr(2500)

	definition, problems := resolveOne(t, "local", cfg)
	if len(problems) != 0 {
		t.Fatalf("problems = %+v", problems)
	}
	if definition.Enabled {
		t.Error("Enabled = true, want the explicit false")
	}
	if !definition.Trusted {
		t.Error("Trusted = false, want the explicit true")
	}
	if definition.ConnectTimeout != 2500*time.Millisecond {
		t.Errorf("connect timeout = %v", definition.ConnectTimeout)
	}
}

func TestResolveRejectsInvalidServers(t *testing.T) {
	cases := map[string]configpkg.MCPServerConfig{
		"missing transport": {Command: ptr("x")},
		"unknown transport": {Transport: ptr("carrier-pigeon"), Command: ptr("x")},
		"stdio without command": {
			Transport: ptr("stdio"),
		},
		"stdio with url": {
			Transport: ptr("stdio"), Command: ptr("x"), URL: ptr("https://example.com"),
		},
		"stdio with headers": {
			Transport: ptr("stdio"), Command: ptr("x"), Headers: map[string]string{"a": "b"},
		},
		"http without url": {
			Transport: ptr("http"),
		},
		"http with command": {
			Transport: ptr("http"), URL: ptr("https://example.com"), Command: ptr("x"),
		},
		"http with args": {
			Transport: ptr("http"), URL: ptr("https://example.com"), Args: []string{"a"},
		},
		"http with env": {
			Transport: ptr("http"), URL: ptr("https://example.com"), Env: map[string]string{"A": "b"},
		},
		"url without scheme": {
			Transport: ptr("http"), URL: ptr("example.com/mcp"),
		},
		"startup timeout on http": {
			Transport: ptr("http"), URL: ptr("https://example.com"), StartupTimeoutMS: ptr(100),
		},
		"non-positive startup timeout": {
			Transport: ptr("stdio"), Command: ptr("x"), StartupTimeoutMS: ptr(0),
		},
		"unsupported tool permission": {
			Transport:       ptr("stdio"),
			Command:         ptr("x"),
			ToolPermissions: map[string]configpkg.MCPPermission{"tool": configpkg.MCPPermission("root")},
		},
		"blank tool permission name": {
			Transport:       ptr("stdio"),
			Command:         ptr("x"),
			ToolPermissions: map[string]configpkg.MCPPermission{"  ": configpkg.MCPPermission("read")},
		},
	}

	for name, cfg := range cases {
		t.Run(name, func(t *testing.T) {
			definition, problems := resolveOne(t, "server", cfg)
			if len(problems) != 1 {
				t.Fatalf("problems = %+v, want exactly one", problems)
			}
			if definition.Name != "" {
				t.Fatalf("invalid config produced a definition: %+v", definition)
			}
			if problems[0].ServerName != "server" || problems[0].Err == nil {
				t.Fatalf("problem = %+v", problems[0])
			}
			if !strings.Contains(problems[0].Error(), "server") {
				t.Fatalf("problem message = %q", problems[0].Error())
			}
		})
	}
}

func TestConfigProblemMessageIncludesTransport(t *testing.T) {
	withTransport := ConfigProblem{ServerName: "x", Transport: "stdio", Err: errString("boom")}
	if got := withTransport.Error(); !strings.Contains(got, "(stdio)") {
		t.Errorf("Error() = %q", got)
	}
	withoutTransport := ConfigProblem{ServerName: "x", Err: errString("boom")}
	if got := withoutTransport.Error(); strings.Contains(got, "(") {
		t.Errorf("Error() = %q, want no transport section", got)
	}
}

type errString string

func (e errString) Error() string { return string(e) }

func TestTransportAliasesAreCaseInsensitive(t *testing.T) {
	for raw, want := range map[string]TransportKind{
		"stdio": TransportStdio,
		"STDIO": TransportStdio,
		" sse ": TransportSSE,
		"http":  TransportHTTP,
		"ws":    TransportWS,
	} {
		got, err := resolveTransportKind(&raw)
		if err != nil {
			t.Errorf("resolveTransportKind(%q): %v", raw, err)
			continue
		}
		if got != want {
			t.Errorf("resolveTransportKind(%q) = %q, want %q", raw, got, want)
		}
	}
	if _, err := resolveTransportKind(nil); err == nil {
		t.Error("resolveTransportKind(nil) returned no error")
	}
}

func TestExpandEnvString(t *testing.T) {
	t.Setenv("NAMI_TEST_TOKEN", "secret-value")

	got, err := expandEnvString("Bearer $NAMI_TEST_TOKEN")
	if err != nil {
		t.Fatalf("expandEnvString: %v", err)
	}
	if got != "Bearer secret-value" {
		t.Fatalf("expanded = %q", got)
	}

	if got, err := expandEnvString("no variables"); err != nil || got != "no variables" {
		t.Fatalf("expandEnvString = %q, %v", got, err)
	}

	if _, err := expandEnvString("$NAMI_TEST_MISSING_VAR"); err == nil {
		t.Fatal("expandEnvString accepted an undefined variable")
	}
}

func TestExpandEnvCollections(t *testing.T) {
	t.Setenv("NAMI_TEST_DIR", "/opt/nami")

	args, err := expandEnvSlice([]string{"--root", "$NAMI_TEST_DIR"})
	if err != nil {
		t.Fatalf("expandEnvSlice: %v", err)
	}
	if len(args) != 2 || args[1] != "/opt/nami" {
		t.Fatalf("args = %#v", args)
	}
	if got, err := expandEnvSlice(nil); got != nil || err != nil {
		t.Fatalf("expandEnvSlice(nil) = %#v, %v", got, err)
	}
	if _, err := expandEnvSlice([]string{"$NAMI_TEST_MISSING_VAR"}); err == nil {
		t.Error("expandEnvSlice accepted an undefined variable")
	}

	env, err := expandEnvMap(map[string]string{"ROOT": "$NAMI_TEST_DIR"})
	if err != nil {
		t.Fatalf("expandEnvMap: %v", err)
	}
	if env["ROOT"] != "/opt/nami" {
		t.Fatalf("env = %#v", env)
	}
	if got, err := expandEnvMap(nil); got != nil || err != nil {
		t.Fatalf("expandEnvMap(nil) = %#v, %v", got, err)
	}
	if _, err := expandEnvMap(map[string]string{"A": "$NAMI_TEST_MISSING_VAR"}); err == nil {
		t.Error("expandEnvMap accepted an undefined variable")
	}
}

func TestNormalizeToolSet(t *testing.T) {
	if got := normalizeToolSet(nil); got != nil {
		t.Fatalf("normalizeToolSet(nil) = %#v, want nil", got)
	}
	if got := normalizeToolSet([]string{"  ", ""}); got != nil {
		t.Fatalf("normalizeToolSet(blank entries) = %#v, want nil", got)
	}
	got := normalizeToolSet([]string{" search ", "search", "fetch"})
	if len(got) != 2 {
		t.Fatalf("normalizeToolSet = %#v, want two entries", got)
	}
	if _, ok := got["search"]; !ok {
		t.Fatalf("normalizeToolSet = %#v, want trimmed keys", got)
	}
}

func TestNormalizeToolPermissions(t *testing.T) {
	permissions, err := normalizeToolPermissions(map[string]configpkg.MCPPermission{
		"reader": configpkg.MCPPermission("read"),
	})
	if err != nil {
		t.Fatalf("normalizeToolPermissions: %v", err)
	}
	if permissions["reader"] != ToolPermissionRead {
		t.Fatalf("permissions = %#v", permissions)
	}
	if got, err := normalizeToolPermissions(nil); got != nil || err != nil {
		t.Fatalf("normalizeToolPermissions(nil) = %#v, %v", got, err)
	}
}

func TestResolveKeepsToolFilters(t *testing.T) {
	cfg := stdioServer()
	cfg.IncludeTools = []string{"search"}
	cfg.ExcludeTools = []string{"delete"}

	definition, problems := resolveOne(t, "local", cfg)
	if len(problems) != 0 {
		t.Fatalf("problems = %+v", problems)
	}
	if _, ok := definition.IncludeTools["search"]; !ok {
		t.Errorf("IncludeTools = %#v", definition.IncludeTools)
	}
	if _, ok := definition.ExcludeTools["delete"]; !ok {
		t.Errorf("ExcludeTools = %#v", definition.ExcludeTools)
	}
}

func TestResolveExpandsStdioEnvironment(t *testing.T) {
	t.Setenv("NAMI_TEST_BIN", "/usr/local/bin/server")
	t.Setenv("NAMI_TEST_KEY", "abc123")

	cfg := configpkg.MCPServerConfig{
		Transport: ptr("stdio"),
		Command:   ptr("$NAMI_TEST_BIN"),
		Args:      []string{"--key", "$NAMI_TEST_KEY"},
		Env:       map[string]string{"API_KEY": "$NAMI_TEST_KEY"},
	}
	definition, problems := resolveOne(t, "local", cfg)
	if len(problems) != 0 {
		t.Fatalf("problems = %+v", problems)
	}
	if definition.Command != "/usr/local/bin/server" {
		t.Errorf("command = %q", definition.Command)
	}
	if definition.Args[1] != "abc123" {
		t.Errorf("args = %#v", definition.Args)
	}
	if definition.Env["API_KEY"] != "abc123" {
		t.Errorf("env = %#v", definition.Env)
	}
}
