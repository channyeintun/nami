// Package bashsecurity holds bash command security rules shared between
// internal/tools and internal/permissions without creating an import cycle.
package bashsecurity

import (
	"regexp"
	"strings"
)

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

var readOnlyPrograms = map[string]struct{}{
	"cat":   {},
	"df":    {},
	"du":    {},
	"echo":  {},
	"file":  {},
	"find":  {},
	"grep":  {},
	"head":  {},
	"ls":    {},
	"pwd":   {},
	"rg":    {},
	"stat":  {},
	"tail":  {},
	"type":  {},
	"wc":    {},
	"which": {},
}

var readOnlyGitSubcommands = map[string]struct{}{
	"blame":     {},
	"branch":    {},
	"diff":      {},
	"log":       {},
	"rev-parse": {},
	"show":      {},
	"status":    {},
	"tag":       {},
}

// listingGitSubcommands only inspect when they are passed nothing but listing
// flags. A bare positional argument creates a branch or tag, and the delete,
// move, copy and force flags rewrite refs, so anything outside
// readOnlyGitListFlags has to fall through to a permission prompt.
var listingGitSubcommands = map[string]struct{}{
	"branch": {},
	"tag":    {},
}

var readOnlyGitListFlags = map[string]struct{}{
	"-a":         {},
	"--all":      {},
	"-l":         {},
	"--list":     {},
	"-v":         {},
	"-vv":        {},
	"--verbose":  {},
	"--color":    {},
	"--no-color": {},
}

// findWritingPredicates delete files or run other programs. find is otherwise
// an inspection tool, and none of these contain a character the segment
// splitter rejects, so they have to be named explicitly.
var findWritingPredicates = map[string]struct{}{
	"-delete":  {},
	"-exec":    {},
	"-execdir": {},
	"-ok":      {},
	"-okdir":   {},
	"-fprint":  {},
	"-fprint0": {},
	"-fprintf": {},
	"-fls":     {},
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

func IsReadOnlyBashCommand(command string) bool {
	segments, ok := splitCommandSegments(command)
	if !ok || len(segments) == 0 {
		return false
	}
	for _, segment := range segments {
		if !isReadOnlySegment(segment) {
			return false
		}
	}
	return true
}

func splitCommandSegments(command string) ([]string, bool) {
	command = strings.TrimSpace(command)
	if command == "" {
		return nil, false
	}

	segments := make([]string, 0, 4)
	var current strings.Builder
	inSingle := false
	inDouble := false
	escaped := false

	flush := func() {
		segment := strings.TrimSpace(current.String())
		if segment != "" {
			segments = append(segments, segment)
		}
		current.Reset()
	}

	for index := 0; index < len(command); index++ {
		char := command[index]

		if escaped {
			current.WriteByte(char)
			escaped = false
			continue
		}

		switch char {
		case '\\':
			escaped = true
			current.WriteByte(char)
			continue
		case '\'':
			if !inDouble {
				inSingle = !inSingle
			}
			current.WriteByte(char)
			continue
		case '"':
			if !inSingle {
				inDouble = !inDouble
			}
			current.WriteByte(char)
			continue
		}

		if inSingle || inDouble {
			current.WriteByte(char)
			continue
		}

		switch char {
		case ';', '\n':
			flush()
			continue
		case '&':
			if index+1 < len(command) && command[index+1] == '&' {
				flush()
				index++
				continue
			}
			return nil, false
		case '|':
			flush()
			if index+1 < len(command) && command[index+1] == '|' {
				index++
			}
			continue
		case '>', '<', '(', ')', '{', '}', '`':
			return nil, false
		case '$':
			if index+1 < len(command) && command[index+1] == '(' {
				return nil, false
			}
		}

		current.WriteByte(char)
	}

	if escaped || inSingle || inDouble {
		return nil, false
	}
	flush()
	return segments, len(segments) > 0
}

func isReadOnlySegment(segment string) bool {
	words := shellWords(segment)
	if len(words) == 0 {
		return false
	}

	wordIndex := 0
	for wordIndex < len(words) && isShellEnvAssignment(words[wordIndex]) {
		wordIndex++
	}
	if wordIndex >= len(words) {
		return false
	}

	program := words[wordIndex]
	arguments := words[wordIndex+1:]

	if program == "git" {
		if len(arguments) == 0 {
			return false
		}
		subcommand := arguments[0]
		if _, ok := readOnlyGitSubcommands[subcommand]; !ok {
			return false
		}
		if _, listing := listingGitSubcommands[subcommand]; listing {
			return isGitListingOnly(arguments[1:])
		}
		return true
	}

	if _, ok := readOnlyPrograms[program]; !ok {
		return false
	}
	if program == "find" {
		for _, argument := range arguments {
			if _, writes := findWritingPredicates[argument]; writes {
				return false
			}
		}
	}
	return true
}

// isGitListingOnly reports whether the arguments to git branch or git tag only
// list refs. A positional argument creates one, so nothing outside the listing
// flag set may appear.
func isGitListingOnly(arguments []string) bool {
	for _, argument := range arguments {
		if _, ok := readOnlyGitListFlags[argument]; !ok {
			return false
		}
	}
	return true
}

func shellWords(command string) []string {
	words := make([]string, 0, 8)
	var current strings.Builder
	inSingle := false
	inDouble := false
	escaped := false

	flush := func() {
		if current.Len() == 0 {
			return
		}
		words = append(words, current.String())
		current.Reset()
	}

	for index := 0; index < len(command); index++ {
		char := command[index]
		if escaped {
			current.WriteByte(char)
			escaped = false
			continue
		}
		switch char {
		case '\\':
			escaped = true
		case '\'':
			if inDouble {
				current.WriteByte(char)
			} else {
				inSingle = !inSingle
			}
		case '"':
			if inSingle {
				current.WriteByte(char)
			} else {
				inDouble = !inDouble
			}
		case ' ', '\t', '\n':
			if inSingle || inDouble {
				current.WriteByte(char)
			} else {
				flush()
			}
		default:
			current.WriteByte(char)
		}
	}
	flush()
	return words
}

func isShellEnvAssignment(word string) bool {
	if word == "" {
		return false
	}
	equalsIndex := strings.IndexByte(word, '=')
	if equalsIndex <= 0 {
		return false
	}
	for _, char := range word[:equalsIndex] {
		if !(char == '_' || char >= 'A' && char <= 'Z' || char >= 'a' && char <= 'z' || char >= '0' && char <= '9') {
			return false
		}
	}
	return true
}
