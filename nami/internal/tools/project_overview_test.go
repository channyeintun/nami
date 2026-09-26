package tools

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

func runProjectOverview(t *testing.T, params map[string]any) projectOverview {
	t.Helper()
	output, err := NewProjectOverviewTool().Execute(context.Background(), ToolInput{Params: params})
	if err != nil {
		t.Fatalf("execute project_overview: %v", err)
	}
	var overview projectOverview
	if err := json.Unmarshal([]byte(output.Output), &overview); err != nil {
		t.Fatalf("output %q is not a project overview: %v", output.Output, err)
	}
	if overview.Truncated != output.Truncated {
		t.Fatalf("summary truncated = %v, output truncated = %v", overview.Truncated, output.Truncated)
	}
	return overview
}

// Both a complete walk and one cut short by max_files must produce a summary;
// neither may come back as an empty result.
func TestProjectOverviewSummarizesWorkspace(t *testing.T) {
	workspace := inWorkspace(t)
	writeWorkspaceFile(t, workspace, "go.mod", "module example.com/x\n")
	writeWorkspaceFile(t, workspace, "cmd/main.go", "package main\n")
	writeWorkspaceFile(t, workspace, "cmd/util.go", "package main\n")

	cases := []struct {
		name          string
		params        map[string]any
		filesScanned  int
		truncated     bool
		wantManifests int
	}{
		{"complete walk", map[string]any{}, 3, false, 1},
		{"truncated walk", map[string]any{"max_files": 1}, 1, true, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			overview := runProjectOverview(t, tc.params)
			if overview.RootPath != workspace {
				t.Errorf("RootPath = %q, want %q", overview.RootPath, workspace)
			}
			if overview.FilesScanned != tc.filesScanned {
				t.Errorf("FilesScanned = %d, want %d", overview.FilesScanned, tc.filesScanned)
			}
			if overview.Truncated != tc.truncated {
				t.Errorf("Truncated = %v, want %v", overview.Truncated, tc.truncated)
			}
			if len(overview.ManifestFiles) != tc.wantManifests {
				t.Errorf("ManifestFiles = %v, want %d entries", overview.ManifestFiles, tc.wantManifests)
			}
		})
	}
}

func TestProjectOverviewStopsOnCancelledContext(t *testing.T) {
	workspace := inWorkspace(t)
	writeWorkspaceFile(t, workspace, "main.go", "package main\n")

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := NewProjectOverviewTool().Execute(ctx, ToolInput{Params: map[string]any{}})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}
