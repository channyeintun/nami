//go:build windows

package tools

import (
	"os/exec"
	"strconv"
)

// startInOwnProcessGroup is a no-op on Windows: taskkill /t finds the tree
// through parent process ids instead of a process group.
func startInOwnProcessGroup(cmd *exec.Cmd) {}

// killProcessTree force-kills cmd's process and every process it started.
func killProcessTree(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	return exec.Command("taskkill", "/f", "/t", "/pid", strconv.Itoa(cmd.Process.Pid)).Run()
}
