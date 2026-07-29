package bashsecurity

import "testing"

// An assignment in front of a read-only program can still hand the shell a
// program to run: the dynamic loader, git, and less all take executables from
// the environment. Auto-approving those would run arbitrary code without a
// prompt, so only inert variables may precede a read-only command.
func TestIsReadOnlyBashCommandRejectsCodeExecutingEnvAssignments(t *testing.T) {
	for _, command := range []string{
		"LD_PRELOAD=/tmp/evil.so cat /etc/hosts",
		"LD_LIBRARY_PATH=/tmp ls",
		"LD_AUDIT=/tmp/evil.so ls",
		"DYLD_INSERT_LIBRARIES=/tmp/evil.dylib ls -la",
		"DYLD_LIBRARY_PATH=/tmp cat go.mod",
		"GIT_EXTERNAL_DIFF=/tmp/evil git diff",
		"GIT_SSH_COMMAND=/tmp/evil git log",
		"GIT_PAGER=/tmp/evil git log",
		"PAGER=/tmp/evil git log",
		"LESSOPEN=|/tmp/evil.sh git log",
		"PATH=/tmp/fakebin ls",
		"BASH_ENV=/tmp/evil.sh cat go.mod",
		"ENV=/tmp/evil.sh cat go.mod",
		"ZDOTDIR=/tmp cat go.mod",
		"SHELL=/tmp/evil git log",
		"PERL5OPT=-Mevil find .",
		"PYTHONSTARTUP=/tmp/evil.py cat x",
		"NODE_OPTIONS=--require=/tmp/evil.js cat x",
		"RUBYOPT=-revil cat x",
		"GOFLAGS=-mod=mod LD_PRELOAD=/tmp/evil.so ls",
	} {
		if IsReadOnlyBashCommand(command) {
			t.Errorf("%q must not be treated as read-only", command)
		}
	}
}

func TestIsReadOnlyBashCommandAllowsInertEnvAssignments(t *testing.T) {
	for _, command := range []string{
		"LC_ALL=C grep -rn TODO internal/",
		"LANG=en_US.UTF-8 ls -la",
		"TZ=UTC git log --oneline -5",
		"NO_COLOR=1 git status",
		"GOFLAGS=-mod=mod ls -la",
		"COLUMNS=200 LC_ALL=C ls",
	} {
		if !IsReadOnlyBashCommand(command) {
			t.Errorf("%q should be read-only", command)
		}
	}
}

func TestIsInertEnvAssignment(t *testing.T) {
	inert := []string{"LC_ALL=C", "TZ=", "GOFLAGS=-mod=mod"}
	for _, word := range inert {
		if !isInertEnvAssignment(word) {
			t.Errorf("isInertEnvAssignment(%q) = false, want true", word)
		}
	}
	dangerous := []string{"LD_PRELOAD=/tmp/x", "PATH=/tmp", "lc_all=C", "no-equals", ""}
	for _, word := range dangerous {
		if isInertEnvAssignment(word) {
			t.Errorf("isInertEnvAssignment(%q) = true, want false", word)
		}
	}
}
