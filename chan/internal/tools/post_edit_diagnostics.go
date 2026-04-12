package tools

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"
)

const postEditDiagnosticsTimeout = 8 * time.Second
const maxDiagnosticLines = 12
const maxDiagnosticChars = 2000

type diagnosticsScope struct {
	kind string
	root string
	file string
}

func runPostEditDiagnostics(ctx context.Context, changedPaths []string) string {
	if len(changedPaths) == 0 {
		return ""
	}

	scopes := collectDiagnosticsScopes(changedPaths)
	if len(scopes) == 0 {
		return ""
	}

	results := make([]string, 0, len(scopes))
	for _, scope := range scopes {
		select {
		case <-ctx.Done():
			return joinDiagnosticSections(results)
		default:
		}
		result := runDiagnosticsForScope(ctx, scope)
		if strings.TrimSpace(result) != "" {
			results = append(results, result)
		}
	}

	return joinDiagnosticSections(results)
}

func collectDiagnosticsScopes(changedPaths []string) []diagnosticsScope {
	seen := map[string]struct{}{}
	scopes := make([]diagnosticsScope, 0)
	for _, changedPath := range changedPaths {
		ext := strings.ToLower(filepath.Ext(changedPath))
		switch ext {
		case ".go":
			if root, ok := findNearestFile(filepath.Dir(changedPath), "go.mod"); ok {
				key := "go:" + root
				if _, exists := seen[key]; !exists {
					seen[key] = struct{}{}
					scopes = append(scopes, diagnosticsScope{kind: "go", root: filepath.Dir(root), file: root})
				}
			}
		case ".ts", ".tsx", ".mts", ".cts", ".js", ".jsx":
			if root, ok := findNearestFile(filepath.Dir(changedPath), "tsconfig.json"); ok {
				key := "ts:" + root
				if _, exists := seen[key]; !exists {
					seen[key] = struct{}{}
					scopes = append(scopes, diagnosticsScope{kind: "ts", root: filepath.Dir(root), file: root})
				}
			}
		}
	}
	sort.Slice(scopes, func(i, j int) bool {
		if scopes[i].kind == scopes[j].kind {
			return scopes[i].root < scopes[j].root
		}
		return scopes[i].kind < scopes[j].kind
	})
	return scopes
}

func runDiagnosticsForScope(parent context.Context, scope diagnosticsScope) string {
	ctx, cancel := context.WithTimeout(parent, postEditDiagnosticsTimeout)
	defer cancel()

	switch scope.kind {
	case "go":
		return runGoDiagnostics(ctx, scope)
	case "ts":
		return runTypeScriptDiagnostics(ctx, scope)
	default:
		return ""
	}
}

func runGoDiagnostics(ctx context.Context, scope diagnosticsScope) string {
	if _, err := exec.LookPath("go"); err != nil {
		return ""
	}
	cmd := exec.CommandContext(ctx, "go", "build", "./...")
	cmd.Dir = scope.root
	output, err := cmd.CombinedOutput()
	label := fmt.Sprintf("Go diagnostics (%s)", relativeDiagnosticsLabel(scope.root))
	if err == nil {
		return label + ": clean"
	}
	if ctx.Err() != nil {
		return label + ": skipped (timed out)"
	}
	trimmed := summarizeDiagnosticsOutput(output)
	if trimmed == "" {
		trimmed = err.Error()
	}
	return label + ":\n" + trimmed
}

func runTypeScriptDiagnostics(ctx context.Context, scope diagnosticsScope) string {
	command, args, ok := resolveLocalTypeScriptDiagnosticsCommand(scope)
	if !ok {
		return ""
	}
	cmd := exec.CommandContext(ctx, command, args...)
	cmd.Dir = scope.root
	output, err := cmd.CombinedOutput()
	label := fmt.Sprintf("TypeScript diagnostics (%s)", relativeDiagnosticsLabel(scope.file))
	if err == nil {
		return label + ": clean"
	}
	if ctx.Err() != nil {
		return label + ": skipped (timed out)"
	}
	trimmed := summarizeDiagnosticsOutput(output)
	if trimmed == "" {
		trimmed = err.Error()
	}
	return label + ":\n" + trimmed
}

func resolveLocalTypeScriptDiagnosticsCommand(scope diagnosticsScope) (string, []string, bool) {
	candidates := []string{filepath.Join("node_modules", ".bin", "tsc")}
	if runtime.GOOS == "windows" {
		candidates = append([]string{filepath.Join("node_modules", ".bin", "tsc.cmd")}, candidates...)
	}
	for _, candidate := range candidates {
		if resolved, ok := findNearestFile(scope.root, candidate); ok {
			return resolved, []string{"--noEmit", "-p", filepath.Base(scope.file)}, true
		}
	}
	return "", nil, false
}

func summarizeDiagnosticsOutput(output []byte) string {
	trimmed := strings.TrimSpace(string(bytes.TrimSpace(output)))
	if trimmed == "" {
		return ""
	}
	lines := strings.Split(trimmed, "\n")
	if len(lines) > maxDiagnosticLines {
		lines = append(lines[:maxDiagnosticLines], "...")
	}
	summary := strings.Join(lines, "\n")
	if len(summary) > maxDiagnosticChars {
		summary = summary[:maxDiagnosticChars-3] + "..."
	}
	return summary
}

func findNearestFile(startDir, fileName string) (string, bool) {
	dir := filepath.Clean(startDir)
	for {
		candidate := filepath.Join(dir, fileName)
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate, true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", false
		}
		dir = parent
	}
}

func relativeDiagnosticsLabel(path string) string {
	cwd, err := os.Getwd()
	if err != nil {
		return filepath.Clean(path)
	}
	rel, err := filepath.Rel(cwd, path)
	if err != nil || strings.HasPrefix(rel, "..") {
		return filepath.Clean(path)
	}
	return filepath.Clean(rel)
}

func joinDiagnosticSections(results []string) string {
	filtered := make([]string, 0, len(results))
	for _, result := range results {
		if strings.TrimSpace(result) != "" {
			filtered = append(filtered, result)
		}
	}
	return strings.Join(filtered, "\n\n")
}
