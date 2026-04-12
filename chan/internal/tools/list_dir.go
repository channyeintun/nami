package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
)

type ListDirTool struct{}

type listDirEntry struct {
	Name      string `json:"name"`
	IsDir     bool   `json:"isDir"`
	SizeBytes int64  `json:"sizeBytes,omitempty"`
}

func NewListDirTool() *ListDirTool {
	return &ListDirTool{}
}

func (t *ListDirTool) Name() string {
	return "list_dir"
}

func (t *ListDirTool) Description() string {
	return "List the direct contents of a directory as a structured JSON array."
}

func (t *ListDirTool) InputSchema() any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"DirectoryPath": map[string]any{
				"type":        "string",
				"description": "The absolute path to the directory to inspect.",
			},
			"directory_path": map[string]any{
				"type":        "string",
				"description": "Snake_case alias for the directory path to inspect.",
			},
		},
		"anyOf": []map[string]any{
			{"required": []string{"DirectoryPath"}},
			{"required": []string{"directory_path"}},
		},
	}
}

func (t *ListDirTool) Permission() PermissionLevel {
	return PermissionReadOnly
}

func (t *ListDirTool) Concurrency(input ToolInput) ConcurrencyDecision {
	return ConcurrencyParallel
}

func (t *ListDirTool) Execute(ctx context.Context, input ToolInput) (ToolOutput, error) {
	select {
	case <-ctx.Done():
		return ToolOutput{}, ctx.Err()
	default:
	}

	directoryPath, ok := firstStringParam(input.Params, "DirectoryPath", "directory_path")
	if !ok || strings.TrimSpace(directoryPath) == "" {
		return ToolOutput{}, fmt.Errorf("list_dir requires DirectoryPath")
	}
	directoryPath, err := resolveToolPath(directoryPath)
	if err != nil {
		return ToolOutput{}, err
	}

	info, err := os.Stat(directoryPath)
	if err != nil {
		return ToolOutput{}, fmt.Errorf("stat directory %q: %w", directoryPath, err)
	}
	if !info.IsDir() {
		return ToolOutput{}, fmt.Errorf("%q is not a directory", directoryPath)
	}

	entries, err := os.ReadDir(directoryPath)
	if err != nil {
		return ToolOutput{}, fmt.Errorf("read directory %q: %w", directoryPath, err)
	}

	results := make([]listDirEntry, 0, len(entries))
	for _, entry := range entries {
		select {
		case <-ctx.Done():
			return ToolOutput{}, ctx.Err()
		default:
		}

		item := listDirEntry{
			Name:  entry.Name(),
			IsDir: entry.IsDir(),
		}
		if !entry.IsDir() {
			entryInfo, err := entry.Info()
			if err == nil {
				item.SizeBytes = entryInfo.Size()
			}
		}
		results = append(results, item)
	}

	sort.Slice(results, func(i, j int) bool {
		if results[i].IsDir != results[j].IsDir {
			return results[i].IsDir
		}
		return strings.ToLower(results[i].Name) < strings.ToLower(results[j].Name)
	})

	encoded, err := json.Marshal(results)
	if err != nil {
		return ToolOutput{}, fmt.Errorf("marshal list_dir results: %w", err)
	}

	return ToolOutput{Output: string(encoded)}, nil
}
