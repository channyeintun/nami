package bashsecurity

import "testing"

func TestValidateBashSecurityBlocksInjectionVectors(t *testing.T) {
	blocked := map[string]string{
		"zsh builtin":          "zmodload zsh/system",
		"process substitution": "diff <(ls) <(ls)",
		"IFS injection":        "IFS=. read a b c",
		"eval":                 "eval rm -rf /",
		"exec":                 "exec /bin/sh",
		"source":               "source ~/.bashrc",
		"backtick":             "echo `whoami`",
	}
	for name, command := range blocked {
		if ValidateBashSecurity(command) == "" {
			t.Errorf("%s: %q was allowed, want blocked", name, command)
		}
	}
}

func TestValidateBashSecurityAllowsOrdinaryCommands(t *testing.T) {
	for _, command := range []string{
		"ls -la",
		"go test ./...",
		"git status",
		"grep -rn TODO internal/",
	} {
		if reason := ValidateBashSecurity(command); reason != "" {
			t.Errorf("%q was blocked: %s", command, reason)
		}
	}
}

func TestCheckDestructiveFlagsKnownPatterns(t *testing.T) {
	for _, command := range []string{
		"git reset --hard HEAD~1",
		"git push origin main --force",
		"git push -f",
		"git clean -fd",
		"git commit --amend --no-edit",
		"rm -rf build",
		"kubectl delete pod x",
		"terraform destroy",
		"DROP TABLE users",
		"delete from users where 1=1",
	} {
		if CheckDestructive(command) == "" {
			t.Errorf("%q was not flagged as destructive", command)
		}
	}
}

func TestCheckDestructiveIgnoresSafeCommands(t *testing.T) {
	for _, command := range []string{"git status", "ls -la", "go build ./..."} {
		if warning := CheckDestructive(command); warning != "" {
			t.Errorf("%q flagged as destructive: %s", command, warning)
		}
	}
}

func TestIsReadOnlyBashCommandAcceptsInspectionCommands(t *testing.T) {
	for _, command := range []string{
		"ls -la",
		"cat go.mod",
		"pwd",
		"grep -rn TODO internal/",
		"git status",
		"git log --oneline -5",
		"git diff HEAD~1",
		"ls -la | grep go",         // pipelines of read-only programs
		"pwd && ls",                // conditional chains
		"pwd; ls",                  // sequences
		"GOFLAGS=-mod=mod ls -la",  // leading env assignments are skipped
		`grep -rn "a;b" internal/`, // separators inside quotes are literal
	} {
		if !IsReadOnlyBashCommand(command) {
			t.Errorf("%q should be read-only", command)
		}
	}
}

func TestIsReadOnlyBashCommandRejectsMutation(t *testing.T) {
	for _, command := range []string{
		"",
		"rm -rf build",
		"go build ./...",
		"ls > out.txt",  // redirection
		"ls < in.txt",   // redirection
		"cat $(ls)",     // command substitution
		"ls &",          // backgrounding
		"echo `ls`",     // backtick substitution
		"ls | rm -rf x", // one bad segment poisons the pipeline
		"git status; rm -rf build",
		"git push",         // a write git subcommand
		"git",              // git with no subcommand
		`ls "unterminated`, // unbalanced quoting
	} {
		if IsReadOnlyBashCommand(command) {
			t.Errorf("%q must not be treated as read-only", command)
		}
	}
}

// find is whitelisted as an inspection program, but several of its predicates
// write or execute. None of them contain a character the segment splitter
// rejects, so they reach the program lookup and would auto-approve.
func TestIsReadOnlyBashCommandRejectsWritingFindPredicates(t *testing.T) {
	for _, command := range []string{
		"find . -delete",
		"find . -name '*.tmp' -delete",
		"find . -exec rm {} +",
		"find . -execdir rm {} +",
		"find . -ok rm {} ;",
		"find . -okdir rm {} ;",
		"find . -fprint /tmp/out",
		"find . -fprintf /tmp/out %p",
		"find . -fls /tmp/out",
	} {
		if IsReadOnlyBashCommand(command) {
			t.Errorf("%q must not be treated as read-only", command)
		}
	}
}

func TestIsReadOnlyBashCommandAcceptsPlainFind(t *testing.T) {
	for _, command := range []string{
		"find . -name '*.go'",
		"find internal -type f -print",
	} {
		if !IsReadOnlyBashCommand(command) {
			t.Errorf("%q should be read-only", command)
		}
	}
}

// git branch and git tag are read-only only when listing. Their delete, move
// and force flags mutate the repository.
func TestIsReadOnlyBashCommandRejectsMutatingGitBranchAndTag(t *testing.T) {
	for _, command := range []string{
		"git branch -D feature",
		"git branch -d feature",
		"git branch -m old new",
		"git branch -f main HEAD~1",
		"git tag -d v1.0.0",
	} {
		if IsReadOnlyBashCommand(command) {
			t.Errorf("%q must not be treated as read-only", command)
		}
	}
}

func TestIsReadOnlyBashCommandAcceptsListingGitBranchAndTag(t *testing.T) {
	for _, command := range []string{"git branch", "git branch -a", "git tag", "git tag -l"} {
		if !IsReadOnlyBashCommand(command) {
			t.Errorf("%q should be read-only", command)
		}
	}
}

func TestIsShellEnvAssignment(t *testing.T) {
	valid := []string{"FOO=bar", "GOFLAGS=-mod=mod", "a1_B=x", "EMPTY="}
	for _, word := range valid {
		if !isShellEnvAssignment(word) {
			t.Errorf("isShellEnvAssignment(%q) = false, want true", word)
		}
	}
	invalid := []string{"", "=bar", "no-equals", "FOO-BAR=x", "./x=y"}
	for _, word := range invalid {
		if isShellEnvAssignment(word) {
			t.Errorf("isShellEnvAssignment(%q) = true, want false", word)
		}
	}
}

func TestShellWordsStripsQuoting(t *testing.T) {
	got := shellWords(`grep -rn "two words" 'single quoted' plain`)
	want := []string{"grep", "-rn", "two words", "single quoted", "plain"}
	if len(got) != len(want) {
		t.Fatalf("shellWords = %#v, want %#v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("shellWords = %#v, want %#v", got, want)
		}
	}
}
