package main

import (
	"context"
	"fmt"
	"iter"

	"github.com/channyeintun/gocode/internal/api"
	"github.com/channyeintun/gocode/internal/debuglog"
)

// debugClientProxy wraps an api.LLMClient and logs every method call.
type debugClientProxy struct {
	inner api.LLMClient
}

func newDebugClientProxy(inner api.LLMClient) *debugClientProxy {
	return &debugClientProxy{inner: inner}
}

func (p *debugClientProxy) Stream(ctx context.Context, req api.ModelRequest) (iter.Seq2[api.ModelEvent, error], error) {
	debuglog.Log("client", "stream_request", map[string]any{
		"model":            p.inner.ModelID(),
		"system_len":       len(req.SystemPrompt),
		"message_count":    len(req.Messages),
		"tool_count":       len(req.Tools),
		"max_tokens":       req.MaxTokens,
		"thinking_budget":  req.ThinkingBudget,
		"reasoning_effort": req.ReasoningEffort,
	})

	it, err := p.inner.Stream(ctx, req)
	if err != nil {
		debuglog.Log("client", "stream_error", map[string]any{
			"error": err.Error(),
		})
		return nil, err
	}

	return func(yield func(api.ModelEvent, error) bool) {
		eventIndex := 0
		for ev, evErr := range it {
			fields := map[string]any{
				"index": eventIndex,
				"type":  debugEventTypeName(ev.Type),
			}
			switch ev.Type {
			case api.ModelEventToken:
				fields["text_len"] = len(ev.Text)
			case api.ModelEventThinking:
				fields["text_len"] = len(ev.Text)
			case api.ModelEventToolCall:
				if ev.ToolCall != nil {
					fields["tool_name"] = ev.ToolCall.Name
					fields["tool_id"] = ev.ToolCall.ID
					fields["input_len"] = len(ev.ToolCall.Input)
				}
			case api.ModelEventStop:
				fields["stop_reason"] = ev.StopReason
			case api.ModelEventUsage:
				if ev.Usage != nil {
					fields["input_tokens"] = ev.Usage.InputTokens
					fields["output_tokens"] = ev.Usage.OutputTokens
					fields["cache_read"] = ev.Usage.CacheReadTokens
				}
			}
			if evErr != nil {
				fields["error"] = evErr.Error()
			}
			debuglog.Log("client", "stream_event", fields)
			eventIndex++
			if !yield(ev, evErr) {
				debuglog.Log("client", "stream_yield_break", map[string]any{"index": eventIndex})
				return
			}
		}
		debuglog.Log("client", "stream_done", map[string]any{"total_events": eventIndex})
	}, nil
}

func (p *debugClientProxy) ModelID() string {
	return p.inner.ModelID()
}

func (p *debugClientProxy) Capabilities() api.ModelCapabilities {
	return p.inner.Capabilities()
}

func (p *debugClientProxy) Warmup(ctx context.Context) error {
	warmable, ok := p.inner.(api.WarmupCapable)
	if !ok || warmable == nil {
		return nil
	}
	debuglog.Log("client", "warmup_start", nil)
	err := warmable.Warmup(ctx)
	if err != nil {
		debuglog.Log("client", "warmup_error", map[string]any{"error": err.Error()})
	} else {
		debuglog.Log("client", "warmup_done", nil)
	}
	return err
}

func debugEventTypeName(t api.ModelEventType) string {
	switch t {
	case api.ModelEventToken:
		return "token"
	case api.ModelEventThinking:
		return "thinking"
	case api.ModelEventToolCall:
		return "tool_call"
	case api.ModelEventStop:
		return "stop"
	case api.ModelEventUsage:
		return "usage"
	case api.ModelEventRateLimits:
		return "rate_limits"
	default:
		return fmt.Sprintf("unknown(%d)", int(t))
	}
}
