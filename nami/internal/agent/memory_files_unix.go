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
// into the system prompt of someone working under /tmp. A file is trusted
// only when you or root own it and no other user can rewrite or replace it.
func instructionFileDistrust(path string) string {
	return instructionFileDistrustFor(path, os.Getuid())
}

// instructionFileDistrustFor is instructionFileDistrust for the user uid.
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
	if stat.Uid != 0 && int(stat.Uid) != uid {
		return fmt.Sprintf("owned by uid %d, not by you or root", stat.Uid)
	}
	if info.Mode().Perm()&0o002 != 0 {
		return "writable by every user"
	}
	dir := filepath.Dir(path)
	dirInfo, err := os.Stat(dir)
	if err != nil {
		return fmt.Sprintf("its directory could not be checked: %v", err)
	}
	// Anyone can create or swap entries in a world-writable directory, the
	// sticky bit of /tmp notwithstanding: it only stops them removing yours.
	if dirInfo.Mode().Perm()&0o002 != 0 {
		return fmt.Sprintf("in %s, which every user can write to", dir)
	}
	return ""
}
