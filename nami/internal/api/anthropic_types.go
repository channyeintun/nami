package api

import (
	"encoding/json"
	"fmt"
	"strings"
)

type anthropicRequest struct {
	Model         string                    `json:"model"`
	System        []anthropicTextBlock      `json:"system,omitempty"`
	Messages      []anthropicMessage        `json:"messages"`
	Tools         []anthropicToolDefinition `json:"tools,omitempty"`
	MaxTokens     int                       `json:"max_tokens"`
	Temperature   *float64                  `json:"temperature,omitempty"`
	StopSequences []string                  `json:"stop_sequences,omitempty"`
	Thinking      *anthropicThinking        `json:"thinking,omitempty"`
	Stream        bool                      `json:"stream"`
}

type anthropicThinking struct {
	Type         string `json:"type"`
	BudgetTokens int    `json:"budget_tokens,omitempty"`
}

type anthropicCacheControl struct {
	Type string `json:"type"`
}

type anthropicTextBlock struct {
	Type         string                 `json:"type"`
	Text         string                 `json:"text"`
	CacheControl *anthropicCacheControl `json:"cache_control,omitempty"`
}

type anthropicMessage struct {
	Role    string `json:"role"`
	Content any    `json:"content"`
}

type anthropicToolDefinition struct {
	Name         string                 `json:"name"`
	Description  string                 `json:"description,omitempty"`
	InputSchema  any                    `json:"input_schema"`
	CacheControl *anthropicCacheControl `json:"cache_control,omitempty"`
}

type anthropicUsage struct {
	InputTokens         int `json:"input_tokens,omitempty"`
	OutputTokens        int `json:"output_tokens,omitempty"`
	CacheReadTokens     int `json:"cache_read_input_tokens,omitempty"`
	CacheCreationTokens int `json:"cache_creation_input_tokens,omitempty"`
}

func (u *anthropicUsage) merge(other anthropicUsage) {
	if other.InputTokens > 0 {
		u.InputTokens = other.InputTokens
	}
	if other.OutputTokens > 0 {
		u.OutputTokens = other.OutputTokens
	}
	if other.CacheReadTokens > 0 {
		u.CacheReadTokens = other.CacheReadTokens
	}
	if other.CacheCreationTokens > 0 {
		u.CacheCreationTokens = other.CacheCreationTokens
	}
}

func (u anthropicUsage) clone() *Usage {
	return &Usage{InputTokens: u.InputTokens, OutputTokens: u.OutputTokens, CacheReadTokens: u.CacheReadTokens, CacheCreationTokens: u.CacheCreationTokens}
}

type anthropicMessageStartEvent struct {
	Message struct {
		Usage *anthropicUsage `json:"usage,omitempty"`
	} `json:"message"`
}

type anthropicContentBlockStartEvent struct {
	Index        int                   `json:"index"`
	ContentBlock anthropicContentBlock `json:"content_block"`
}

type anthropicContentBlock struct {
	Type     string          `json:"type"`
	Text     string          `json:"text,omitempty"`
	Thinking string          `json:"thinking,omitempty"`
	ID       string          `json:"id,omitempty"`
	Name     string          `json:"name,omitempty"`
	Input    json.RawMessage `json:"input,omitempty"`
}

type anthropicContentBlockDeltaEvent struct {
	Index int                 `json:"index"`
	Delta anthropicBlockDelta `json:"delta"`
}

type anthropicBlockDelta struct {
	Type        string `json:"type"`
	Text        string `json:"text,omitempty"`
	Thinking    string `json:"thinking,omitempty"`
	PartialJSON string `json:"partial_json,omitempty"`
}

type anthropicContentBlockStopEvent struct {
	Index int `json:"index"`
}

type anthropicMessageDeltaEvent struct {
	Delta struct {
		StopReason string `json:"stop_reason,omitempty"`
	} `json:"delta"`
	Usage *anthropicUsage `json:"usage,omitempty"`
}

type anthropicStreamErrorEvent struct {
	Error struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error"`
}

type anthropicErrorEnvelope struct {
	Error struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error"`
}

type anthropicStreamState struct {
	usage      anthropicUsage
	stopReason string
	toolBlocks map[int]*anthropicToolUseState
	// toolInputErr holds why a tool call's input failed to decode. A response
	// cut off by max_tokens ends inside its last call's input, and that is
	// only known once message_delta arrives, so the error waits for
	// message_stop.
	toolInputErr error
}

type anthropicToolUseState struct {
	ID      string
	Name    string
	Initial json.RawMessage
	Builder strings.Builder
}

func (s *anthropicToolUseState) inputJSON() (string, error) {
	if s.Builder.Len() > 0 {
		var decoded any
		if err := json.Unmarshal([]byte(s.Builder.String()), &decoded); err != nil {
			return "", fmt.Errorf("decode anthropic tool input delta: %w", err)
		}
		encoded, err := json.Marshal(decoded)
		if err != nil {
			return "", fmt.Errorf("encode anthropic tool input delta: %w", err)
		}
		return string(encoded), nil
	}
	if len(s.Initial) > 0 {
		var decoded any
		if err := json.Unmarshal(s.Initial, &decoded); err != nil {
			return "", fmt.Errorf("decode anthropic tool input: %w", err)
		}
		encoded, err := json.Marshal(decoded)
		if err != nil {
			return "", fmt.Errorf("encode anthropic tool input: %w", err)
		}
		return string(encoded), nil
	}
	return "{}", nil
}
