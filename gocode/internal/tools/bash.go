package tools

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const defaultBashTimeout = 30 * time.Second

var bashDangerousZshCommands = regexp.MustCompile(`\b(zmodload|emulate|sysopen|sysread|syswrite|zpty|ztcp|zsocket|zf_rm|zf_mv|zf_chmod|zf_mkdir|zf_chown|mapfile)\b`)
var bashDangerousSubstitution = regexp.MustCompile(`<\(|>\(`)
var bashIFSInjection = regexp.MustCompile(`\bIFS=`)
var bashReadOnlyCommands = regexp.MustCompile(`^\s*(git\s+(diff|status|log|show|branch|tag)|ls|cat|find|rg|grep|wc|head|tail|echo|pwd|which|type|file|stat|du|df)\b`)

var bashDestructivePatterns = []struct {
	pattern     *regexp.Regexp
	description string
}{
	{regexp.MustCompile(`git\s+reset\s+--hard`), "git reset --hard"},
	{regexp.MustCompile(`git\s+push\s+.*--force`), "git push --force"},
	{regexp.MustCompile(`git\s+push\s+-f\b`), "git push -f"},
	{regexp.MustCompile(`git\s+clean\s+-f`), "git clean -f"},
	{regexp.MustCompile(`git\s+checkout\s+\.\s*$`), "git checkout ."},
	{regexp.MustCompile(`git\s+commit\s+.*--amend`), "git commit --amend"},
	{regexp.MustCompile(`--no-verify`), "--no-verify"},
	{regexp.MustCompile(`\brm\s+-rf\b`), "rm -rf"},
	{regexp.MustCompile(`\brm\s+-f\b`), "rm -f"},
	{regexp.MustCompile(`(?i)\bDROP\s+TABLE\b`), "DROP TABLE"},
	{regexp.MustCompile(`(?i)\bTRUNCATE\b`), "TRUNCATE"},
	{regexp.MustCompile(`(?i)\bDELETE\s+FROM\b`), "DELETE FROM"},
	{regexp.MustCompile(`\bkubectl\s+delete\b`), "kubectl delete"},
	{regexp.MustCompile(`\bterraform\s+destroy\b`), "terraform destroy"},
}

// BashTool executes shell commands through zsh with basic security validation.
type BashTool struct{}

// NewBashTool constructs the Bash execution tool.
func NewBashTool() *BashTool {
	return &BashTool{}
}

func (t *BashTool) Name() string {
	return "bash"
}

func (t *BashTool) Description() string {
	return "Execute a zsh shell command in the local workspace."
}

func (t *BashTool) InputSchema() any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"command": map[string]any{
				"type":        "string",
				"description": "The zsh command to execute.",
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
	if bashReadOnlyCommands.MatchString(command) {
		return ConcurrencyParallel
	}
	return ConcurrencySerial
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
		result, err := renderBackgroundCommandResult(backgroundCommandResult{
			CommandID: bg.id,
			Running:   true,
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

	cmd := exec.CommandContext(commandCtx, "/bin/zsh", "-lc", command)
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
	if bashDangerousZshCommands.MatchString(command) {
		return "blocked: dangerous ZSH command detected"
	}
	if bashDangerousSubstitution.MatchString(command) {
		return "blocked: dangerous process substitution pattern"
	}
	if bashIFSInjection.MatchString(command) {
		return "blocked: IFS injection detected"
	}
	return ""
}

func checkDestructive(command string) string {
	for _, pattern := range bashDestructivePatterns {
		if pattern.pattern.MatchString(command) {
			return "warning: destructive command — " + pattern.description
		}
	}
	return ""
}
