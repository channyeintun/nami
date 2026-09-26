package api

import (
	"encoding/json"
	"net/http"
	"strings"
)

func (c *OpenAICompatClient) resolveAPIKey() (string, error) {
	c.mu.RLock()
	apiKeyFunc := c.apiKeyFunc
	c.mu.RUnlock()
	if apiKeyFunc != nil {
		return apiKeyFunc()
	}
	return c.apiKey, nil
}

func (c *OpenAICompatClient) resolveBaseURL(apiKey string) string {
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

func classifyOpenAICompatStatus(statusCode int, body []byte) error {
	var envelope openAICompatErrorEnvelope
	if err := json.Unmarshal(body, &envelope); err != nil || envelope.Error == nil {
		message := strings.TrimSpace(string(body))
		if message == "" {
			message = http.StatusText(statusCode)
		}
		return &APIError{
			Type:       classifyOpenAICompatErrorType(statusCode, "", message),
			StatusCode: statusCode,
			Message:    message,
		}
	}

	message := openAICompatErrorMessage(*envelope.Error)
	if message == "" {
		message = http.StatusText(statusCode)
	}

	return &APIError{
		Type:       classifyOpenAICompatErrorType(statusCode, envelope.Error.Type, message),
		StatusCode: statusCode,
		Message:    message,
	}
}

func classifyOpenAICompatErrorType(statusCode int, errorType, message string) APIErrorType {
	lowerType := strings.ToLower(errorType)
	lowerMessage := strings.ToLower(message)

	switch {
	case statusCode == http.StatusUnauthorized || statusCode == http.StatusForbidden:
		return ErrAuth
	case statusCode == http.StatusTooManyRequests || strings.Contains(lowerType, "rate_limit"):
		return ErrRateLimit
	case statusCode >= http.StatusInternalServerError || strings.Contains(lowerType, "server_error") || strings.Contains(lowerType, "overloaded"):
		return ErrOverloaded
	case strings.Contains(lowerMessage, "maximum context length") || strings.Contains(lowerMessage, "context length") || strings.Contains(lowerMessage, "prompt too long"):
		return ErrPromptTooLong
	case strings.Contains(lowerMessage, "max_tokens") || strings.Contains(lowerMessage, "maximum output tokens"):
		return ErrMaxTokens
	default:
		return ErrUnknown
	}
}

func mapOpenAICompatStopReason(reason string) string {
	switch reason {
	case "stop", "end_turn":
		return "end_turn"
	case "tool_calls", "function_call":
		return "tool_use"
	case "length", "max_tokens":
		return "max_tokens"
	default:
		return reason
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func openAICompatErrorMessage(err openAICompatErrorBody) string {
	message := strings.TrimSpace(err.Message)
	if err.Metadata == nil {
		return message
	}

	raw := strings.TrimSpace(err.Metadata.Raw)
	providerName := strings.TrimSpace(err.Metadata.ProviderName)
	if raw == "" {
		return message
	}

	if message == "" || strings.EqualFold(message, "Provider returned error") {
		if providerName != "" {
			return providerName + ": " + raw
		}
		return raw
	}

	if providerName != "" && !strings.Contains(raw, providerName) {
		return message + " (" + providerName + ": " + raw + ")"
	}
	return message + " (" + raw + ")"
}
