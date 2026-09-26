//go:build windows

// Package proctree stops a command together with every process it started.
// Killing only the command's own process leaves its children running: a
// shell's pipeline, a test runner's workers, a server started in the
// background.
package proctree

import (
	"os/exec"
	"strconv"
)

// StartInOwnGroup does nothing on Windows: KillTree finds the tree through
// parent process ids instead of a process group.
func StartInOwnGroup(cmd *exec.Cmd) {}

// KillTree force-kills cmd's process and every process it started.
func KillTree(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	return exec.Command("taskkill", "/f", "/t", "/pid", strconv.Itoa(cmd.Process.Pid)).Run()
}
