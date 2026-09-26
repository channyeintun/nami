package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"
)

const backgroundCommandSummaryPreviewBytes = 160
const backgroundCommandNotificationPreviewBytes = 4096

func forgetBackgroundCommand(commandID string) (BackgroundCommandResult, error) {
	bg, err := getBackgroundCommand(commandID)
	if err != nil {
		return BackgroundCommandResult{}, err
	}

	// Queue behind the command's other consumers before taking the registry
	// lock: a status wait can hold consumeMu for minutes, and holding the
	// registry lock meanwhile stalled every background command, new ones too.
	bg.consumeMu.Lock()
	defer bg.consumeMu.Unlock()

	backgroundCommandsMu.Lock()
	if backgroundCommands[commandID] != bg {
		backgroundCommandsMu.Unlock()
		return BackgroundCommandResult{}, fmt.Errorf("command %q not found", commandID)
	}

	bg.mu.Lock()
	if bg.running {
		bg.mu.Unlock()
		backgroundCommandsMu.Unlock()
		return BackgroundCommandResult{}, fmt.Errorf("command %q is still running; stop it before forgetting it", bg.id)
	}

	result := BackgroundCommandResult{
		CommandID: bg.id,
		Command:   bg.command,
		Cwd:       bg.cwd,
		Running:   false,
		StartedAt: bg.startedAt,
		UpdatedAt: bg.updatedAt,
		Error:     bg.errText,
	}
	if bg.exitCode != nil {
		copied := *bg.exitCode
		result.ExitCode = &copied
	}
	bg.mu.Unlock()

	delete(backgroundCommands, commandID)
	backgroundCommandsMu.Unlock()

	result.Output = bg.output.ReadDelta()
	return result, nil
}

func (bg *backgroundCommand) sendInput(ctx context.Context, input string, wait time.Duration) (BackgroundCommandResult, error) {
	bg.consumeMu.Lock()
	defer bg.consumeMu.Unlock()

	bg.mu.Lock()
	running := bg.running
	stdin := bg.stdin
	bg.mu.Unlock()
	// shutdown drops stdin before the exit handler marks the command finished.
	if !running || stdin == nil {
		return BackgroundCommandResult{}, fmt.Errorf("command %q is not running", bg.id)
	}
	// The write runs without bg.mu: a process that stops reading its input
	// blocks it, and listing, the exit handler, and shutdown all need bg.mu.
	if err := bg.writeInput(ctx, stdin, input); err != nil {
		return BackgroundCommandResult{}, fmt.Errorf("write command input: %w", err)
	}
	bg.markUpdated(time.Now())

	if err := bg.waitForExit(ctx, wait); err != nil {
		return BackgroundCommandResult{}, err
	}
	return bg.snapshotDelta(), nil
}

// waitForExit waits up to wait for the command to exit, returning early with
// ctx's error once the turn is cancelled. Callers hold consumeMu while they
// wait, so a wait that outlived its turn kept the command from everyone else.
func (bg *backgroundCommand) waitForExit(ctx context.Context, wait time.Duration) error {
	if wait <= 0 {
		return nil
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case <-bg.done:
		return nil
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// writeInput writes input to the command's stdin. A process that stops reading
// lets the terminal buffer fill and the write block in the kernel, where no
// deadline or close can reach it: creack/pty leaves the terminal in blocking
// mode, and the write can stay stuck even after the process exits. So the
// write runs on its own goroutine, and the caller stops waiting for it once
// ctx ends or the command exits, abandoning the write.
func (bg *backgroundCommand) writeInput(ctx context.Context, stdin io.Writer, input string) error {
	written := make(chan error, 1)
	go func() {
		_, err := io.WriteString(stdin, input)
		written <- err
	}()
	select {
	case err := <-written:
		return err
	case <-ctx.Done():
		return ctx.Err()
	case <-bg.done:
		select {
		case err := <-written:
			return err
		default:
			return fmt.Errorf("command %q exited before reading its input", bg.id)
		}
	}
}

func (bg *backgroundCommand) status(ctx context.Context, wait time.Duration) (BackgroundCommandResult, error) {
	bg.consumeMu.Lock()
	defer bg.consumeMu.Unlock()

	if err := bg.waitForExit(ctx, wait); err != nil {
		return BackgroundCommandResult{}, err
	}
	return bg.snapshotDelta(), nil
}

func (bg *backgroundCommand) stop(ctx context.Context, wait time.Duration) (BackgroundCommandResult, error) {
	// Shut down before queueing behind other consumers: send_command_input
	// stuck on a process that stopped reading holds consumeMu until the
	// command exits, and shutdown is what makes it exit.
	bg.shutdown()

	bg.consumeMu.Lock()
	defer bg.consumeMu.Unlock()

	if err := bg.waitForExit(ctx, wait); err != nil {
		return BackgroundCommandResult{}, err
	}
	return bg.snapshotDelta(), nil
}

func (bg *backgroundCommand) snapshotDelta() BackgroundCommandResult {
	bg.mu.Lock()
	running := bg.running
	errText := bg.errText
	updatedAt := bg.updatedAt
	var exitCode *int
	if bg.exitCode != nil {
		copied := *bg.exitCode
		exitCode = &copied
	}
	bg.mu.Unlock()

	return BackgroundCommandResult{
		CommandID: bg.id,
		Command:   bg.command,
		Cwd:       bg.cwd,
		Running:   running,
		StartedAt: bg.startedAt,
		UpdatedAt: updatedAt,
		Output:    bg.output.ReadDelta(),
		Error:     errText,
		ExitCode:  exitCode,
	}
}

func (bg *backgroundCommand) detail(limit int) BackgroundCommandDetail {
	bg.mu.Lock()
	running := bg.running
	errText := bg.errText
	commandID := bg.id
	command := bg.command
	cwd := bg.cwd
	startedAt := bg.startedAt
	updatedAt := bg.updatedAt
	var exitCode *int
	if bg.exitCode != nil {
		copied := *bg.exitCode
		exitCode = &copied
	}
	bg.mu.Unlock()

	unread := bg.output.unreadSummary(limit)

	return BackgroundCommandDetail{
		CommandID:       commandID,
		Command:         command,
		Cwd:             cwd,
		Status:          backgroundCommandAsyncStatus(running, exitCode, errText),
		Running:         running,
		StartedAt:       startedAt,
		UpdatedAt:       updatedAt,
		Output:          bg.output.tail(limit),
		HasUnreadOutput: unread.HasUnread,
		UnreadBytes:     unread.UnreadBytes,
		ExitCode:        exitCode,
		Error:           errText,
	}
}

func (bg *backgroundCommand) summary() backgroundCommandSummary {
	bg.mu.Lock()
	startedAt := bg.startedAt
	updatedAt := bg.updatedAt
	defer bg.mu.Unlock()

	var exitCode *int
	if bg.exitCode != nil {
		copied := *bg.exitCode
		exitCode = &copied
	}
	unread := bg.output.unreadSummary(backgroundCommandSummaryPreviewBytes)

	return backgroundCommandSummary{
		CommandID:       bg.id,
		Command:         bg.command,
		Cwd:             bg.cwd,
		Running:         bg.running,
		Error:           bg.errText,
		ExitCode:        exitCode,
		StartedAt:       startedAt,
		UpdatedAt:       updatedAt,
		HasUnreadOutput: unread.HasUnread,
		UnreadBytes:     unread.UnreadBytes,
		UnreadPreview:   unread.Preview,
	}
}

func (bg *backgroundCommand) markUpdated(at time.Time) {
	bg.mu.Lock()
	defer bg.mu.Unlock()
	bg.updatedAt = at
}

func (bg *backgroundCommand) asyncUpdate() BackgroundCommandUpdate {
	bg.mu.Lock()
	running := bg.running
	errText := bg.errText
	startedAt := bg.startedAt
	updatedAt := bg.updatedAt
	command := bg.command
	cwd := bg.cwd
	commandID := bg.id
	sessionID := bg.sessionID
	var exitCode *int
	if bg.exitCode != nil {
		copied := *bg.exitCode
		exitCode = &copied
	}
	bg.mu.Unlock()

	unread := bg.output.unreadSummary(backgroundCommandNotificationPreviewBytes)

	return BackgroundCommandUpdate{
		SessionID:       sessionID,
		CommandID:       commandID,
		Command:         command,
		Cwd:             cwd,
		Status:          backgroundCommandAsyncStatus(running, exitCode, errText),
		Running:         running,
		StartedAt:       startedAt,
		UpdatedAt:       updatedAt,
		OutputPreview:   unread.Preview,
		HasUnreadOutput: unread.HasUnread,
		UnreadBytes:     unread.UnreadBytes,
		ExitCode:        exitCode,
		Error:           errText,
	}
}

func backgroundCommandAsyncStatus(running bool, exitCode *int, errText string) string {
	if running {
		return "running"
	}
	if exitCode != nil && *exitCode != 0 {
		return "failed"
	}
	if strings.TrimSpace(errText) != "" {
		return "failed"
	}
	return "completed"
}

func renderBackgroundCommandResult(result BackgroundCommandResult) (string, error) {
	encoded, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return "", err
	}
	return string(encoded), nil
}

func InspectBackgroundCommand(ctx context.Context, commandID string, wait time.Duration, tailBytes int) (BackgroundCommandDetail, error) {
	bg, err := getBackgroundCommand(commandID)
	if err != nil {
		return BackgroundCommandDetail{}, err
	}

	if err := bg.waitForExit(ctx, wait); err != nil {
		return BackgroundCommandDetail{}, err
	}
	return bg.detail(tailBytes), nil
}

func StopBackgroundCommand(commandID string, wait time.Duration) (BackgroundCommandResult, error) {
	bg, err := getBackgroundCommand(commandID)
	if err != nil {
		return BackgroundCommandResult{}, err
	}
	return bg.stop(context.Background(), wait)
}

func BackgroundCommandUpdateSnapshot(commandID string) (BackgroundCommandUpdate, error) {
	bg, err := getBackgroundCommand(commandID)
	if err != nil {
		return BackgroundCommandUpdate{}, err
	}
	return bg.asyncUpdate(), nil
}
