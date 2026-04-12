package permissions

import (
	"context"
	"fmt"

	"github.com/channyeintun/gocode/internal/tools"
)

// Request describes a tool approval decision that may need user interaction.
type Request struct {
	ToolName        string
	Input           tools.ToolInput
	PermissionLevel tools.PermissionLevel
}

// RequestHandler optionally resolves ask-decisions interactively.
type RequestHandler func(context.Context, Request) (Decision, error)

// ExecutorGate adapts the permission context to the tool executor's gate contract.
type ExecutorGate struct {
	Context        *Context
	RequestHandler RequestHandler
}

// Check evaluates a pending tool call against policy and an optional interactive handler.
func (g ExecutorGate) Check(ctx context.Context, call tools.PendingCall) (tools.GateResult, error) {
	permissionContext := g.Context
	if permissionContext == nil {
		permissionContext = NewContext()
	}

	decision := permissionContext.Check(call.Tool.Name(), call.Input, call.Tool.Permission())
	switch decision {
	case DecisionAllow:
		return tools.GateResult{Decision: tools.GateAllow}, nil
	case DecisionDeny:
		return tools.GateResult{Decision: tools.GateDeny, Reason: "blocked by permission policy"}, nil
	case DecisionAsk:
		if g.RequestHandler == nil {
			return tools.GateResult{Decision: tools.GateAsk, Reason: "user approval required"}, nil
		}
		resolved, err := g.RequestHandler(ctx, Request{
			ToolName:        call.Tool.Name(),
			Input:           call.Input,
			PermissionLevel: call.Tool.Permission(),
		})
		if err != nil {
			return tools.GateResult{}, fmt.Errorf("resolve permission request: %w", err)
		}
		switch resolved {
		case DecisionAllow:
			return tools.GateResult{Decision: tools.GateAllow}, nil
		case DecisionDeny:
			return tools.GateResult{Decision: tools.GateDeny, Reason: "user denied request"}, nil
		default:
			return tools.GateResult{Decision: tools.GateAsk, Reason: "permission request unresolved"}, nil
		}
	default:
		return tools.GateResult{Decision: tools.GateAsk, Reason: "unknown permission decision"}, nil
	}
}
