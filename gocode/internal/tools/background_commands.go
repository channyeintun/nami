package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/creack/pty"
)

const backgroundCommandMaxOutputBytes = 256 * 1024

const backgroundCommandRetention = 5 * time.Minute

type backgroundCommand struct {
	mu        sync.Mutex
	consumeMu sync.Mutex
	id        string
	command   string
	cwd       string
	cmd       *exec.Cmd
	stdin     io.WriteCloser
	terminal  *os.File
	cancel    context.CancelFunc
	output    *boundedOutput
	running   bool
	exitCode  *int
	errText   string
	done      chan struct{}
}

type boundedOutput struct {
	mu               sync.Mutex
	data             []byte
	readOffset       int
	droppedUnreadLen int
}

type backgroundCommandResult struct {
	CommandID string `json:"CommandId"`
	Running   bool   `json:"Running"`
	Output    string `json:"Output,omitempty"`
	Error     string `json:"Error,omitempty"`
	ExitCode  *int   `json:"ExitCode,omitempty"`
}

var (
	backgroundCommands   = make(map[string]*backgroundCommand)
	backgroundCommandsMu sync.RWMutex
	backgroundCounter    uint64
)

func startBackgroundShellCommand(command, cwd string) (*backgroundCommand, error) {
	id := fmt.Sprintf("cmd_%d", atomic.AddUint64(&backgroundCounter, 1))
	cmd := exec.Command("/bin/zsh", "-lc", command)
	cmd.Dir = cwd
	streamCtx, cancel := context.WithCancel(context.Background())

	terminal, err := pty.Start(cmd)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("start background command in pty: %w", err)
	}

	bg := &backgroundCommand{
		id:       id,
		command:  command,
		cwd:      cwd,
		cmd:      cmd,
		stdin:    terminal,
		terminal: terminal,
		cancel:   cancel,
		output:   &boundedOutput{},
		running:  true,
		done:     make(chan struct{}),
	}

	backgroundCommandsMu.Lock()
	backgroundCommands[id] = bg
	backgroundCommandsMu.Unlock()

	go streamBackgroundOutput(streamCtx, bg.output, terminal)
	go waitForBackgroundCommand(bg)

	return bg, nil
}

func waitForBackgroundCommand(bg *backgroundCommand) {
	err := bg.cmd.Wait()

	bg.mu.Lock()
	defer bg.mu.Unlock()
	defer close(bg.done)
	defer scheduleBackgroundCommandCleanup(bg)
	if bg.cancel != nil {
		bg.cancel()
		bg.cancel = nil
	}
	if bg.terminal != nil {
		_ = bg.terminal.Close()
		bg.terminal = nil
		bg.stdin = nil
	}

	bg.running = false
	if err == nil {
		exitCode := 0
		bg.exitCode = &exitCode
		return
	}

	if exitErr, ok := err.(*exec.ExitError); ok {
		exitCode := exitErr.ExitCode()
		bg.exitCode = &exitCode
		bg.errText = err.Error()
		return
	}

	bg.errText = err.Error()
}

func streamBackgroundOutput(ctx context.Context, buffer *boundedOutput, terminal *os.File) {
	chunk := make([]byte, 4096)
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		_ = terminal.SetReadDeadline(time.Now().Add(250 * time.Millisecond))
		readLen, err := terminal.Read(chunk)
		if readLen > 0 {
			_, _ = buffer.Write(chunk[:readLen])
		}
		if err == nil {
			continue
		}
		if timeoutErr, ok := err.(interface{ Timeout() bool }); ok && timeoutErr.Timeout() {
			continue
		}
		if errors.Is(err, io.EOF) || errors.Is(err, syscall.EIO) {
			return
		}
		_, _ = buffer.Write([]byte(fmt.Sprintf("\n[Background PTY stream closed: %v]\n", err)))
		return
	}
}

func shutdownBackgroundCommands() {
	backgroundCommandsMu.RLock()
	commands := make([]*backgroundCommand, 0, len(backgroundCommands))
	for _, bg := range backgroundCommands {
		commands = append(commands, bg)
	}
	backgroundCommandsMu.RUnlock()

	for _, bg := range commands {
		bg.shutdown()
	}
}

// ShutdownBackgroundCommandsForSession terminates any still-running background
// commands so their PTY readers do not outlive engine shutdown.
func ShutdownBackgroundCommandsForSession() {
	shutdownBackgroundCommands()
}

func (bg *backgroundCommand) shutdown() {
	bg.mu.Lock()
	if bg.cancel != nil {
		bg.cancel()
		bg.cancel = nil
	}
	terminal := bg.terminal
	bg.terminal = nil
	bg.stdin = nil
	cmd := bg.cmd
	running := bg.running
	bg.mu.Unlock()

	if terminal != nil {
		_ = terminal.Close()
	}
	if running && cmd != nil && cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
}

func scheduleBackgroundCommandCleanup(bg *backgroundCommand) {
	time.AfterFunc(backgroundCommandRetention, func() {
		backgroundCommandsMu.Lock()
		defer backgroundCommandsMu.Unlock()

		current, ok := backgroundCommands[bg.id]
		if !ok || current != bg {
			return
		}
		current.mu.Lock()
		defer current.mu.Unlock()
		if current.running {
			return
		}
		delete(backgroundCommands, bg.id)
	})
}

func getBackgroundCommand(commandID string) (*backgroundCommand, error) {
	backgroundCommandsMu.RLock()
	defer backgroundCommandsMu.RUnlock()

	bg, ok := backgroundCommands[commandID]
	if !ok {
		return nil, fmt.Errorf("command %q not found", commandID)
	}
	return bg, nil
}

func (bg *backgroundCommand) sendInput(input string, wait time.Duration) (backgroundCommandResult, error) {
	bg.consumeMu.Lock()
	defer bg.consumeMu.Unlock()

	bg.mu.Lock()
	if !bg.running {
		bg.mu.Unlock()
		return backgroundCommandResult{}, fmt.Errorf("command %q is not running", bg.id)
	}
	_, err := io.WriteString(bg.stdin, input)
	bg.mu.Unlock()
	if err != nil {
		return backgroundCommandResult{}, fmt.Errorf("write command input: %w", err)
	}

	if wait > 0 {
		timer := time.NewTimer(wait)
		defer timer.Stop()
		select {
		case <-bg.done:
		case <-timer.C:
		}
	}

	return bg.snapshotDelta(), nil
}

func (bg *backgroundCommand) status(wait time.Duration) backgroundCommandResult {
	bg.consumeMu.Lock()
	defer bg.consumeMu.Unlock()

	if wait > 0 {
		timer := time.NewTimer(wait)
		defer timer.Stop()
		select {
		case <-bg.done:
		case <-timer.C:
		}
	}
	return bg.snapshotDelta()
}

func (bg *backgroundCommand) snapshotDelta() backgroundCommandResult {
	bg.mu.Lock()
	running := bg.running
	errText := bg.errText
	var exitCode *int
	if bg.exitCode != nil {
		copied := *bg.exitCode
		exitCode = &copied
	}
	bg.mu.Unlock()

	return backgroundCommandResult{
		CommandID: bg.id,
		Running:   running,
		Output:    bg.output.ReadDelta(),
		Error:     errText,
		ExitCode:  exitCode,
	}
}

func renderBackgroundCommandResult(result backgroundCommandResult) (string, error) {
	encoded, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return "", err
	}
	return string(encoded), nil
}

func (b *boundedOutput) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.data = append(b.data, p...)
	if len(b.data) > backgroundCommandMaxOutputBytes {
		trim := len(b.data) - backgroundCommandMaxOutputBytes
		if b.readOffset < trim {
			b.droppedUnreadLen += trim - b.readOffset
		}
		b.data = append([]byte(nil), b.data[trim:]...)
		if b.readOffset > trim {
			b.readOffset -= trim
		} else {
			b.readOffset = 0
		}
	}
	return len(p), nil
}

func (b *boundedOutput) ReadDelta() string {
	b.mu.Lock()
	defer b.mu.Unlock()

	hadDroppedOutput := b.droppedUnreadLen > 0
	droppedUnreadLen := b.droppedUnreadLen
	b.droppedUnreadLen = 0

	if b.readOffset >= len(b.data) {
		if hadDroppedOutput {
			return fmt.Sprintf("[Older buffered output was dropped before it could be read (%d bytes)]", droppedUnreadLen)
		}
		return ""
	}
	delta := bytes.TrimSpace(b.data[b.readOffset:])
	b.readOffset = len(b.data)
	if hadDroppedOutput {
		if len(delta) == 0 {
			return fmt.Sprintf("[Older buffered output was dropped before it could be read (%d bytes)]", droppedUnreadLen)
		}
		return fmt.Sprintf("[Older buffered output was dropped before it could be read (%d bytes)]\n%s", droppedUnreadLen, delta)
	}
	return string(delta)
}
