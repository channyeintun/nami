package permissions

import "regexp"

// ZSH dangerous commands — always reject these.
var DangerousZshCommands = regexp.MustCompile(
	`\b(zmodload|emulate|sysopen|sysread|syswrite|zpty|ztcp|zsocket|zf_rm|zf_mv|zf_chmod|zf_mkdir|zf_chown|mapfile)\b`,
)

// Command substitution patterns to reject.
var DangerousSubstitution = regexp.MustCompile(
	`\$\(|\$\{[^}]*\}|<\(|>\(|=[a-zA-Z]`,
)

// IFS injection.
var IFSInjection = regexp.MustCompile(`\bIFS=`)

// DestructiveCommandPatterns are UI warnings (not blocks).
var DestructiveCommandPatterns = []struct {
	Pattern     *regexp.Regexp
	Description string
}{
	{regexp.MustCompile(`git\s+reset\s+--hard`), "git reset --hard"},
	{regexp.MustCompile(`git\s+push\s+.*--force`), "git push --force"},
	{regexp.MustCompile(`git\s+push\s+-f\b`), "git push -f"},
	{regexp.MustCompile(`git\s+clean\s+-f`), "git clean -f"},
	{regexp.MustCompile(`git\s+checkout\s+\.\s*$`), "git checkout ."},
	{regexp.MustCompile(`git\s+commit\s+.*--amend`), "git commit --amend"},
	{regexp.MustCompile(`--no-verify`), "--no-verify"},
	{regexp.MustCompile(`\brm\s+-rf\b`), "rm -rf"},
	{regexp.MustCompile(`\brm\s+-f\b`), "rm -f"},
	{regexp.MustCompile(`(?i)\bDROP\s+TABLE\b`), "DROP TABLE"},
	{regexp.MustCompile(`(?i)\bTRUNCATE\b`), "TRUNCATE"},
	{regexp.MustCompile(`(?i)\bDELETE\s+FROM\b`), "DELETE FROM"},
	{regexp.MustCompile(`\bkubectl\s+delete\b`), "kubectl delete"},
	{regexp.MustCompile(`\bterraform\s+destroy\b`), "terraform destroy"},
}

// ReadOnlyBashCommands are safe for concurrent execution.
var ReadOnlyBashCommands = regexp.MustCompile(
	`^\s*(git\s+(diff|status|log|show|branch|tag)|ls|cat|find|rg|grep|wc|head|tail|echo|pwd|which|type|file|stat|du|df)\b`,
)

// ValidateBashSecurity checks a command for security violations.
// Returns an error message if blocked, empty string if safe.
func ValidateBashSecurity(command string) string {
	if DangerousZshCommands.MatchString(command) {
		return "blocked: dangerous ZSH command detected"
	}
	if DangerousSubstitution.MatchString(command) {
		return "blocked: dangerous command substitution pattern"
	}
	if IFSInjection.MatchString(command) {
		return "blocked: IFS injection detected"
	}
	return ""
}

// CheckDestructive returns a warning if the command is destructive.
func CheckDestructive(command string) string {
	for _, p := range DestructiveCommandPatterns {
		if p.Pattern.MatchString(command) {
			return "warning: destructive command — " + p.Description
		}
	}
	return ""
}

// IsBashReadOnly returns true if the command is safe for concurrent execution.
func IsBashReadOnly(command string) bool {
	return ReadOnlyBashCommands.MatchString(command)
}
