package api

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
)

func (c *AnthropicClient) resolveAPIKey() (string, error) {
	c.mu.RLock()
	apiKeyFunc := c.apiKeyFunc
	c.mu.RUnlock()
	if apiKeyFunc != nil {
		return apiKeyFunc()
	}
	return c.apiKey, nil
}

func (c *AnthropicClient) resolveBaseURL(apiKey string) string {
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

// claudeTakesThinkingBudget reports whether a Claude model predates 4.6 and so
// configures extended thinking with a fixed budget_tokens. 4.6 deprecated the
// budget in favour of adaptive thinking, and Opus 4.7 and every later model
// reject it with a 400. An id whose version cannot be read is assumed to name
// a current model.
func claudeTakesThinkingBudget(model string) bool {
	major, minor, ok := claudeModelVersion(model)
	if !ok {
		return false
	}
	return major < 4 || (major == 4 && minor < 6)
}

// claudeModelVersion reads the major and minor generation out of a Claude
// model id in any of the spellings in use: "claude-opus-4-7",
// "claude-sonnet-4.5", "claude-sonnet-4-20250514" (a release date, not a minor
// version) and the older "claude-3-7-sonnet-20250219".
func claudeModelVersion(model string) (int, int, bool) {
	fields := strings.FieldsFunc(strings.ToLower(model), func(r rune) bool {
		return r == '-' || r == '.'
	})
	for i, field := range fields {
		if len(field) > 2 {
			continue
		}
		major, err := strconv.Atoi(field)
		if err != nil {
			continue
		}
		minor := 0
		if i+1 < len(fields) && len(fields[i+1]) <= 2 {
			if next, err := strconv.Atoi(fields[i+1]); err == nil {
				minor = next
			}
		}
		return major, minor, true
	}
	return 0, 0, false
}

func classifyAnthropicStatus(statusCode int, body []byte) error {
	var envelope anthropicErrorEnvelope
	if err := json.Unmarshal(body, &envelope); err != nil {
		message := strings.TrimSpace(string(body))
		if message == "" {
			message = http.StatusText(statusCode)
		}
		return &APIError{Type: classifyAnthropicErrorType(statusCode, "", message), StatusCode: statusCode, Message: message}
	}

	message := envelope.Error.Message
	if message == "" {
		message = http.StatusText(statusCode)
	}

	return &APIError{Type: classifyAnthropicErrorType(statusCode, envelope.Error.Type, message), StatusCode: statusCode, Message: message}
}

func classifyAnthropicErrorType(statusCode int, errorType, message string) APIErrorType {
	lowerType := strings.ToLower(errorType)
	lowerMessage := strings.ToLower(message)

	switch {
	case statusCode == http.StatusUnauthorized || statusCode == http.StatusForbidden:
		return ErrAuth
	case statusCode == http.StatusTooManyRequests || strings.Contains(lowerType, "rate_limit"):
		return ErrRateLimit
	case statusCode == 529 || statusCode >= http.StatusInternalServerError || strings.Contains(lowerType, "overloaded"):
		return ErrOverloaded
	case strings.Contains(lowerMessage, "prompt is too long") || strings.Contains(lowerMessage, "prompt too long") || strings.Contains(lowerMessage, "context length"):
		return ErrPromptTooLong
	case strings.Contains(lowerMessage, "max tokens"):
		return ErrMaxTokens
	default:
		return ErrUnknown
	}
}
