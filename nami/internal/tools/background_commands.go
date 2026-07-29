package tools

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/creack/pty"
)

const backgroundCommandRetention = 5 * time.Minute

// timeoutError matches the read-deadline errors os and net return, which mean
// "nothing to read yet" rather than "the stream ended".
type timeoutError interface {
	error
	Timeout() bool
}

type backgroundCommand struct {
	mu                        sync.Mutex
	consumeMu                 sync.Mutex
	id                        string
	command                   string
	cwd                       string
	cmd                       *exec.Cmd
	stdin                     io.WriteCloser
	terminal                  *os.File
	cancel                    context.CancelFunc
	output                    *boundedOutput
	running                   bool
	exitCode                  *int
	errText                   string
	suppressAsyncNotification bool
	done                      chan struct{}
	startedAt                 time.Time
	updatedAt                 time.Time
}

type BackgroundCommandResult struct {
	CommandID string    `json:"CommandId"`
	Command   string    `json:"Command,omitempty"`
	Cwd       string    `json:"Cwd,omitempty"`
	Running   bool      `json:"Running"`
	StartedAt time.Time `json:"StartedAt"`
	UpdatedAt time.Time `json:"UpdatedAt"`
	Output    string    `json:"Output,omitempty"`
	Error     string    `json:"Error,omitempty"`
	ExitCode  *int      `json:"ExitCode,omitempty"`
}

type BackgroundCommandDetail struct {
	CommandID       string
	Command         string
	Cwd             string
	Status          string
	Running         bool
	StartedAt       time.Time
	UpdatedAt       time.Time
	Output          string
	HasUnreadOutput bool
	UnreadBytes     int
	ExitCode        *int
	Error           string
}

// BackgroundCommandUpdate is emitted when a retained background command changes
// state asynchronously outside the active tool turn.
type BackgroundCommandUpdate struct {
	CommandID       string
	Command         string
	Cwd             string
	Status          string
	Running         bool
	StartedAt       time.Time
	UpdatedAt       time.Time
	OutputPreview   string
	HasUnreadOutput bool
	UnreadBytes     int
	ExitCode        *int
	Error           string
}

var (
	backgroundCommands   = make(map[string]*backgroundCommand)
	backgroundCommandsMu sync.RWMutex
	backgroundCounter    uint64
	backgroundNotifierMu sync.RWMutex
	backgroundNotifier   func(BackgroundCommandUpdate)
)

// SetBackgroundCommandNotifier configures a process-local callback for
// asynchronous background command state updates.
func SetBackgroundCommandNotifier(fn func(BackgroundCommandUpdate)) {
	backgroundNotifierMu.Lock()
	defer backgroundNotifierMu.Unlock()
	backgroundNotifier = fn
}

func emitBackgroundCommandUpdate(update BackgroundCommandUpdate) {
	backgroundNotifierMu.RLock()
	fn := backgroundNotifier
	backgroundNotifierMu.RUnlock()
	if fn != nil {
		fn(update)
	}
}

func listBackgroundCommands(includeCompleted bool) []backgroundCommandSummary {
	backgroundCommandsMu.RLock()
	commands := make([]*backgroundCommand, 0, len(backgroundCommands))
	for _, bg := range backgroundCommands {
		commands = append(commands, bg)
	}
	backgroundCommandsMu.RUnlock()

	summaries := make([]backgroundCommandSummary, 0, len(commands))
	for _, bg := range commands {
		summary := bg.summary()
		if !includeCompleted && !summary.Running {
			continue
		}
		summaries = append(summaries, summary)
	}
	return summaries
}

func startBackgroundShellCommand(command, cwd string) (*backgroundCommand, error) {
	id := fmt.Sprintf("cmd_%d", atomic.AddUint64(&backgroundCounter, 1))
	cmd, err := shellCommand(command)
	if err != nil {
		return nil, err
	}
	cmd.Dir = cwd
	streamCtx, cancel := context.WithCancel(context.Background())
	if runtime.GOOS == "windows" {
		return startBackgroundPipeCommand(streamCtx, cancel, id, command, cwd, cmd)
	}
	return startBackgroundPTYCommand(streamCtx, cancel, id, command, cwd, cmd)
}

func startBackgroundPTYCommand(streamCtx context.Context, cancel context.CancelFunc, id, command, cwd string, cmd *exec.Cmd) (*backgroundCommand, error) {
	terminal, err := pty.Start(cmd)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("start background command in pty: %w", err)
	}

	bg := &backgroundCommand{
		id:        id,
		command:   command,
		cwd:       cwd,
		cmd:       cmd,
		stdin:     terminal,
		terminal:  terminal,
		cancel:    cancel,
		output:    &boundedOutput{},
		running:   true,
		done:      make(chan struct{}),
		startedAt: time.Now(),
		updatedAt: time.Now(),
	}

	backgroundCommandsMu.Lock()
	backgroundCommands[id] = bg
	backgroundCommandsMu.Unlock()

	go streamBackgroundOutput(streamCtx, bg, bg.output, terminal)
	go waitForBackgroundCommand(bg)

	return bg, nil
}

func startBackgroundPipeCommand(streamCtx context.Context, cancel context.CancelFunc, id, command, cwd string, cmd *exec.Cmd) (*backgroundCommand, error) {
	stdin, err := cmd.StdinPipe()
	if err != nil {
		cancel()
		return nil, fmt.Errorf("open background command stdin: %w", err)
	}
	// Output pipes are created here rather than with cmd.StdoutPipe so that
	// cmd.Wait does not close them out from under the readers, which would drop
	// whatever the command wrote just before exiting.
	stdout, stdoutWriter, err := os.Pipe()
	if err != nil {
		cancel()
		_ = stdin.Close()
		return nil, fmt.Errorf("open background command stdout: %w", err)
	}
	stderr, stderrWriter, err := os.Pipe()
	if err != nil {
		cancel()
		_ = stdin.Close()
		closeAll(stdout, stdoutWriter)
		return nil, fmt.Errorf("open background command stderr: %w", err)
	}
	cmd.Stdout = stdoutWriter
	cmd.Stderr = stderrWriter

	if err := cmd.Start(); err != nil {
		cancel()
		_ = stdin.Close()
		closeAll(stdout, stdoutWriter, stderr, stderrWriter)
		return nil, fmt.Errorf("start background command: %w", err)
	}
	// The child holds its own descriptors now; the parent's copies of the write
	// ends must go or the readers never see EOF.
	closeAll(stdoutWriter, stderrWriter)

	bg := &backgroundCommand{
		id:        id,
		command:   command,
		cwd:       cwd,
		cmd:       cmd,
		stdin:     stdin,
		cancel:    cancel,
		output:    &boundedOutput{},
		running:   true,
		done:      make(chan struct{}),
		startedAt: time.Now(),
		updatedAt: time.Now(),
	}

	backgroundCommandsMu.Lock()
	backgroundCommands[id] = bg
	backgroundCommandsMu.Unlock()

	for _, reader := range []io.ReadCloser{stdout, stderr} {
		go streamBackgroundOutput(streamCtx, bg, bg.output, reader)
	}
	go waitForBackgroundCommand(bg)

	return bg, nil
}

func closeAll(files ...*os.File) {
	for _, file := range files {
		_ = file.Close()
	}
}

func waitForBackgroundCommand(bg *backgroundCommand) {
	err := bg.cmd.Wait()

	bg.mu.Lock()
	if bg.cancel != nil {
		bg.cancel()
		bg.cancel = nil
	}
	if bg.terminal != nil {
		_ = bg.terminal.Close()
		bg.terminal = nil
		bg.stdin = nil
	} else if bg.stdin != nil {
		_ = bg.stdin.Close()
		bg.stdin = nil
	}

	bg.running = false
	bg.updatedAt = time.Now()
	bg.recordExitLocked(err)
	notify := !bg.suppressAsyncNotification
	bg.mu.Unlock()

	close(bg.done)
	scheduleBackgroundCommandCleanup(bg)
	if notify {
		emitBackgroundCommandUpdate(bg.asyncUpdate())
	}
}

// recordExitLocked stores the outcome of cmd.Wait. A non-zero exit is normal
// for a shell command, so it keeps the code alongside the message; anything
// else (a signal, a lost process) leaves the exit code unset.
func (bg *backgroundCommand) recordExitLocked(err error) {
	if err == nil {
		exitCode := 0
		bg.exitCode = &exitCode
		return
	}
	bg.errText = err.Error()
	if exitErr, ok := errors.AsType[*exec.ExitError](err); ok {
		exitCode := exitErr.ExitCode()
		bg.exitCode = &exitCode
	}
}

func streamBackgroundOutput(ctx context.Context, bg *backgroundCommand, buffer *boundedOutput, reader io.ReadCloser) {
	chunk := make([]byte, 4096)
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		if deadlineReader, ok := reader.(interface{ SetReadDeadline(time.Time) error }); ok {
			_ = deadlineReader.SetReadDeadline(time.Now().Add(250 * time.Millisecond))
		}
		readLen, err := reader.Read(chunk)
		if readLen > 0 {
			_, _ = buffer.Write(chunk[:readLen])
			bg.markUpdated(time.Now())
		}
		if err == nil {
			continue
		}
		if timeoutErr, ok := errors.AsType[timeoutError](err); ok && timeoutErr.Timeout() {
			continue
		}
		if errors.Is(err, io.EOF) || errors.Is(err, syscall.EIO) {
			return
		}
		_, _ = buffer.Write(fmt.Appendf(nil, "\n[Background PTY stream closed: %v]\n", err))
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
	bg.suppressAsyncNotification = true
	if bg.cancel != nil {
		bg.cancel()
		bg.cancel = nil
	}
	terminal := bg.terminal
	bg.terminal = nil
	stdin := bg.stdin
	bg.stdin = nil
	cmd := bg.cmd
	running := bg.running
	bg.mu.Unlock()

	if terminal != nil {
		_ = terminal.Close()
	} else if stdin != nil {
		_ = stdin.Close()
	}
	if running && cmd != nil && cmd.Process != nil {
		_ = terminateBackgroundProcessTree(cmd)
	}
}

func terminateBackgroundProcessTree(cmd *exec.Cmd) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	if runtime.GOOS == "windows" {
		return exec.Command("taskkill", "/f", "/t", "/pid", strconv.Itoa(cmd.Process.Pid)).Run()
	}
	return cmd.Process.Kill()
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
