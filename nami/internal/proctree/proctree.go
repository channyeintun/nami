package proctree

import "os/exec"

// KillTreeOnCancel makes cancelling cmd's context kill every process the
// command started, not just the command itself. Call it before cmd starts.
func KillTreeOnCancel(cmd *exec.Cmd) {
	StartInOwnGroup(cmd)
	cmd.Cancel = func() error { return KillTree(cmd) }
}
