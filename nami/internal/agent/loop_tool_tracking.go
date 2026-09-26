package agent

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/channyeintun/nami/internal/api"
	"github.com/channyeintun/nami/internal/textutil"
)

const retrievalTouchedLimit = 64

func buildEditRetryNudge(calls []api.ToolCall, results []api.ToolResult) string {
	callByID := make(map[string]api.ToolCall, len(calls))
	for _, call := range calls {
		callByID[call.ID] = call
	}

	seen := make(map[string]struct{})
	var files []string
	for _, result := range results {
		if !result.IsError {
			continue
		}
		path := result.FilePath
		if path == "" {
			call := callByID[result.ToolCallID]
			path = extractFilePathFromInput(call.Input)
		}
		if path != "" {
			if _, ok := seen[path]; !ok {
				seen[path] = struct{}{}
				files = append(files, path)
			}
		}
	}
	if len(files) == 0 {
		return "[system] Your previous file edit failed with the same error again. You are using stale file contents. Re-read the target file before retrying the edit."
	}
	return fmt.Sprintf("[system] Your previous file edit failed with the same error again. You are using stale file contents. Re-read the following file(s) before retrying: %s", strings.Join(files, ", "))
}

func extractFilePathFromInput(input string) string {
	if input == "" {
		return ""
	}
	var params map[string]any
	if err := json.Unmarshal([]byte(input), &params); err != nil {
		return ""
	}
	for _, key := range []string{"file_path", "FilePath", "target_file", "TargetFile", "path"} {
		if value, ok := params[key]; ok {
			if path, ok := value.(string); ok && path != "" {
				return path
			}
		}
	}
	return ""
}

func collectTouchedFiles(state *QueryState, calls []api.ToolCall, results []api.ToolResult) {
	if state == nil {
		return
	}
	cwd := state.TurnContext.CurrentDir
	seen := make(map[string]struct{}, len(state.RetrievalTouched))
	for _, path := range state.RetrievalTouched {
		seen[path] = struct{}{}
	}
	addTouchedPath := func(path string) {
		for _, resolved := range resolveFilePath(path, cwd) {
			if _, ok := seen[resolved]; ok {
				continue
			}
			seen[resolved] = struct{}{}
			state.RetrievalTouched = append(state.RetrievalTouched, resolved)
		}
	}

	for _, call := range calls {
		for _, path := range extractFilePathsFromToolCall(call) {
			addTouchedPath(path)
		}
	}
	for _, result := range results {
		addTouchedPath(result.FilePath)
		for _, path := range extractFilePathMatches(result.Output) {
			addTouchedPath(path)
		}
	}
	if len(state.RetrievalTouched) > retrievalTouchedLimit {
		state.RetrievalTouched = append([]string(nil), state.RetrievalTouched[len(state.RetrievalTouched)-retrievalTouchedLimit:]...)
	}
}

func invalidateGraphFiles(state *QueryState, calls []api.ToolCall, results []api.ToolResult) {
	if state == nil || state.Graph == nil {
		return
	}
	cwd := state.TurnContext.CurrentDir
	invalidated := make(map[string]struct{})
	invalidate := func(path string) {
		for _, resolved := range resolveFilePath(path, cwd) {
			if _, done := invalidated[resolved]; done {
				continue
			}
			invalidated[resolved] = struct{}{}
			state.Graph.Invalidate(resolved)
		}
	}
	for _, call := range calls {
		for _, path := range extractFilePathsFromToolCall(call) {
			invalidate(path)
		}
	}
	for _, result := range results {
		invalidate(result.FilePath)
	}
}

func extractFilePathsFromToolCall(call api.ToolCall) []string {
	type genericInput struct {
		Path            string `json:"path"`
		TargetFile      string `json:"target_file"`
		FilePath        string `json:"file_path"`
		FilePathCompat  string `json:"filePath"`
		File            string `json:"file"`
		DirPath         string `json:"dirPath"`
		WorkspaceFolder string `json:"workspaceFolder"`
		RootPath        string `json:"rootPath"`
		URI             string `json:"uri"`
	}

	var input genericInput
	if err := json.Unmarshal([]byte(call.Input), &input); err != nil {
		return nil
	}

	var paths []string
	for _, path := range []string{
		input.Path,
		input.TargetFile,
		input.FilePath,
		input.FilePathCompat,
		input.File,
		input.DirPath,
		input.WorkspaceFolder,
		input.RootPath,
		input.URI,
	} {
		path = strings.TrimSpace(path)
		if path != "" {
			paths = append(paths, path)
		}
	}
	return paths
}

func recordFailedAttempts(log *AttemptLog, calls []api.ToolCall, results []api.ToolResult) (int, error) {
	if log == nil || len(results) == 0 {
		return 0, nil
	}

	hasErrorResult := false
	for _, result := range results {
		if result.IsError {
			hasErrorResult = true
			break
		}
	}
	if !hasErrorResult {
		return 0, nil
	}

	existing, err := log.Load()
	if err != nil {
		return 0, err
	}
	existingSigs := make(map[string]struct{}, len(existing))
	for _, entry := range existing {
		if entry.ErrorSignature != "" {
			existingSigs[entry.ErrorSignature] = struct{}{}
		}
	}

	callByID := make(map[string]api.ToolCall, len(calls))
	for _, call := range calls {
		callByID[call.ID] = call
	}

	repeated := 0
	for _, result := range results {
		if !result.IsError {
			continue
		}
		call := callByID[result.ToolCallID]
		signature := errorSignatureFromOutput(result.Output)
		if signature != "" {
			if _, wasLogged := existingSigs[signature]; wasLogged {
				repeated++
			}
		}
		entry := AttemptEntry{
			Command:        call.Name,
			ErrorSignature: signature,
		}
		if err := log.Record(entry); err != nil {
			return repeated, err
		}
	}
	return repeated, nil
}

func errorSignatureFromOutput(output string) string {
	output = strings.TrimSpace(output)
	if output == "" {
		return ""
	}
	lines := strings.SplitN(output, "\n", 4)
	signature := strings.TrimSpace(lines[0])
	return textutil.TruncateHead(signature, 120)
}
