package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// inWorkspace runs a test with the process working directory pointed at a fresh
// temp dir, because tool paths resolve relative to it.
func inWorkspace(t *testing.T) string {
	t.Helper()
	workspace := t.TempDir()
	original, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(workspace); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(original); err != nil {
			t.Fatalf("restore wd: %v", err)
		}
	})
	// macOS temp dirs are symlinked through /private, so resolve to the path
	// the tool layer will produce.
	resolved, err := filepath.EvalSymlinks(workspace)
	if err != nil {
		return workspace
	}
	return resolved
}

func runApplyPatch(t *testing.T, patchText string) ToolOutput {
	t.Helper()
	output, err := NewApplyPatchTool().Execute(context.Background(), ToolInput{Params: map[string]any{"input": patchText}})
	if err != nil {
		t.Fatalf("execute apply_patch: %v", err)
	}
	return output
}

func writeWorkspaceFile(t *testing.T, workspace, name, content string) string {
	t.Helper()
	path := filepath.Join(workspace, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	return path
}

func TestApplyPatchAddsAndDeletesFiles(t *testing.T) {
	workspace := inWorkspace(t)
	writeWorkspaceFile(t, workspace, "old.txt", "goodbye\n")

	output := runApplyPatch(t, strings.Join([]string{
		"*** Begin Patch",
		"*** Add File: nested/new.txt",
		"+hello",
		"+world",
		"*** Delete File: old.txt",
		"*** End Patch",
	}, "\n"))

	if output.IsError {
		t.Fatalf("output = %s", output.Output)
	}
	if !strings.Contains(output.Output, "2 files changed") {
		t.Errorf("summary = %q", output.Output)
	}
	if output.FilePath != "2 files" {
		t.Errorf("FilePath = %q", output.FilePath)
	}

	added, err := os.ReadFile(filepath.Join(workspace, "nested", "new.txt"))
	if err != nil {
		t.Fatalf("read added file: %v", err)
	}
	if string(added) != "hello\nworld" {
		t.Errorf("added content = %q", added)
	}
	if _, err := os.Stat(filepath.Join(workspace, "old.txt")); !os.IsNotExist(err) {
		t.Errorf("deleted file still exists: %v", err)
	}
}

// The patch format is line-based, so an existing file's CRLF endings have to
// survive an edit.
func TestApplyPatchPreservesCRLFLineEndings(t *testing.T) {
	workspace := inWorkspace(t)
	path := writeWorkspaceFile(t, workspace, "crlf.txt", "alpha\r\nbeta\r\ngamma\r\n")

	output := runApplyPatch(t, strings.Join([]string{
		"*** Begin Patch",
		"*** Update File: crlf.txt",
		"@@",
		" alpha",
		"-beta",
		"+beta updated",
		" gamma",
		"*** End Patch",
	}, "\n"))
	if output.IsError {
		t.Fatalf("output = %s", output.Output)
	}

	updated, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if got, want := string(updated), "alpha\r\nbeta updated\r\ngamma\r\n"; got != want {
		t.Fatalf("content = %q, want %q", got, want)
	}
}

func TestApplyPatchPreservesMissingTrailingNewline(t *testing.T) {
	workspace := inWorkspace(t)
	path := writeWorkspaceFile(t, workspace, "no-newline.txt", "alpha\nomega")

	output := runApplyPatch(t, strings.Join([]string{
		"*** Begin Patch",
		"*** Update File: no-newline.txt",
		"@@",
		"-omega",
		"+updated",
		"*** End Patch",
	}, "\n"))
	if output.IsError {
		t.Fatalf("output = %s", output.Output)
	}

	updated, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if got := string(updated); got != "alpha\nupdated" {
		t.Fatalf("content = %q, want no trailing newline added", got)
	}
}

func TestApplyPatchReportsRecoverableFailures(t *testing.T) {
	workspace := inWorkspace(t)
	writeWorkspaceFile(t, workspace, "sample.txt", "alpha\nbeta\n")

	cases := map[string]struct {
		patch string
		kind  EditFailureKind
	}{
		"no match": {strings.Join([]string{
			"*** Begin Patch",
			"*** Update File: sample.txt",
			"@@",
			"-missing line",
			"+replacement",
			"*** End Patch",
		}, "\n"), EditFailureNoMatch},
		"malformed": {"not a patch at all", EditFailureInvalidPatchFormat},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			output := runApplyPatch(t, tc.patch)
			if !output.IsError {
				t.Fatalf("output = %+v, want a recoverable failure", output)
			}
			if output.ErrorKind != string(tc.kind) {
				t.Fatalf("ErrorKind = %q, want %q", output.ErrorKind, tc.kind)
			}
			if output.ErrorHint == "" {
				t.Error("failure output carries no recovery hint")
			}
		})
	}
}

func TestApplyPatchValidateRejectsConflictingTargets(t *testing.T) {
	workspace := inWorkspace(t)
	writeWorkspaceFile(t, workspace, "exists.txt", "content\n")

	tool := NewApplyPatchTool()
	cases := map[string]string{
		"add over an existing file": strings.Join([]string{
			"*** Begin Patch", "*** Add File: exists.txt", "+x", "*** End Patch",
		}, "\n"),
		"update a missing file": strings.Join([]string{
			"*** Begin Patch", "*** Update File: missing.txt", "@@", "-a", "+b", "*** End Patch",
		}, "\n"),
		"delete a missing file": strings.Join([]string{
			"*** Begin Patch", "*** Delete File: missing.txt", "*** End Patch",
		}, "\n"),
		"no input": "",
	}
	for name, patchText := range cases {
		t.Run(name, func(t *testing.T) {
			err := tool.Validate(ToolInput{Params: map[string]any{"input": patchText}})
			if err == nil {
				t.Fatal("Validate accepted a conflicting patch")
			}
			if _, ok := ExtractEditFailure(err); !ok {
				t.Fatalf("error = %T (%v), want an *EditFailure", err, err)
			}
		})
	}
}

func TestApplyPatchAcceptsPatchAlias(t *testing.T) {
	workspace := inWorkspace(t)
	path := writeWorkspaceFile(t, workspace, "alias.txt", "one\n")

	output, err := NewApplyPatchTool().Execute(context.Background(), ToolInput{Params: map[string]any{
		"patch": strings.Join([]string{
			"*** Begin Patch",
			"*** Update File: alias.txt",
			"@@",
			"-one",
			"+two",
			"*** End Patch",
		}, "\n"),
	}})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if output.IsError {
		t.Fatalf("output = %s", output.Output)
	}

	updated, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(updated) != "two\n" {
		t.Fatalf("content = %q", updated)
	}
}

func TestExtractApplyPatchTargets(t *testing.T) {
	targets, err := ExtractApplyPatchTargets(strings.Join([]string{
		"*** Begin Patch",
		"*** Update File: a.txt",
		"@@",
		"-x",
		"+y",
		"*** Delete File: b.txt",
		"*** End Patch",
	}, "\n"))
	if err != nil {
		t.Fatalf("ExtractApplyPatchTargets: %v", err)
	}
	if len(targets) != 2 || targets[0] != "a.txt" || targets[1] != "b.txt" {
		t.Fatalf("targets = %#v", targets)
	}
	if _, err := ExtractApplyPatchTargets("nonsense"); err == nil {
		t.Fatal("ExtractApplyPatchTargets accepted a malformed patch")
	}
}
