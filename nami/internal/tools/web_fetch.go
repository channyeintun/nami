package tools

import (
	"context"
	"fmt"
	"strings"

	"github.com/channyeintun/nami/internal/webfetch"
)

// searchReportThreshold is the result size above which a fetch is also saved as
// a search-report artifact so the model can revisit it without refetching.
const searchReportThreshold = 4000

// WebFetchTool fetches a URL, converts HTML to markdown, and returns
// prompt-focused content. The transport and rendering live in internal/webfetch.
type WebFetchTool struct {
	fetcher *webfetch.Fetcher
}

// NewWebFetchTool constructs the web fetch tool.
func NewWebFetchTool() *WebFetchTool {
	return &WebFetchTool{fetcher: webfetch.New()}
}

func (t *WebFetchTool) Name() string {
	return "web_fetch"
}

func (t *WebFetchTool) Description() string {
	return "Fetch content from a URL, convert HTML to markdown, and return either prompt-focused excerpts or markdown."
}

func (t *WebFetchTool) InputSchema() any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"url": map[string]any{
				"type":        "string",
				"description": "The URL to fetch.",
			},
			"prompt": map[string]any{
				"type":        "string",
				"description": "What information to extract from the fetched content. Required unless respond_with is markdown.",
			},
			"respond_with": map[string]any{
				"type":        "string",
				"description": "Optional output mode. Use markdown to return the converted page markdown directly.",
				"enum":        []string{"report", "markdown"},
			},
			"respondWith": map[string]any{
				"type":        "string",
				"description": "CamelCase alias for respond_with.",
				"enum":        []string{"report", "markdown"},
			},
		},
		"required": []string{"url"},
	}
}

func (t *WebFetchTool) Permission() PermissionLevel {
	return PermissionReadOnly
}

func (t *WebFetchTool) Concurrency(input ToolInput) ConcurrencyDecision {
	return ConcurrencyParallel
}

func (t *WebFetchTool) Execute(ctx context.Context, input ToolInput) (ToolOutput, error) {
	rawURL, ok := stringParam(input.Params, "url")
	if !ok || strings.TrimSpace(rawURL) == "" {
		return ToolOutput{}, fmt.Errorf("web_fetch requires url")
	}
	mode, prompt, err := webFetchRequestOptions(input.Params)
	if err != nil {
		return ToolOutput{}, err
	}

	normalizedURL, err := webfetch.NormalizeURL(rawURL)
	if err != nil {
		return ToolOutput{}, err
	}

	content, err := t.fetcher.Fetch(ctx, normalizedURL)
	if err != nil {
		return ToolOutput{}, err
	}

	result := webfetch.Render(normalizedURL, prompt, mode, content)
	if mode == webfetch.ModeReport && len(result) >= searchReportThreshold {
		if mutation, ok := saveSearchReportArtifact(ctx, normalizedURL, strings.TrimSpace(prompt), result); ok {
			return ToolOutput{Output: result, Artifacts: []ArtifactMutation{mutation}}, nil
		}
	}
	return ToolOutput{Output: result}, nil
}

// webFetchRequestOptions resolves the response mode and the prompt, which is
// only optional when raw markdown was requested.
func webFetchRequestOptions(params map[string]any) (webfetch.Mode, string, error) {
	rawMode, _ := firstStringParam(params, "respond_with", "respondWith")
	mode, err := webfetch.ParseMode(rawMode)
	if err != nil {
		return "", "", err
	}

	prompt, _ := stringParam(params, "prompt")
	prompt = strings.TrimSpace(prompt)
	if mode == webfetch.ModeReport && prompt == "" {
		return "", "", fmt.Errorf("web_fetch requires prompt")
	}
	return mode, prompt, nil
}
