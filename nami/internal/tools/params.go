package tools

import "strings"

// Tools accept several spellings for the same argument because different
// providers and prompt styles name them differently. The first* helpers take
// the aliases in preference order and return the first one supplied.

func firstStringParam(params map[string]any, keys ...string) (string, bool) {
	for _, key := range keys {
		if value, ok := stringParam(params, key); ok && strings.TrimSpace(value) != "" {
			return value, true
		}
	}
	return "", false
}

func firstIntParam(params map[string]any, keys ...string) (int, bool) {
	for _, key := range keys {
		if value, ok := intParam(params, key); ok {
			return value, true
		}
	}
	return 0, false
}

func firstParam(params map[string]any, keys ...string) (any, bool) {
	for _, key := range keys {
		if value, ok := params[key]; ok {
			return value, true
		}
	}
	return nil, false
}

func firstBoolParam(params map[string]any, keys ...string) bool {
	for _, key := range keys {
		if value, ok := params[key]; ok {
			switch typed := value.(type) {
			case bool:
				return typed
			case string:
				return strings.EqualFold(typed, "true")
			}
		}
	}
	return false
}
