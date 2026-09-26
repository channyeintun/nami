//go:build !unix

package agent

// instructionFileDistrust trusts every instruction file on systems without
// POSIX ownership and permission bits, which the check relies on.
func instructionFileDistrust(string) string {
	return ""
}
