package api

import "strings"

func (c *OpenAIResponsesClient) resolveAPIKey() (string, error) {
	c.mu.RLock()
	apiKeyFunc := c.apiKeyFunc
	c.mu.RUnlock()
	if apiKeyFunc != nil {
		return apiKeyFunc()
	}
	return c.apiKey, nil
}

func (c *OpenAIResponsesClient) resolveCodexAccountID() string {
	c.mu.RLock()
	accountIDFunc := c.codexAccountIDFunc
	accountID := c.codexAccountID
	c.mu.RUnlock()
	if accountIDFunc != nil {
		if current := strings.TrimSpace(accountIDFunc()); current != "" {
			return current
		}
	}
	return accountID
}

func (c *OpenAIResponsesClient) resolveBaseURL(apiKey string) string {
	if c.provider != "github-copilot" {
		return c.baseURL
	}
	c.mu.RLock()
	enterpriseDomain := c.enterpriseDomain
	c.mu.RUnlock()
	resolved := strings.TrimRight(GetGitHubCopilotBaseURL(apiKey, enterpriseDomain), "/")
	if resolved == "" {
		return c.baseURL
	}
	return resolved
}

func openAIResponsesUsesDeveloperRole(model string) bool {
	lower := strings.ToLower(strings.TrimSpace(model))
	prefixes := []string{"gpt-5", "o1", "o3", "o4"}
	for _, p := range prefixes {
		if strings.HasPrefix(lower, p) {
			return true
		}
	}
	return false
}

func openAIResponsesStopReason(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "completed", "in_progress", "queued", "":
		return "end_turn"
	case "incomplete":
		return "max_tokens"
	default:
		return "end_turn"
	}
}
