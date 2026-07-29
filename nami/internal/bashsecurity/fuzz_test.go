package bashsecurity

import (
	"strings"
	"testing"
)

// programsThatWrite are binaries that can modify the machine. If the classifier
// ever calls a command read-only while one of these is the program of any
// segment, the engine would run it without asking the user.
var programsThatWrite = []string{
	"rm", "mv", "cp", "dd", "chmod", "chown", "ln", "touch", "mkdir", "rmdir",
	"truncate", "tee", "install", "sh", "bash", "zsh", "python", "node", "perl",
	"ruby", "curl", "wget", "npm", "pip", "go", "make", "docker", "kubectl",
	"systemctl", "sudo", "su", "ssh", "scp", "rsync", "kill", "pkill", "killall",
	"sed", "awk", "patch", "git-push", "apt", "brew", "gem", "cargo",
}

// FuzzIsReadOnlyBashCommand asserts the property the permission gate depends
// on: a command classified as read-only never names a mutating program and
// never carries a shell construct that could reach one.
func FuzzIsReadOnlyBashCommand(f *testing.F) {
	seeds := []string{
		"ls -la",
		"cat go.mod | grep module",
		"git status; git log",
		"pwd && ls",
		"LC_ALL=C grep -rn TODO .",
		"rm -rf /",
		"ls > out.txt",
		"cat $(ls)",
		"echo `whoami`",
		"find . -delete",
		"git branch -D main",
		`ls "unterminated`,
		"ls \\; rm -rf x",
		"LD_PRELOAD=/tmp/x.so ls",
		"ls | sh",
		"cat a\nrm b",
	}
	for _, seed := range seeds {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, command string) {
		if !IsReadOnlyBashCommand(command) {
			return
		}

		// Backticks and process substitution are rejected wherever they appear,
		// quoted or not, because the blocking rules run on the raw string.
		for _, forbidden := range []string{"`", "<(", ">("} {
			if strings.Contains(command, forbidden) {
				t.Fatalf("read-only verdict for command containing %q: %q", forbidden, command)
			}
		}

		segments, ok := splitCommandSegments(command)
		if !ok || len(segments) == 0 {
			t.Fatalf("read-only verdict for a command that does not split: %q", command)
		}
		for _, segment := range segments {
			assertSegmentIsInspectionOnly(t, command, segment)
		}
	})
}

func assertSegmentIsInspectionOnly(t *testing.T, command, segment string) {
	t.Helper()
	words := shellWords(segment)
	if len(words) == 0 {
		t.Fatalf("read-only verdict for a command with an empty segment: %q", command)
	}

	index := 0
	for index < len(words) && isShellEnvAssignment(words[index]) {
		if !isInertEnvAssignment(words[index]) {
			t.Fatalf("read-only verdict despite the env assignment %q: %q", words[index], command)
		}
		index++
	}
	if index >= len(words) {
		t.Fatalf("read-only verdict for a segment with no program: %q", command)
	}

	program := words[index]
	for _, writer := range programsThatWrite {
		if program == writer {
			t.Fatalf("read-only verdict for the mutating program %q: %q", program, command)
		}
	}
	if program == "git" {
		assertGitSubcommandIsReadOnly(t, command, words[index+1:])
	}
	if program == "find" {
		for _, argument := range words[index+1:] {
			if _, writes := findWritingPredicates[argument]; writes {
				t.Fatalf("read-only verdict for find predicate %q: %q", argument, command)
			}
		}
	}
}

func assertGitSubcommandIsReadOnly(t *testing.T, command string, arguments []string) {
	t.Helper()
	if len(arguments) == 0 {
		t.Fatalf("read-only verdict for bare git: %q", command)
	}
	if _, ok := readOnlyGitSubcommands[arguments[0]]; !ok {
		t.Fatalf("read-only verdict for git subcommand %q: %q", arguments[0], command)
	}
	if _, listing := listingGitSubcommands[arguments[0]]; listing && !isGitListingOnly(arguments[1:]) {
		t.Fatalf("read-only verdict for a mutating git %s: %q", arguments[0], command)
	}
}

// FuzzValidateBashSecurity checks that the blocking rules keep their contract:
// anything containing an eval/exec/backtick/process-substitution construct is
// reported as blocked, whatever else surrounds it.
func FuzzValidateBashSecurity(f *testing.F) {
	f.Add("ls -la")
	f.Add("eval $(cat x)")
	f.Add("diff <(ls) <(ls)")
	f.Add("IFS=. read a b c")
	f.Add("echo `id`")
	f.Add("zmodload zsh/system")

	f.Fuzz(func(t *testing.T, command string) {
		blocked := ValidateBashSecurity(command) != ""
		mustBlock := strings.Contains(command, "`") ||
			strings.Contains(command, "<(") ||
			strings.Contains(command, ">(") ||
			strings.Contains(command, "IFS=")
		if mustBlock && !blocked {
			t.Fatalf("command with an injection construct was allowed: %q", command)
		}
		// A blocked command is never simultaneously auto-approvable.
		if blocked && IsReadOnlyBashCommand(command) {
			t.Fatalf("command is both blocked and read-only: %q", command)
		}
	})
}
