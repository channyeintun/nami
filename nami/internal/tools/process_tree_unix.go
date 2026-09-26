//go:build !windows

package tools

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
)

// startInOwnProcessGroup makes cmd the leader of a new process group, so
// killProcessTree reaches every process the command starts rather than only
// the shell, and signals aimed at the command's group cannot reach Nami.
func startInOwnProcessGroup(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
}

// killProcessTree kills the process group led by cmd's process. The command
// must lead its own group, either through startInOwnProcessGroup or by
// starting a new session as pty.Start does.
func killProcessTree(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	if errors.Is(err, syscall.ESRCH) {
		return os.ErrProcessDone
	}
	return err
}
