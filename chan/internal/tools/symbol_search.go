package tools

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

const defaultSymbolSearchLimit = 100

type symbolPattern struct {
	kind   string
	regex  *regexp.Regexp
	weight int
}

type symbolMatch struct {
	path    string
	line    int
	kind    string
	content string
	weight  int
	index   int
}

var symbolSearchPatterns = map[string][]symbolPattern{
	".go": {
		{kind: "func", regex: regexp.MustCompile(`^\s*func\s+(?:\([^)]*\)\s*)?%s\b`), weight: 100},
		{kind: "type", regex: regexp.MustCompile(`^\s*type\s+%s\b`), weight: 95},
		{kind: "const", regex: regexp.MustCompile(`^\s*const\s+%s\b`), weight: 90},
		{kind: "var", regex: regexp.MustCompile(`^\s*var\s+%s\b`), weight: 85},
	},
	".py": {
		{kind: "function", regex: regexp.MustCompile(`^\s*def\s+%s\b`), weight: 100},
		{kind: "class", regex: regexp.MustCompile(`^\s*class\s+%s\b`), weight: 95},
	},
	".js":  jsSymbolPatterns(),
	".jsx": jsSymbolPatterns(),
	".ts":  tsSymbolPatterns(),
	".tsx": tsSymbolPatterns(),
	".java": {
		{kind: "class", regex: regexp.MustCompile(`^\s*(?:public|private|protected|abstract|final|static|\s)*class\s+%s\b`), weight: 100},
		{kind: "interface", regex: regexp.MustCompile(`^\s*(?:public|private|protected|abstract|static|\s)*interface\s+%s\b`), weight: 95},
		{kind: "enum", regex: regexp.MustCompile(`^\s*(?:public|private|protected|static|\s)*enum\s+%s\b`), weight: 90},
		{kind: "method", regex: regexp.MustCompile(`^\s*(?:public|private|protected|static|final|synchronized|native|abstract|\s)+[A-Za-z0-9_<>,\[\]]+\s+%s\s*\(`), weight: 85},
	},
	".rs": {
		{kind: "fn", regex: regexp.MustCompile(`^\s*(?:pub\s+)?fn\s+%s\b`), weight: 100},
		{kind: "struct", regex: regexp.MustCompile(`^\s*(?:pub\s+)?struct\s+%s\b`), weight: 95},
		{kind: "enum", regex: regexp.MustCompile(`^\s*(?:pub\s+)?enum\s+%s\b`), weight: 90},
		{kind: "trait", regex: regexp.MustCompile(`^\s*(?:pub\s+)?trait\s+%s\b`), weight: 85},
	},
}

// SymbolSearchTool finds likely symbol definitions across source files.
type SymbolSearchTool struct{}

// NewSymbolSearchTool constructs the symbol search tool.
func NewSymbolSearchTool() *SymbolSearchTool {
	return &SymbolSearchTool{}
}

func (t *SymbolSearchTool) Name() string {
	return "symbol_search"
}

func (t *SymbolSearchTool) Description() string {
	return "Find likely symbol definitions across common source files, returning file paths, line numbers, and symbol kinds."
}

func (t *SymbolSearchTool) InputSchema() any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"symbol": map[string]any{
				"type":        "string",
				"description": "The symbol name to search for.",
			},
			"path": map[string]any{
				"type":        "string",
				"description": "Optional file or directory to search in. Defaults to the current working directory.",
			},
			"max_results": map[string]any{
				"type":        "integer",
				"description": "Optional maximum number of results to return. Defaults to 100.",
				"minimum":     1,
			},
		},
		"required": []string{"symbol"},
	}
}

func (t *SymbolSearchTool) Permission() PermissionLevel {
	return PermissionReadOnly
}

func (t *SymbolSearchTool) Concurrency(input ToolInput) ConcurrencyDecision {
	return ConcurrencyParallel
}

func (t *SymbolSearchTool) Validate(input ToolInput) error {
	symbol, ok := stringParam(input.Params, "symbol")
	if !ok || strings.TrimSpace(symbol) == "" {
		return fmt.Errorf("symbol_search requires symbol")
	}
	if value, ok := intParam(input.Params, "max_results"); ok && value < 1 {
		return fmt.Errorf("max_results must be >= 1")
	}
	return nil
}

func (t *SymbolSearchTool) Execute(ctx context.Context, input ToolInput) (ToolOutput, error) {
	symbol, _ := stringParam(input.Params, "symbol")
	symbol = strings.TrimSpace(symbol)
	searchPath, err := resolveSearchPath(input.Params)
	if err != nil {
		return ToolOutput{}, err
	}
	maxResults := intOrDefault(input.Params, "max_results", defaultSymbolSearchLimit)
	matcherSet := buildSymbolMatchers(symbol)
	if len(matcherSet) == 0 {
		return ToolOutput{Output: "No supported source file patterns for symbol search."}, nil
	}

	matches := make([]symbolMatch, 0, maxResults)
	truncated := false
	nextIndex := 0

	collectFromFile := func(filePath string) error {
		file, err := os.Open(filePath)
		if err != nil {
			return nil
		}
		defer file.Close()

		scanner := bufio.NewScanner(file)
		lineNo := 0
		for scanner.Scan() {
			select {
			case <-ctx.Done():
				return ctx.Err()
			default:
			}
			lineNo++
			line := scanner.Text()
			for _, pattern := range matcherSet[filepath.Ext(filePath)] {
				if pattern.regex.MatchString(line) {
					matches = append(matches, symbolMatch{
						path:    filePath,
						line:    lineNo,
						kind:    pattern.kind,
						content: strings.TrimSpace(line),
						weight:  pattern.weight,
						index:   nextIndex,
					})
					nextIndex++
					if len(matches) >= maxResults {
						truncated = true
						return nil
					}
				}
			}
		}
		return nil
	}

	info, err := os.Stat(searchPath)
	if err != nil {
		return ToolOutput{}, err
	}
	if !info.IsDir() {
		if _, ok := matcherSet[filepath.Ext(searchPath)]; ok {
			if err := collectFromFile(searchPath); err != nil {
				return ToolOutput{}, err
			}
		}
	} else {
		walkErr := filepath.Walk(searchPath, func(path string, info os.FileInfo, walkErr error) error {
			if walkErr != nil {
				return nil
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			default:
			}
			if info.IsDir() {
				name := info.Name()
				if name == ".git" || name == "node_modules" || name == "dist" || name == "build" {
					return filepath.SkipDir
				}
				return nil
			}
			if _, ok := matcherSet[filepath.Ext(path)]; !ok {
				return nil
			}
			if len(matches) >= maxResults {
				truncated = true
				return filepath.SkipAll
			}
			return collectFromFile(path)
		})
		if walkErr != nil && walkErr != filepath.SkipAll {
			return ToolOutput{}, walkErr
		}
	}

	if len(matches) == 0 {
		return ToolOutput{Output: "No symbol definitions found"}, nil
	}

	sort.SliceStable(matches, func(i, j int) bool {
		if matches[i].weight != matches[j].weight {
			return matches[i].weight > matches[j].weight
		}
		if matches[i].path != matches[j].path {
			return matches[i].path < matches[j].path
		}
		if matches[i].line != matches[j].line {
			return matches[i].line < matches[j].line
		}
		return matches[i].index < matches[j].index
	})

	lines := make([]string, 0, len(matches)+1)
	for _, match := range matches {
		lines = append(lines, fmt.Sprintf("%s:%d [%s] %s", match.path, match.line, match.kind, match.content))
	}
	if truncated {
		lines = append(lines, "(Results are truncated. Use a narrower path or smaller max_results.)")
	}

	return ToolOutput{Output: strings.Join(lines, "\n"), Truncated: truncated}, nil
}

func buildSymbolMatchers(symbol string) map[string][]symbolPattern {
	escaped := regexp.QuoteMeta(symbol)
	patterns := make(map[string][]symbolPattern, len(symbolSearchPatterns))
	for extension, matcherList := range symbolSearchPatterns {
		compiled := make([]symbolPattern, 0, len(matcherList))
		for _, pattern := range matcherList {
			compiled = append(compiled, symbolPattern{
				kind:   pattern.kind,
				regex:  regexp.MustCompile(fmt.Sprintf(pattern.regex.String(), escaped)),
				weight: pattern.weight,
			})
		}
		patterns[extension] = compiled
	}
	return patterns
}

func jsSymbolPatterns() []symbolPattern {
	return []symbolPattern{
		{kind: "function", regex: regexp.MustCompile(`^\s*(?:export\s+)?function\s+%s\b`), weight: 100},
		{kind: "class", regex: regexp.MustCompile(`^\s*(?:export\s+)?class\s+%s\b`), weight: 95},
		{kind: "const", regex: regexp.MustCompile(`^\s*(?:export\s+)?const\s+%s\b`), weight: 90},
		{kind: "let", regex: regexp.MustCompile(`^\s*(?:export\s+)?let\s+%s\b`), weight: 85},
		{kind: "var", regex: regexp.MustCompile(`^\s*(?:export\s+)?var\s+%s\b`), weight: 80},
	}
}

func tsSymbolPatterns() []symbolPattern {
	patterns := jsSymbolPatterns()
	return append([]symbolPattern{
		{kind: "interface", regex: regexp.MustCompile(`^\s*(?:export\s+)?interface\s+%s\b`), weight: 98},
		{kind: "type", regex: regexp.MustCompile(`^\s*(?:export\s+)?type\s+%s\b`), weight: 97},
		{kind: "enum", regex: regexp.MustCompile(`^\s*(?:export\s+)?enum\s+%s\b`), weight: 96},
	}, patterns...)
}
