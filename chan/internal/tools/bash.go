package tools

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/channyeintun/gocode/internal/bashsecurity"
)

const defaultBashTimeout = 30 * time.Second

var bashReadOnlyPrograms = map[string]struct{}{
	"cat":   {},
	"df":    {},
	"du":    {},
	"echo":  {},
	"file":  {},
	"find":  {},
	"grep":  {},
	"head":  {},
	"ls":    {},
	"pwd":   {},
	"rg":    {},
	"stat":  {},
	"tail":  {},
	"type":  {},
	"wc":    {},
	"which": {},
}

var bashReadOnlyGitSubcommands = map[string]struct{}{
	"blame":     {},
	"branch":    {},
	"diff":      {},
	"log":       {},
	"rev-parse": {},
	"show":      {},
	"status":    {},
	"tag":       {},
}

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
	return "Execute a shell command in the local workspace."
}

func (t *BashTool) InputSchema() any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
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
	if isParallelReadOnlyBashCommand(command) {
		return ConcurrencyParallel
	}
	return ConcurrencySerial
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
		result, err := renderBackgroundCommandResult(backgroundCommandResult{
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
	shellPath, err := resolveShellPath()
	if err != nil {
		return nil, err
	}
	return exec.CommandContext(ctx, shellPath, "-lc", command), nil
}

func shellCommand(command string) (*exec.Cmd, error) {
	shellPath, err := resolveShellPath()
	if err != nil {
		return nil, err
	}
	return exec.Command(shellPath, "-lc", command), nil
}

func resolveShellPath() (string, error) {
	candidates := []string{
		strings.TrimSpace(os.Getenv("SHELL")),
		"zsh",
		"bash",
		"sh",
		"/bin/zsh",
		"/bin/bash",
		"/bin/sh",
	}
	seen := make(map[string]struct{}, len(candidates))
	for _, candidate := range candidates {
		candidate = strings.TrimSpace(candidate)
		if candidate == "" {
			continue
		}

		resolved := candidate
		if !filepath.IsAbs(candidate) {
			lookup, err := exec.LookPath(candidate)
			if err != nil {
				continue
			}
			resolved = lookup
		}

		resolved = filepath.Clean(resolved)
		if _, ok := seen[resolved]; ok {
			continue
		}
		seen[resolved] = struct{}{}
		if !isSupportedShellBinary(resolved) {
			continue
		}
		info, err := os.Stat(resolved)
		if err != nil || info.IsDir() || info.Mode()&0o111 == 0 {
			continue
		}
		return resolved, nil
	}

	return "", fmt.Errorf("no supported shell found; checked $SHELL, zsh, bash, and sh")
}

func isSupportedShellBinary(path string) bool {
	switch filepath.Base(path) {
	case "zsh", "bash", "sh", "dash":
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

func isParallelReadOnlyBashCommand(command string) bool {
	segments, ok := splitBashCommandSegments(command)
	if !ok || len(segments) == 0 {
		return false
	}
	for _, segment := range segments {
		if !isReadOnlyBashSegment(segment) {
			return false
		}
	}
	return true
}

func splitBashCommandSegments(command string) ([]string, bool) {
	command = strings.TrimSpace(command)
	if command == "" {
		return nil, false
	}

	segments := make([]string, 0, 4)
	var current strings.Builder
	inSingle := false
	inDouble := false
	escaped := false

	flush := func() {
		segment := strings.TrimSpace(current.String())
		if segment != "" {
			segments = append(segments, segment)
		}
		current.Reset()
	}

	for index := 0; index < len(command); index++ {
		char := command[index]

		if escaped {
			current.WriteByte(char)
			escaped = false
			continue
		}

		switch char {
		case '\\':
			escaped = true
			current.WriteByte(char)
			continue
		case '\'':
			if !inDouble {
				inSingle = !inSingle
			}
			current.WriteByte(char)
			continue
		case '"':
			if !inSingle {
				inDouble = !inDouble
			}
			current.WriteByte(char)
			continue
		}

		if inSingle || inDouble {
			current.WriteByte(char)
			continue
		}

		switch char {
		case ';', '\n':
			flush()
			continue
		case '&':
			if index+1 < len(command) && command[index+1] == '&' {
				flush()
				index++
				continue
			}
			return nil, false
		case '|':
			flush()
			if index+1 < len(command) && command[index+1] == '|' {
				index++
			}
			continue
		case '>', '<', '(', ')', '{', '}', '`':
			return nil, false
		case '$':
			if index+1 < len(command) && command[index+1] == '(' {
				return nil, false
			}
		}

		current.WriteByte(char)
	}

	if escaped || inSingle || inDouble {
		return nil, false
	}
	flush()
	return segments, len(segments) > 0
}

func isReadOnlyBashSegment(segment string) bool {
	words := bashShellWords(segment)
	if len(words) == 0 {
		return false
	}

	wordIndex := 0
	for wordIndex < len(words) && isShellEnvAssignment(words[wordIndex]) {
		wordIndex++
	}
	if wordIndex >= len(words) {
		return false
	}

	program := words[wordIndex]
	if program == "git" {
		if wordIndex+1 >= len(words) {
			return false
		}
		_, ok := bashReadOnlyGitSubcommands[words[wordIndex+1]]
		return ok
	}

	_, ok := bashReadOnlyPrograms[program]
	return ok
}

func bashShellWords(command string) []string {
	words := make([]string, 0, 8)
	var current strings.Builder
	inSingle := false
	inDouble := false
	escaped := false

	flush := func() {
		if current.Len() == 0 {
			return
		}
		words = append(words, current.String())
		current.Reset()
	}

	for index := 0; index < len(command); index++ {
		char := command[index]
		if escaped {
			current.WriteByte(char)
			escaped = false
			continue
		}
		switch char {
		case '\\':
			escaped = true
		case '\'':
			if inDouble {
				current.WriteByte(char)
			} else {
				inSingle = !inSingle
			}
		case '"':
			if inSingle {
				current.WriteByte(char)
			} else {
				inDouble = !inDouble
			}
		case ' ', '\t', '\n':
			if inSingle || inDouble {
				current.WriteByte(char)
			} else {
				flush()
			}
		default:
			current.WriteByte(char)
		}
	}
	flush()
	return words
}

func isShellEnvAssignment(word string) bool {
	if word == "" {
		return false
	}
	equalsIndex := strings.IndexByte(word, '=')
	if equalsIndex <= 0 {
		return false
	}
	for _, char := range word[:equalsIndex] {
		if !(char == '_' || char >= 'A' && char <= 'Z' || char >= 'a' && char <= 'z' || char >= '0' && char <= '9') {
			return false
		}
	}
	return true
}
