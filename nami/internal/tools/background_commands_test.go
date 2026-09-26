package tools

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func startTestBackgroundCommand(t *testing.T, command string) *backgroundCommand {
	t.Helper()
	bg, err := startBackgroundShellCommand(command, t.TempDir())
	if err != nil {
		t.Fatalf("start background command: %v", err)
	}
	t.Cleanup(func() {
		bg.shutdown()
		backgroundCommandsMu.Lock()
		delete(backgroundCommands, bg.id)
		backgroundCommandsMu.Unlock()
	})
	return bg
}

func waitForBackgroundExit(t *testing.T, bg *backgroundCommand, limit time.Duration) {
	t.Helper()
	select {
	case <-bg.done:
	case <-time.After(limit):
		t.Fatalf("background command %s did not finish within %v", bg.id, limit)
	}
}

// Once a background command has finished, all of its output has to be
// readable. The exit handler used to cancel the readers and close the terminal
// as soon as the process exited, dropping whatever was still buffered, which
// is usually the part that matters: the summary or the error at the end.
func TestBackgroundCommandKeepsTheTailOfItsOutput(t *testing.T) {
	skipWithoutPOSIXShell(t)
	const command = "i=0; while [ $i -lt 200 ]; do echo line$i; i=$((i+1)); done; echo FINAL-MARKER"

	// The loss was a race, so one run proves little.
	for run := range 25 {
		bg := startTestBackgroundCommand(t, command)
		waitForBackgroundExit(t, bg, 10*time.Second)

		output := bg.snapshotDelta().Output
		if !strings.Contains(output, "FINAL-MARKER") {
			t.Fatalf("run %d: output lost its tail; ends with %q", run, lastBytes(output, 80))
		}
		if strings.Contains(output, "stream closed") {
			t.Fatalf("run %d: output reports a stream error: %q", run, lastBytes(output, 200))
		}
	}
}

// Waiting for the output to drain must stay bounded: a process the command
// left running holds the terminal open and never lets the readers see its end.
// It ignores SIGHUP, so the hangup the shell's exit sends does not end it.
func TestBackgroundCommandFinishesWhileALeftoverProcessHoldsTheTerminal(t *testing.T) {
	skipWithoutPOSIXShell(t)
	bg := startTestBackgroundCommand(t, "(trap '' HUP; exec sleep 10) & echo started")

	waitForBackgroundExit(t, bg, commandWaitDelay+4*time.Second)
	if output := bg.snapshotDelta().Output; !strings.Contains(output, "started") {
		t.Fatalf("output = %q, want the command's output", output)
	}
}

// Stopping a command has to stop everything it started. Killing only the shell
// relied on the terminal hangup to take the rest down, which misses any process
// that ignores SIGHUP.
func TestStopKillsEverythingTheBackgroundCommandStarted(t *testing.T) {
	skipWithoutPOSIXShell(t)
	bg := startTestBackgroundCommand(t, "(trap '' HUP; echo ready; sleep 1; echo alive > survived) & wait")
	waitForBackgroundOutput(t, bg, "ready", 5*time.Second)

	result := bg.stop(2 * time.Second)
	if result.Running {
		t.Fatalf("stop returned a running command: %+v", result)
	}
	time.Sleep(1500 * time.Millisecond)
	if _, err := os.Stat(filepath.Join(bg.cwd, "survived")); err == nil {
		t.Fatal("a process started by the stopped command kept running")
	}
}

// unreadInput is far more than the terminal buffers hold, so writing it to a
// process that never reads its stdin blocks.
var unreadInput = strings.Repeat("0123456789abcdef\n", 64*1024)

// A write to a process that stopped reading used to block while holding the
// command's lock, which froze list_commands, the exit handler, and stop, the
// very things that could have released it.
func TestSendInputStuckOnACommandThatStopsReadingBlocksNobodyElse(t *testing.T) {
	skipWithoutPOSIXShell(t)
	bg := startTestBackgroundCommand(t, "sleep 30")

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	sent := make(chan error, 1)
	go func() {
		_, err := bg.sendInput(ctx, unreadInput, 0)
		sent <- err
	}()
	time.Sleep(300 * time.Millisecond)

	listed := make(chan struct{})
	go func() {
		_ = bg.summary()
		close(listed)
	}()
	select {
	case <-listed:
	case <-time.After(2 * time.Second):
		t.Fatal("listing the command blocked behind a stuck input write")
	}

	cancel()
	select {
	case err := <-sent:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("sendInput error = %v, want context.Canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("sendInput ignored cancellation while its write was stuck")
	}
}

func TestStopReleasesAnInputWriteStuckOnACommandThatStopsReading(t *testing.T) {
	skipWithoutPOSIXShell(t)
	bg := startTestBackgroundCommand(t, "sleep 30")

	sent := make(chan error, 1)
	go func() {
		_, err := bg.sendInput(context.Background(), unreadInput, 0)
		sent <- err
	}()
	time.Sleep(300 * time.Millisecond)

	stopped := make(chan BackgroundCommandResult, 1)
	go func() { stopped <- bg.stop(2 * time.Second) }()
	select {
	case result := <-stopped:
		if result.Running {
			t.Fatalf("stop returned a running command: %+v", result)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("stop blocked behind a stuck input write")
	}
	select {
	case err := <-sent:
		if err == nil {
			t.Fatal("sendInput reported success for input the stopped process never read")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("stopping the command did not release the stuck write")
	}
}

// shutdown drops stdin before the exit handler records the exit, so input sent
// in that window has to be refused rather than written to a nil stream.
func TestSendInputRefusesACommandBeingShutDown(t *testing.T) {
	bg := &backgroundCommand{id: "cmd_stopping", running: true, output: &boundedOutput{}, done: make(chan struct{})}
	if _, err := bg.sendInput(t.Context(), "y\n", 0); err == nil {
		t.Fatal("sendInput accepted input for a command whose stdin is gone")
	}
}

func waitForBackgroundOutput(t *testing.T, bg *backgroundCommand, want string, limit time.Duration) {
	t.Helper()
	deadline := time.Now().Add(limit)
	for !strings.Contains(bg.output.tail(0), want) {
		if time.Now().After(deadline) {
			t.Fatalf("background command %s never printed %q", bg.id, want)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func lastBytes(text string, n int) string {
	if len(text) <= n {
		return text
	}
	return text[len(text)-n:]
}
