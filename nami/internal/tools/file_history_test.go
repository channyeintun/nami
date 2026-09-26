package tools

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func takeSnapshot(t *testing.T, history *FileHistory, label string) string {
	t.Helper()
	snapshotID, err := history.MakeSnapshot(label)
	if err != nil {
		t.Fatalf("MakeSnapshot: %v", err)
	}
	return snapshotID
}

// trackedWrite does what the edit tools do: record the file, then change it.
// A nil content deletes the file.
func trackedWrite(t *testing.T, history *FileHistory, path string, content *string) {
	t.Helper()
	if err := history.TrackEdit(path); err != nil {
		t.Fatalf("TrackEdit(%s): %v", path, err)
	}
	if content == nil {
		if err := os.Remove(path); err != nil {
			t.Fatalf("remove %s: %v", path, err)
		}
		return
	}
	if err := os.WriteFile(path, []byte(*content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func text(value string) *string { return &value }

func rewindTo(t *testing.T, history *FileHistory, snapshotID string) {
	t.Helper()
	result, err := history.Rewind(snapshotID)
	if err != nil {
		t.Fatalf("Rewind(%s): %v", snapshotID, err)
	}
	if len(result.Failed) > 0 {
		t.Fatalf("Rewind(%s) failures: %v", snapshotID, result.Failed)
	}
}

// fileState returns the file's content, or nil when it does not exist.
func fileState(t *testing.T, path string) *string {
	t.Helper()
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return text(string(data))
}

func describeState(state *string) string {
	if state == nil {
		return "<missing>"
	}
	return *state
}

// A snapshot has to capture the files as they are when it is taken, whether
// the session touched them before the snapshot, only after it, or both.
func TestFileHistoryRewindRestoresSnapshotState(t *testing.T) {
	cases := []struct {
		name    string
		initial *string
		before  []*string // tracked writes before the snapshot
		after   []*string // tracked writes after the snapshot
		want    *string
	}{
		{"edited before and after", text("v0"), []*string{text("v1")}, []*string{text("v2")}, text("v1")},
		{"first edited after", text("v0"), nil, []*string{text("v1"), text("v2")}, text("v0")},
		{"created after", nil, nil, []*string{text("new")}, nil},
		{"deleted after", text("v0"), nil, []*string{nil}, text("v0")},
		{"created before, edited after", nil, []*string{text("v1")}, []*string{text("v2")}, text("v1")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			history := NewFileHistory(t.TempDir())
			path := filepath.Join(t.TempDir(), "file.txt")
			if tc.initial != nil {
				if err := os.WriteFile(path, []byte(*tc.initial), 0o644); err != nil {
					t.Fatalf("write initial: %v", err)
				}
			}
			for _, content := range tc.before {
				trackedWrite(t, history, path, content)
			}
			snapshotID := takeSnapshot(t, history, "checkpoint")
			for _, content := range tc.after {
				trackedWrite(t, history, path, content)
			}

			rewindTo(t, history, snapshotID)
			if got := fileState(t, path); describeState(got) != describeState(tc.want) {
				t.Fatalf("after rewind the file is %q, want %q", describeState(got), describeState(tc.want))
			}
		})
	}
}

func TestFileHistoryRewindsToEitherOfTwoSnapshots(t *testing.T) {
	history := NewFileHistory(t.TempDir())
	path := filepath.Join(t.TempDir(), "file.txt")
	if err := os.WriteFile(path, []byte("v0"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	first := takeSnapshot(t, history, "first")
	trackedWrite(t, history, path, text("v1"))
	second := takeSnapshot(t, history, "second")
	trackedWrite(t, history, path, text("v2"))

	rewindTo(t, history, first)
	if got := describeState(fileState(t, path)); got != "v0" {
		t.Fatalf("after rewinding to the first snapshot the file is %q, want v0", got)
	}
	rewindTo(t, history, second)
	if got := describeState(fileState(t, path)); got != "v1" {
		t.Fatalf("after rewinding to the second snapshot the file is %q, want v1", got)
	}
}

// The model drives snapshots through the tools: edit, snapshot, edit again,
// rewind. The rewind must land on the content the snapshot saw.
func TestFileHistoryToolsRewindToSnapshot(t *testing.T) {
	workspace := inWorkspace(t)
	SetGlobalFileHistory(NewFileHistory(t.TempDir()))
	t.Cleanup(func() { SetGlobalFileHistory(nil) })
	path := writeWorkspaceFile(t, workspace, "notes.txt", "alpha\n")

	edit := func(oldString, newString string) {
		t.Helper()
		output, err := runValidatedTool(t, NewFileEditTool(), map[string]any{"filePath": path, "oldString": oldString, "newString": newString})
		if err != nil || output.IsError {
			t.Fatalf("edit %q -> %q: err=%v output=%q", oldString, newString, err, output.Output)
		}
	}

	edit("alpha", "beta")
	snapshot, err := runValidatedTool(t, NewFileHistoryTool(), map[string]any{"action": "snapshot", "label": "before-gamma"})
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	snapshotID, ok := strings.CutPrefix(snapshot.Output, "Created snapshot: ")
	if !ok {
		t.Fatalf("snapshot output = %q", snapshot.Output)
	}
	edit("beta", "gamma")

	rewind, err := runValidatedTool(t, NewFileHistoryRewindTool(), map[string]any{"snapshot_id": snapshotID})
	if err != nil || rewind.IsError {
		t.Fatalf("rewind: err=%v output=%q", err, rewind.Output)
	}
	if got := readWorkspaceFile(t, path); got != "beta\n" {
		t.Fatalf("after rewind the file is %q, want %q", got, "beta\n")
	}
}

// A write that could not be backed up for an open snapshot could never be
// rewound, so the tools must refuse it rather than modify the file.
func TestFileToolsRefuseWritesThatCannotBeBackedUp(t *testing.T) {
	workspace := inWorkspace(t)
	blocker := writeWorkspaceFile(t, t.TempDir(), "not-a-directory", "")
	history := NewFileHistory(filepath.Join(blocker, "file-history"))
	SetGlobalFileHistory(history)
	t.Cleanup(func() { SetGlobalFileHistory(nil) })
	takeSnapshot(t, history, "checkpoint")

	const original = "alpha\n"
	const notebook = `{"cells": [], "metadata": {}, "nbformat": 4, "nbformat_minor": 5}`
	cases := []struct {
		name    string
		file    string
		content string
		tool    Tool
		params  map[string]any
	}{
		{"replace_string_in_file", "edit.txt", original, NewFileEditTool(), map[string]any{"filePath": "edit.txt", "oldString": "alpha", "newString": "beta"}},
		{"file_write", "write.txt", original, NewFileWriteTool(), map[string]any{"file_path": "write.txt", "content": "beta\n"}},
		{"apply_patch update", "update.txt", original, NewApplyPatchTool(), map[string]any{"input": "*** Begin Patch\n*** Update File: update.txt\n@@\n-alpha\n+beta\n*** End Patch"}},
		{"apply_patch delete", "delete.txt", original, NewApplyPatchTool(), map[string]any{"input": "*** Begin Patch\n*** Delete File: delete.txt\n*** End Patch"}},
		{"notebook_edit", "book.ipynb", notebook, NewNotebookEditTool(), map[string]any{"filePath": "book.ipynb", "operation": "insert", "cellType": "code", "source": "x = 1"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := writeWorkspaceFile(t, workspace, tc.file, tc.content)
			output, err := runValidatedTool(t, tc.tool, tc.params)
			if err == nil && !output.IsError {
				t.Fatalf("%s succeeded without a backup: %q", tc.name, output.Output)
			}
			if got := describeState(fileState(t, path)); got != tc.content {
				t.Fatalf("file changed to %q, want it untouched", got)
			}
		})
	}
}

// Rewinding a deletion recreates the file with the permissions it had, so an
// executable script comes back executable.
func TestFileHistoryRewindRestoresFileMode(t *testing.T) {
	history := NewFileHistory(t.TempDir())
	path := filepath.Join(t.TempDir(), "run.sh")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := os.Chmod(path, 0o755); err != nil {
		t.Fatalf("chmod: %v", err)
	}

	snapshotID := takeSnapshot(t, history, "checkpoint")
	trackedWrite(t, history, path, nil)
	rewindTo(t, history, snapshotID)

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat restored file: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o755 {
		t.Fatalf("restored mode = %v, want %v", got, fs.FileMode(0o755))
	}
}
