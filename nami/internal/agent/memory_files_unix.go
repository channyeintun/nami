//go:build unix

package agent

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// instructionFileDistrust says why an instruction file must not be loaded,
// or returns "" when it may be. AGENTS.md is read from every directory up to
// the filesystem root, so without this any local user could put instructions
// into the system prompt of someone working under /tmp.
func instructionFileDistrust(path string) string {
	return instructionFileDistrustFor(path, os.Getuid())
}

// instructionFileDistrustFor is instructionFileDistrust for the user uid. A
// file is distrusted when it sits in a shared temporary directory, or when
// another user owns it. Permission bits alone are not held against a file:
// WSL's view of Windows drives and FAT volumes show every file and directory
// as writable by everyone, and skipping those would drop the instructions of
// every project kept there.
func instructionFileDistrustFor(path string, uid int) string {
	info, err := os.Stat(path)
	if err != nil {
		// There is no file to load; reading it fails the same way.
		return ""
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return "its owner could not be determined"
	}
	// Root is exempt: it works in other users' trees as a matter of course,
	// a container over a host checkout being the common case, and it trusts
	// their code there already.
	if uid != 0 && stat.Uid != 0 && int(stat.Uid) != uid {
		return fmt.Sprintf("owned by uid %d, not by you or root", stat.Uid)
	}
	dir := filepath.Dir(path)
	dirInfo, err := os.Stat(dir)
	if err != nil {
		return fmt.Sprintf("its directory could not be checked: %v", err)
	}
	// A sticky, world-writable directory is a shared temporary directory such
	// as /tmp: anyone can create a file there, root included when the user is
	// root, so no file in it was necessarily meant for this user.
	if dirInfo.Mode().Perm()&0o002 != 0 && dirInfo.Mode()&os.ModeSticky != 0 {
		return fmt.Sprintf("in %s, a directory every user can create files in", dir)
	}
	return ""
}
