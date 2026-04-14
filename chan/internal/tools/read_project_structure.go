package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const (
	defaultProjectTreeDepth   = 4
	defaultProjectTreeEntries = 200
)

type ReadProjectStructureTool struct{}

type treeRenderState struct {
	lines      []string
	entries    int
	maxEntries int
	truncated  bool
}

func NewReadProjectStructureTool() *ReadProjectStructureTool {
	return &ReadProjectStructureTool{}
}

func (t *ReadProjectStructureTool) Name() string {
	return "read_project_structure"
}

func (t *ReadProjectStructureTool) Description() string {
	return "Get a file tree representation of the workspace or a subdirectory. Use this for navigation and structure, not for semantic repository summary."
}

func (t *ReadProjectStructureTool) InputSchema() any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"path": map[string]any{
				"type":        "string",
				"description": "Optional file or directory to inspect. Defaults to the current working directory.",
			},
			"max_depth": map[string]any{
				"type":        "integer",
				"description": "Optional maximum tree depth, where 0 means only the root. Defaults to 4.",
				"minimum":     0,
			},
			"max_entries": map[string]any{
				"type":        "integer",
				"description": "Optional maximum number of rendered entries below the root. Defaults to 200.",
				"minimum":     1,
			},
			"include_files": map[string]any{
				"type":        "boolean",
				"description": "Whether to include files in the tree. Defaults to true.",
			},
			"include_hidden": map[string]any{
				"type":        "boolean",
				"description": "Whether to include hidden entries, except large skipped directories such as .git and node_modules. Defaults to false.",
			},
		},
	}
}

func (t *ReadProjectStructureTool) Permission() PermissionLevel {
	return PermissionReadOnly
}

func (t *ReadProjectStructureTool) Concurrency(input ToolInput) ConcurrencyDecision {
	return ConcurrencyParallel
}

func (t *ReadProjectStructureTool) Validate(input ToolInput) error {
	if value, ok := intParam(input.Params, "max_depth"); ok && value < 0 {
		return fmt.Errorf("max_depth must be >= 0")
	}
	if value, ok := intParam(input.Params, "max_entries"); ok && value < 1 {
		return fmt.Errorf("max_entries must be >= 1")
	}
	return nil
}

func (t *ReadProjectStructureTool) Execute(ctx context.Context, input ToolInput) (ToolOutput, error) {
	searchPath, err := resolveSearchPath(input.Params)
	if err != nil {
		return ToolOutput{}, err
	}

	rootPath, err := normalizeOverviewRoot(searchPath)
	if err != nil {
		return ToolOutput{}, err
	}

	maxDepth := intOrDefault(input.Params, "max_depth", defaultProjectTreeDepth)
	maxEntries := intOrDefault(input.Params, "max_entries", defaultProjectTreeEntries)
	includeFiles := true
	if value, ok := input.Params["include_files"].(bool); ok {
		includeFiles = value
	}
	includeHidden := false
	if value, ok := input.Params["include_hidden"].(bool); ok {
		includeHidden = value
	}

	state := &treeRenderState{
		lines:      []string{fmt.Sprintf("Root: %s", rootPath), "."},
		maxEntries: maxEntries,
	}

	if err := renderProjectTree(ctx, state, rootPath, 0, maxDepth, nil, includeFiles, includeHidden); err != nil {
		return ToolOutput{}, err
	}

	if state.truncated {
		state.lines = append(state.lines, "", fmt.Sprintf("[Tree truncated after %d entries. Narrow the path or reduce max_depth to focus the view.]", state.entries))
	}

	return ToolOutput{
		Output:    strings.Join(state.lines, "\n"),
		Truncated: state.truncated,
	}, nil
}

func renderProjectTree(
	ctx context.Context,
	state *treeRenderState,
	absPath string,
	depth int,
	maxDepth int,
	prefixes []bool,
	includeFiles bool,
	includeHidden bool,
) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	if depth >= maxDepth {
		return nil
	}

	entries, err := os.ReadDir(absPath)
	if err != nil {
		return fmt.Errorf("read project structure at %q: %w", absPath, err)
	}

	filtered := make([]os.DirEntry, 0, len(entries))
	for _, entry := range entries {
		name := entry.Name()
		if !includeHidden && strings.HasPrefix(name, ".") {
			continue
		}
		if entry.IsDir() {
			if _, skip := projectOverviewSkippedDirs[name]; skip {
				continue
			}
		}
		if !includeFiles && !entry.IsDir() {
			continue
		}
		filtered = append(filtered, entry)
	}

	sort.Slice(filtered, func(i, j int) bool {
		if filtered[i].IsDir() != filtered[j].IsDir() {
			return filtered[i].IsDir()
		}
		return strings.ToLower(filtered[i].Name()) < strings.ToLower(filtered[j].Name())
	})

	for i, entry := range filtered {
		if state.entries >= state.maxEntries {
			state.truncated = true
			return nil
		}

		isLast := i == len(filtered)-1
		connector := "├── "
		if isLast {
			connector = "└── "
		}

		var builder strings.Builder
		for _, hasMore := range prefixes {
			if hasMore {
				builder.WriteString("│   ")
			} else {
				builder.WriteString("    ")
			}
		}
		builder.WriteString(connector)
		builder.WriteString(entry.Name())
		if entry.IsDir() {
			builder.WriteString("/")
		}

		state.lines = append(state.lines, builder.String())
		state.entries++

		if entry.IsDir() {
			nextPrefixes := append(append([]bool(nil), prefixes...), !isLast)
			nextPath := filepath.Join(absPath, entry.Name())
			if err := renderProjectTree(ctx, state, nextPath, depth+1, maxDepth, nextPrefixes, includeFiles, includeHidden); err != nil {
				return err
			}
			if state.truncated {
				return nil
			}
		}
	}

	return nil
}
