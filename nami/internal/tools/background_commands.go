package tools

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
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
	readers                   sync.WaitGroup // output readers, done once they reach the end of their streams
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
	backgroundCounter    atomic.Uint64
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
	id := fmt.Sprintf("cmd_%d", backgroundCounter.Add(1))
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

	bg.readers.Go(func() { streamBackgroundOutput(streamCtx, bg, terminal) })
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

	for _, reader := range []*os.File{stdout, stderr} {
		bg.readers.Go(func() {
			streamBackgroundOutput(streamCtx, bg, reader)
			_ = reader.Close()
		})
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
	bg.awaitOutputDrain(commandWaitDelay)

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

// awaitOutputDrain gives the output readers up to timeout to reach the end of
// their streams. The process has exited, but what it wrote last can still be
// buffered in the terminal or pipe, and cancelling the readers or closing the
// terminal at this point threw that tail away. The wait is bounded because a
// process the command left running can hold the streams open indefinitely.
func (bg *backgroundCommand) awaitOutputDrain(timeout time.Duration) {
	drained := make(chan struct{})
	go func() {
		bg.readers.Wait()
		close(drained)
	}()
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-drained:
	case <-timer.C:
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

func streamBackgroundOutput(ctx context.Context, bg *backgroundCommand, reader *os.File) {
	chunk := make([]byte, 4096)
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		_ = reader.SetReadDeadline(time.Now().Add(250 * time.Millisecond))
		readLen, err := reader.Read(chunk)
		if readLen > 0 {
			_, _ = bg.output.Write(chunk[:readLen])
			bg.markUpdated(time.Now())
		}
		if err == nil {
			continue
		}
		if timeoutErr, ok := errors.AsType[timeoutError](err); ok && timeoutErr.Timeout() {
			continue
		}
		// EOF and EIO are how a pipe and a terminal report that every writer
		// is gone; ErrClosed means the stream was torn down on purpose.
		if errors.Is(err, io.EOF) || errors.Is(err, syscall.EIO) || errors.Is(err, os.ErrClosed) {
			return
		}
		_, _ = bg.output.Write(fmt.Appendf(nil, "\n[Background PTY stream closed: %v]\n", err))
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
	// Kill everything the command started, not just the shell: closing the
	// terminal hangs up its processes, but not those that ignore SIGHUP. On
	// Unix, pty.Start made the shell a session leader, so its process group
	// holds the whole tree.
	if running && cmd != nil {
		_ = killProcessTree(cmd)
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
