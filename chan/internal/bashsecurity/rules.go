// Package bashsecurity holds bash command security rules shared between
// internal/tools and internal/permissions without creating an import cycle.
package bashsecurity

import "regexp"

// DangerousZshCommands matches ZSH-specific builtins that should always be blocked.
var DangerousZshCommands = regexp.MustCompile(
	`\b(zmodload|emulate|sysopen|sysread|syswrite|zpty|ztcp|zsocket|zf_rm|zf_mv|zf_chmod|zf_mkdir|zf_chown|mapfile)\b`,
)

// DangerousSubstitution matches process substitution operators.
var DangerousSubstitution = regexp.MustCompile(`<\(|>\(`)

// IFSInjection matches IFS variable assignments that can corrupt word splitting.
var IFSInjection = regexp.MustCompile(`\bIFS=`)

// EvalExec matches eval/exec/source builtins and backtick substitution that can
// execute arbitrary code bypassing the normal argument structure.
var EvalExec = regexp.MustCompile("`|\\beval\\b|\\bexec\\b|\\bsource\\b|\\b\\.\\s")

// DestructivePattern pairs a pattern with a human-readable description.
type DestructivePattern struct {
	Pattern     *regexp.Regexp
	Description string
}

// DestructivePatterns are UI warnings for potentially destructive operations.
var DestructivePatterns = []DestructivePattern{
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

// ValidateBashSecurity returns a non-empty error description if the command is
// blocked for security reasons, or empty string if it is safe to execute.
func ValidateBashSecurity(command string) string {
	if DangerousZshCommands.MatchString(command) {
		return "blocked: dangerous ZSH command detected"
	}
	if DangerousSubstitution.MatchString(command) {
		return "blocked: dangerous process substitution pattern"
	}
	if IFSInjection.MatchString(command) {
		return "blocked: IFS injection detected"
	}
	if EvalExec.MatchString(command) {
		return "blocked: eval/exec/source or backtick substitution detected"
	}
	return ""
}

// CheckDestructive returns a warning string if the command matches a
// destructive pattern, or empty string otherwise.
func CheckDestructive(command string) string {
	for _, p := range DestructivePatterns {
		if p.Pattern.MatchString(command) {
			return "warning: destructive command — " + p.Description
		}
	}
	return ""
}
