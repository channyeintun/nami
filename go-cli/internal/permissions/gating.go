package permissions

import (
	"regexp"

	"github.com/channyeintun/go-cli/internal/tools"
)

// Mode controls the overall permission posture.
type Mode string

const (
	ModeDefault           Mode = "default"           // ask for writes/executes
	ModeBypassPermissions Mode = "bypassPermissions" // auto-approve everything
	ModeAutoApprove       Mode = "autoApprove"       // auto-approve with logging
)

// Rule is a pattern-based permission rule.
type Rule struct {
	Pattern  *regexp.Regexp
	ToolName string // optional: restrict to specific tool
}

// Context holds the current permission state.
type Context struct {
	Mode             Mode
	AlwaysAllowRules []Rule
	AlwaysDenyRules  []Rule
	AlwaysAskRules   []Rule
}

// NewContext creates a default permission context.
func NewContext() *Context {
	return &Context{
		Mode: ModeDefault,
	}
}

// Decision is the outcome of a permission check.
type Decision int

const (
	DecisionAllow Decision = iota
	DecisionDeny
	DecisionAsk
)

// Check evaluates whether a tool call should be allowed, denied, or requires user approval.
func (c *Context) Check(toolName string, input tools.ToolInput, permLevel tools.PermissionLevel) Decision {
	if c.Mode == ModeBypassPermissions {
		return DecisionAllow
	}
	if c.Mode == ModeAutoApprove {
		return DecisionAllow
	}

	// Check deny rules first
	for _, rule := range c.AlwaysDenyRules {
		if rule.ToolName != "" && rule.ToolName != toolName {
			continue
		}
		if rule.Pattern.MatchString(input.Raw) {
			return DecisionDeny
		}
	}

	// Check allow rules
	for _, rule := range c.AlwaysAllowRules {
		if rule.ToolName != "" && rule.ToolName != toolName {
			continue
		}
		if rule.Pattern.MatchString(input.Raw) {
			return DecisionAllow
		}
	}

	// Check ask rules
	for _, rule := range c.AlwaysAskRules {
		if rule.ToolName != "" && rule.ToolName != toolName {
			continue
		}
		if rule.Pattern.MatchString(input.Raw) {
			return DecisionAsk
		}
	}

	// Default: auto-approve reads, ask for writes/executes
	switch permLevel {
	case tools.PermissionReadOnly:
		return DecisionAllow
	default:
		return DecisionAsk
	}
}

// AddAlwaysAllow registers a permanent allow rule.
func (c *Context) AddAlwaysAllow(toolName, pattern string) error {
	re, err := regexp.Compile(pattern)
	if err != nil {
		return err
	}
	c.AlwaysAllowRules = append(c.AlwaysAllowRules, Rule{Pattern: re, ToolName: toolName})
	return nil
}
