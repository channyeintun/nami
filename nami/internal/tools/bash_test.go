package tools

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func skipWithoutPOSIXShell(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("these commands are POSIX shell syntax")
	}
}

// A timeout has to stop everything the command started. Killing only the
// shell left its children running, and because they inherited the output
// pipes, the call itself kept waiting for them long past the timeout.
func TestBashTimeoutKillsTheWholeProcessTree(t *testing.T) {
	skipWithoutPOSIXShell(t)
	dir := t.TempDir()

	start := time.Now()
	out, err := NewBashTool().Execute(t.Context(), ToolInput{Params: map[string]any{
		"command":    "(sleep 1; echo alive > survived) & sleep 10",
		"cwd":        dir,
		"timeout_ms": 200,
	}})
	elapsed := time.Since(start)

	if err != nil || !out.IsError || !strings.Contains(out.Output, "timed out after 200ms") {
		t.Fatalf("Execute = %+v, %v; want a failed call that says it timed out", out, err)
	}
	if elapsed > 5*time.Second {
		t.Fatalf("Execute returned after %v; the 200ms timeout did not stop the command", elapsed)
	}
	time.Sleep(1500 * time.Millisecond)
	if _, err := os.Stat(filepath.Join(dir, "survived")); err == nil {
		t.Fatal("a process started by the timed-out command kept running")
	}
}

// The output a command printed before it timed out is often what shows why it
// hung, so it is returned with the failure rather than dropped.
func TestBashTimeoutKeepsTheOutputSoFar(t *testing.T) {
	skipWithoutPOSIXShell(t)
	out, err := NewBashTool().Execute(t.Context(), ToolInput{Params: map[string]any{
		"command":    "echo 'waiting for the lock'; sleep 10",
		"cwd":        t.TempDir(),
		"timeout_ms": 300,
	}})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !out.IsError || !strings.Contains(out.Output, "waiting for the lock") {
		t.Fatalf("Execute = %+v, want the output so far in a failed result", out)
	}
}

func TestBashCancelKillsTheWholeProcessTree(t *testing.T) {
	skipWithoutPOSIXShell(t)
	ctx, cancel := context.WithCancel(t.Context())
	time.AfterFunc(200*time.Millisecond, cancel)

	start := time.Now()
	_, err := NewBashTool().Execute(ctx, ToolInput{Params: map[string]any{
		"command": "sleep 10; echo finished",
	}})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Execute error = %v, want context.Canceled", err)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("Execute returned after %v; cancelling did not stop the command", elapsed)
	}
}

// A process deliberately left running with "&" keeps the output pipes open.
// The call has to return once the command itself exits instead of waiting
// for that process to finish.
func TestBashReturnsWhenABackgroundedProcessHoldsTheOutput(t *testing.T) {
	skipWithoutPOSIXShell(t)

	start := time.Now()
	output, err := NewBashTool().Execute(t.Context(), ToolInput{Params: map[string]any{
		"command":    "sleep 10 & echo started",
		"timeout_ms": 30000,
	}})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if elapsed := time.Since(start); elapsed > commandWaitDelay+4*time.Second {
		t.Fatalf("Execute returned after %v, want it bounded by the %v wait delay", elapsed, commandWaitDelay)
	}
	if output.IsError {
		t.Fatalf("output = %+v, want success", output)
	}
	if !strings.HasPrefix(output.Output, "started") || !strings.Contains(output.Output, "still running") {
		t.Fatalf("output = %q, want the command output and a note about the background process", output.Output)
	}
}
