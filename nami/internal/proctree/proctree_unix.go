//go:build !windows

// Package proctree stops a command together with every process it started.
// Killing only the command's own process leaves its children running: a
// shell's pipeline, a test runner's workers, a server started with "&".
package proctree

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
)

// StartInOwnGroup makes cmd the leader of a new process group when it
// starts, so KillTree reaches every process the command starts rather than
// only the first, and signals aimed at the command's group cannot reach the
// caller.
func StartInOwnGroup(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
}

// KillTree kills the process group led by cmd's process. The command must
// lead its own group, through StartInOwnGroup or by starting a new session as
// pty.Start does. It returns os.ErrProcessDone when the group is already gone.
func KillTree(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	if errors.Is(err, syscall.ESRCH) {
		return os.ErrProcessDone
	}
	return err
}
