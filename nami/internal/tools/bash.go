package tools

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/channyeintun/nami/internal/bashsecurity"
)

const defaultBashTimeout = 30 * time.Second

type shellFamily string

const (
	shellFamilyPOSIX      shellFamily = "posix"
	shellFamilyPowerShell shellFamily = "powershell"
)

type localShell struct {
	family shellFamily
	path   string
}

var bashReadOnlyPrograms = map[string]struct{}{}

var bashReadOnlyGitSubcommands = map[string]struct{}{}

// BashTool executes shell commands through the preferred local shell with basic security validation.
type BashTool struct{}

// NewBashTool constructs the Bash execution tool.
func NewBashTool() *BashTool {
	return &BashTool{}
}

func (t *BashTool) Name() string {
	return "bash"
}

func (t *BashTool) Description() string {
	return "Execute a shell command in the local workspace. Provide a short description when possible so permission prompts can show intent clearly."
}

func (t *BashTool) InputSchema() any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"description": map[string]any{
				"type":        "string",
				"description": "Optional short explanation of what the command is meant to do.",
			},
			"command": map[string]any{
				"type":        "string",
				"description": "The shell command to execute.",
			},
			"background": map[string]any{
				"type":        "boolean",
				"description": "Start the command in the background and return a CommandId for follow-up status and stdin tools.",
			},
			"cwd": map[string]any{
				"type":        "string",
				"description": "Optional working directory for the command.",
			},
			"timeout_ms": map[string]any{
				"type":        "integer",
				"description": "Optional timeout in milliseconds. Defaults to 30000.",
				"minimum":     1,
			},
		},
		"required": []string{"command"},
	}
}

func (t *BashTool) Permission() PermissionLevel {
	return PermissionExecute
}

func (t *BashTool) Concurrency(input ToolInput) ConcurrencyDecision {
	if firstBoolParam(input.Params, "background") {
		return ConcurrencySerial
	}
	command, _ := stringParam(input.Params, "command")
	if bashsecurity.IsReadOnlyBashCommand(command) {
		return ConcurrencyParallel
	}
	return ConcurrencySerial
}

func (t *BashTool) PermissionTarget(input ToolInput) PermissionTarget {
	command, _ := stringParam(input.Params, "command")
	description, _ := stringParam(input.Params, "description")
	workingDir, _ := stringParam(input.Params, "cwd")
	value := strings.TrimSpace(command)
	trimmedDescription := strings.TrimSpace(description)
	if trimmedDescription != "" {
		if value == "" {
			value = trimmedDescription
		} else {
			value = trimmedDescription + " :: " + value
		}
	}
	return PermissionTarget{Kind: "command", Value: value, WorkingDir: strings.TrimSpace(workingDir)}
}

func (t *BashTool) Validate(input ToolInput) error {
	command, ok := stringParam(input.Params, "command")
	if !ok || strings.TrimSpace(command) == "" {
		return fmt.Errorf("bash requires a non-empty command")
	}
	return nil
}

func (t *BashTool) Execute(ctx context.Context, input ToolInput) (ToolOutput, error) {
	command, ok := stringParam(input.Params, "command")
	if !ok || strings.TrimSpace(command) == "" {
		return ToolOutput{}, fmt.Errorf("bash tool requires a non-empty command")
	}

	if blocked := validateBashSecurity(command); blocked != "" {
		return ToolOutput{Output: blocked, IsError: true}, nil
	}

	workingDir, err := resolveWorkingDirectory(input.Params)
	if err != nil {
		return ToolOutput{}, err
	}

	if firstBoolParam(input.Params, "background") {
		bg, err := startBackgroundShellCommand(command, workingDir)
		if err != nil {
			return ToolOutput{}, err
		}
		result, err := renderBackgroundCommandResult(BackgroundCommandResult{
			CommandID: bg.id,
			Command:   command,
			Cwd:       workingDir,
			Running:   true,
			StartedAt: bg.startedAt,
			UpdatedAt: bg.updatedAt,
		})
		if err != nil {
			return ToolOutput{}, fmt.Errorf("render background command result: %w", err)
		}
		return ToolOutput{Output: result}, nil
	}

	commandCtx := ctx
	if timeout := timeoutFromParams(input.Params); timeout > 0 {
		var cancel context.CancelFunc
		commandCtx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}

	cmd, err := shellCommandContext(commandCtx, command)
	if err != nil {
		return ToolOutput{}, err
	}
	cmd.Dir = workingDir

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err = cmd.Run()
	combined := strings.TrimSpace(joinOutputs(stdout.String(), stderr.String()))
	if warning := checkDestructive(command); warning != "" {
		combined = strings.TrimSpace(warning + "\n" + combined)
	}

	if err != nil {
		if commandCtx.Err() != nil {
			return ToolOutput{}, commandCtx.Err()
		}
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return ToolOutput{Output: combined, IsError: true}, nil
		}
		return ToolOutput{}, fmt.Errorf("run bash command: %w", err)
	}

	return ToolOutput{Output: combined}, nil
}

func resolveWorkingDirectory(params map[string]any) (string, error) {
	workingDir, ok := stringParam(params, "cwd")
	if !ok || strings.TrimSpace(workingDir) == "" {
		return os.Getwd()
	}
	if !filepath.IsAbs(workingDir) {
		cwd, err := os.Getwd()
		if err != nil {
			return "", fmt.Errorf("get working directory: %w", err)
		}
		workingDir = filepath.Join(cwd, workingDir)
	}
	info, err := os.Stat(workingDir)
	if err != nil {
		return "", fmt.Errorf("stat cwd %q: %w", workingDir, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("cwd %q is not a directory", workingDir)
	}
	return workingDir, nil
}

func timeoutFromParams(params map[string]any) time.Duration {
	value, ok := params["timeout_ms"]
	if !ok {
		return defaultBashTimeout
	}
	switch v := value.(type) {
	case int:
		if v > 0 {
			return time.Duration(v) * time.Millisecond
		}
	case int64:
		if v > 0 {
			return time.Duration(v) * time.Millisecond
		}
	case float64:
		if v > 0 {
			return time.Duration(v) * time.Millisecond
		}
	case string:
		parsed, err := strconv.Atoi(v)
		if err == nil && parsed > 0 {
			return time.Duration(parsed) * time.Millisecond
		}
	}
	return defaultBashTimeout
}

func shellCommandContext(ctx context.Context, command string) (*exec.Cmd, error) {
	shell, err := resolveLocalShell()
	if err != nil {
		return nil, err
	}
	return exec.CommandContext(ctx, shell.path, shell.execArgs(command)...), nil
}

func shellCommand(command string) (*exec.Cmd, error) {
	shell, err := resolveLocalShell()
	if err != nil {
		return nil, err
	}
	return exec.Command(shell.path, shell.execArgs(command)...), nil
}

func (s localShell) execArgs(command string) []string {
	switch s.family {
	case shellFamilyPowerShell:
		return []string{"-NoProfile", "-NonInteractive", "-Command", command}
	default:
		return []string{"-lc", command}
	}
}

func resolveLocalShell() (localShell, error) {
	if runtime.GOOS == "windows" {
		return resolveWindowsShell()
	}
	return resolvePOSIXShell()
}

func resolvePOSIXShell() (localShell, error) {
	candidates := []string{
		strings.TrimSpace(os.Getenv("SHELL")),
		"zsh",
		"bash",
		"sh",
		"/bin/zsh",
		"/bin/bash",
		"/bin/sh",
	}
	resolved, err := resolveShellCandidate(candidates, isSupportedPOSIXShellBinary)
	if err != nil {
		return localShell{}, fmt.Errorf("no supported shell found; checked $SHELL, zsh, bash, and sh")
	}
	return localShell{family: shellFamilyPOSIX, path: resolved}, nil
}

func resolveWindowsShell() (localShell, error) {
	candidates := []string{
		strings.TrimSpace(os.Getenv("NAMI_POWERSHELL")),
		strings.TrimSpace(os.Getenv("NAMI_WINDOWS_SHELL")),
		"pwsh",
		"pwsh.exe",
		"powershell",
		"powershell.exe",
	}
	if programFiles := strings.TrimSpace(os.Getenv("ProgramFiles")); programFiles != "" {
		candidates = append(candidates, filepath.Join(programFiles, "PowerShell", "7", "pwsh.exe"))
	}
	if systemRoot := strings.TrimSpace(os.Getenv("SystemRoot")); systemRoot != "" {
		candidates = append(candidates, filepath.Join(systemRoot, "System32", "WindowsPowerShell", "v1.0", "powershell.exe"))
	}
	resolved, err := resolveShellCandidate(candidates, isSupportedWindowsShellBinary)
	if err != nil {
		return localShell{}, fmt.Errorf("no supported PowerShell installation found; checked NAMI_POWERSHELL, pwsh, and powershell")
	}
	return localShell{family: shellFamilyPowerShell, path: resolved}, nil
}

func resolveShellCandidate(candidates []string, supported func(string) bool) (string, error) {
	seen := make(map[string]struct{}, len(candidates))
	for _, candidate := range candidates {
		candidate = strings.TrimSpace(candidate)
		if candidate == "" {
			continue
		}

		resolved, ok := resolveExecutableCandidate(candidate)
		if !ok {
			continue
		}
		if _, ok := seen[resolved]; ok {
			continue
		}
		seen[resolved] = struct{}{}
		if !supported(resolved) {
			continue
		}
		return resolved, nil
	}

	return "", fmt.Errorf("no supported shell found")
}

func resolveExecutableCandidate(candidate string) (string, bool) {
	resolved := candidate
	if !filepath.IsAbs(candidate) {
		lookup, err := exec.LookPath(candidate)
		if err != nil {
			return "", false
		}
		resolved = lookup
	}

	resolved = filepath.Clean(resolved)
	info, err := os.Stat(resolved)
	if err != nil || info.IsDir() {
		return "", false
	}
	if runtime.GOOS != "windows" && info.Mode()&0o111 == 0 {
		return "", false
	}
	return resolved, true
}

func isSupportedPOSIXShellBinary(path string) bool {
	switch filepath.Base(path) {
	case "zsh", "bash", "sh", "dash":
		return true
	default:
		return false
	}
}

func isSupportedWindowsShellBinary(path string) bool {
	base := strings.ToLower(filepath.Base(path))
	base = strings.TrimSuffix(base, ".exe")
	switch base {
	case "pwsh", "powershell":
		return true
	default:
		return false
	}
}

func stringParam(params map[string]any, key string) (string, bool) {
	value, ok := params[key]
	if !ok {
		return "", false
	}
	stringValue, ok := value.(string)
	return stringValue, ok
}

func joinOutputs(stdout, stderr string) string {
	stdout = strings.TrimSpace(stdout)
	stderr = strings.TrimSpace(stderr)
	switch {
	case stdout == "":
		return stderr
	case stderr == "":
		return stdout
	default:
		return stdout + "\n" + stderr
	}
}

func validateBashSecurity(command string) string {
	return bashsecurity.ValidateBashSecurity(command)
}

func checkDestructive(command string) string {
	return bashsecurity.CheckDestructive(command)
}
