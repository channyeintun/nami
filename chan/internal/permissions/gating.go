package permissions

import (
	"path/filepath"
	"regexp"
	"strings"

	"github.com/channyeintun/gocode/internal/tools"
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
	SessionAllowAll  bool // auto-approve read-only tools and non-destructive bash commands for this session
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

type RiskAssessment struct {
	Level                   string
	Reason                  string
	DisallowPersistentAllow bool
}

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

	if isReadOnlySubagentLaunch(toolName, input) {
		return DecisionAllow
	}

	// Session-wide allow-all: approve non-destructive commands
	if c.SessionAllowAll {
		if permLevel == tools.PermissionReadOnly {
			return DecisionAllow
		}
		if toolName == "bash" {
			command := ""
			if params, ok := input.Params["command"]; ok {
				command, _ = params.(string)
			}
			if CheckDestructive(command) == "" {
				return DecisionAllow
			}
			// destructive bash commands still require explicit approval
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

// AssessRisk returns a coarse risk label and policy notes for a pending tool call.
func AssessRisk(toolName string, input tools.ToolInput, permLevel tools.PermissionLevel) RiskAssessment {
	if isReadOnlySubagentLaunch(toolName, input) {
		return RiskAssessment{Level: "read"}
	}

	if toolName == "bash" {
		command := ""
		if params, ok := input.Params["command"]; ok {
			command, _ = params.(string)
		}
		if warning := CheckDestructive(command); warning != "" {
			return RiskAssessment{Level: "destructive", Reason: warning}
		}
		return RiskAssessment{Level: "execute"}
	}

	if permLevel != tools.PermissionWrite {
		switch permLevel {
		case tools.PermissionExecute:
			return RiskAssessment{Level: "execute"}
		default:
			return RiskAssessment{Level: "read"}
		}
	}

	if toolName == "apply_patch" {
		patchText, ok := firstStringParam(input.Params, "patch")
		if !ok || strings.TrimSpace(patchText) == "" {
			return RiskAssessment{Level: "write"}
		}
		targets, err := tools.ExtractApplyPatchTargets(patchText)
		if err != nil || len(targets) == 0 {
			return RiskAssessment{Level: "write"}
		}
		for _, target := range targets {
			assessment := assessSensitiveFilePath(target)
			if assessment.Level == "high" {
				assessment.Reason = "patch touches sensitive files and requires explicit approval every time"
				return assessment
			}
		}
		return RiskAssessment{Level: "write"}
	}

	filePath, ok := firstStringParam(input.Params,
		"file_path",
		"target_file",
		"TargetFile",
	)
	if !ok || strings.TrimSpace(filePath) == "" {
		return RiskAssessment{Level: "write"}
	}

	assessment := assessSensitiveFilePath(filePath)
	if assessment.Level != "" {
		return assessment
	}
	return RiskAssessment{Level: "write"}
}

func assessSensitiveFilePath(filePath string) RiskAssessment {
	cleanPath := filepath.Clean(strings.TrimSpace(filePath))
	base := strings.ToLower(filepath.Base(cleanPath))
	components := splitPathComponents(cleanPath)

	for _, component := range components {
		switch strings.ToLower(component) {
		case ".git":
			return RiskAssessment{Level: "high", Reason: "editing repository metadata inside .git requires explicit approval every time", DisallowPersistentAllow: true}
		case ".vscode":
			if strings.HasSuffix(base, ".json") {
				return RiskAssessment{Level: "high", Reason: "editing editor workspace settings requires explicit approval every time", DisallowPersistentAllow: true}
			}
		}
	}

	if strings.HasPrefix(base, ".env") {
		return RiskAssessment{Level: "high", Reason: "editing environment or secret-bearing files requires explicit approval every time", DisallowPersistentAllow: true}
	}

	sensitiveDotfiles := map[string]string{
		".gitignore":       "editing repository behavior files requires explicit approval every time",
		".gitattributes":   "editing repository behavior files requires explicit approval every time",
		".editorconfig":    "editing shared formatting policy files requires explicit approval every time",
		".npmrc":           "editing package manager config files requires explicit approval every time",
		".yarnrc":          "editing package manager config files requires explicit approval every time",
		".yarnrc.yml":      "editing package manager config files requires explicit approval every time",
		".pnp.cjs":         "editing package manager config files requires explicit approval every time",
		".prettierrc":      "editing shared formatting policy files requires explicit approval every time",
		".prettierrc.json": "editing shared formatting policy files requires explicit approval every time",
		".prettierrc.yml":  "editing shared formatting policy files requires explicit approval every time",
		".prettierrc.yaml": "editing shared formatting policy files requires explicit approval every time",
		".eslintrc":        "editing shared linting policy files requires explicit approval every time",
		".eslintrc.json":   "editing shared linting policy files requires explicit approval every time",
		".eslintrc.yml":    "editing shared linting policy files requires explicit approval every time",
		".eslintrc.yaml":   "editing shared linting policy files requires explicit approval every time",
	}
	if reason, ok := sensitiveDotfiles[base]; ok {
		return RiskAssessment{Level: "high", Reason: reason, DisallowPersistentAllow: true}
	}

	if base == "go.sum" || base == "package-lock.json" || base == "pnpm-lock.yaml" || base == "bun.lockb" || base == "bun.lock" || base == "cargo.lock" {
		return RiskAssessment{Level: "high", Reason: "editing lockfiles requires explicit approval every time", DisallowPersistentAllow: true}
	}

	return RiskAssessment{}
}

func splitPathComponents(filePath string) []string {
	path := filepath.ToSlash(strings.TrimSpace(filePath))
	if path == "" {
		return nil
	}
	parts := strings.Split(path, "/")
	components := make([]string, 0, len(parts))
	for _, part := range parts {
		if part == "" || part == "." {
			continue
		}
		components = append(components, part)
	}
	return components
}

func firstStringParam(params map[string]any, keys ...string) (string, bool) {
	for _, key := range keys {
		value, ok := params[key]
		if !ok {
			continue
		}
		stringValue, ok := value.(string)
		if ok && strings.TrimSpace(stringValue) != "" {
			return stringValue, true
		}
	}
	return "", false
}

func isReadOnlySubagentLaunch(toolName string, input tools.ToolInput) bool {
	if toolName != "agent" {
		return false
	}
	subagentType, ok := firstStringParam(input.Params, "subagent_type")
	if !ok {
		subagentType = "explore"
	}
	subagentType = strings.TrimSpace(subagentType)
	switch subagentType {
	case "", "explore", "search":
		return true
	default:
		return false
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
