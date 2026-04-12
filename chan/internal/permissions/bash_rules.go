package permissions

import (
	"regexp"

	"github.com/channyeintun/gocode/internal/bashsecurity"
)

// Re-export canonical vars so existing code in this package can reference them.
var (
	DangerousZshCommands  = bashsecurity.DangerousZshCommands
	DangerousSubstitution = bashsecurity.DangerousSubstitution
	IFSInjection          = bashsecurity.IFSInjection
)

// DestructiveCommandPatterns are UI warnings (not blocks).
// Exposed as the concrete struct type expected by tests/callers in this package.
var DestructiveCommandPatterns = bashsecurity.DestructivePatterns

// ReadOnlyBashCommands are safe for concurrent execution.
var ReadOnlyBashCommands = regexp.MustCompile(
	`^\s*(git\s+(diff|status|log|show|branch|tag)|ls|cat|find|rg|grep|wc|head|tail|echo|pwd|which|type|file|stat|du|df)\b`,
)

// ValidateBashSecurity checks a command for security violations.
// Returns an error message if blocked, empty string if safe.
func ValidateBashSecurity(command string) string {
	return bashsecurity.ValidateBashSecurity(command)
}

// CheckDestructive returns a warning if the command is destructive.
func CheckDestructive(command string) string {
	return bashsecurity.CheckDestructive(command)
}

// IsBashReadOnly returns true if the command is safe for concurrent execution.
func IsBashReadOnly(command string) bool {
	return ReadOnlyBashCommands.MatchString(command)
}
