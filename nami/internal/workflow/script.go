// Package workflow runs workflow scripts: JavaScript programs that spawn child
// agents with agent(), fan work out with parallel() and pipeline(), and return
// a value.
//
// A script runs in an embedded goja VM that can reach only the hooks this
// package injects. It has no filesystem, network, or process access, so every
// side effect goes through the caller's AgentRunner and inherits whatever
// controls the caller applies to its agents. The package itself knows nothing
// about agents beyond that function.
package workflow

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/dop251/goja"
	"github.com/dop251/goja/ast"
	"github.com/dop251/goja/token"
)

// Script is a parsed workflow script, ready to run.
type Script struct {
	Meta   Meta
	Source string
	// program evaluates to the async function that holds the script body. The
	// hooks are that function's parameters, so a nested workflow gets its own
	// phase, args, and agent bindings instead of sharing globals.
	program *goja.Program
}

// Meta is the script's leading `export const meta = {...}` literal. It is
// read without running the script, which is why it has to be a pure literal.
type Meta struct {
	Name        string      `json:"name"`
	Description string      `json:"description"`
	WhenToUse   string      `json:"when_to_use,omitempty"`
	Phases      []PhaseMeta `json:"phases,omitempty"`
}

// PhaseMeta describes one phase() group for progress display.
type PhaseMeta struct {
	Title  string `json:"title"`
	Detail string `json:"detail,omitempty"`
	Model  string `json:"model,omitempty"`
}

// bodyPrefix and bodySuffix wrap a script body in an async function, which is
// what allows top-level await and a top-level return. The prefix sits on the
// body's first line so error line numbers match the script as written.
const (
	bodyPrefix = `(async (agent, parallel, pipeline, phase, log, workflow, args, budget) => {`
	bodySuffix = "\n})"
)

var (
	metaExportPattern = regexp.MustCompile(`^export\s+const\s+meta\s*=`)
	metaNamePattern   = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)
)

// ParseScript checks a script's syntax and reads its meta. filename only
// labels error messages.
func ParseScript(filename string, source string) (Script, error) {
	if strings.TrimSpace(source) == "" {
		return Script{}, fmt.Errorf("workflow script is empty")
	}
	offset := leadingCodeOffset(source)
	if !metaExportPattern.MatchString(source[offset:]) {
		return Script{}, fmt.Errorf("workflow script must begin with `export const meta = {...}`")
	}
	// goja runs scripts, not modules, so the export keyword cannot stay. Spaces
	// keep every later column where it was.
	body := source[:offset] + strings.Repeat(" ", len("export")) + source[offset+len("export"):]

	parsed, err := goja.Parse(filename, bodyPrefix+body+bodySuffix)
	if err != nil {
		return Script{}, fmt.Errorf("workflow script has a syntax error: %w", err)
	}
	literal, err := metaLiteral(parsed)
	if err != nil {
		return Script{}, err
	}
	meta, err := metaFromLiteral(literal)
	if err != nil {
		return Script{}, err
	}
	program, err := goja.CompileAST(parsed, true)
	if err != nil {
		return Script{}, fmt.Errorf("workflow script does not compile: %w", err)
	}
	return Script{Meta: meta, Source: source, program: program}, nil
}

// leadingCodeOffset skips whitespace and comments so a script may open with a
// license header or a note before its meta.
func leadingCodeOffset(source string) int {
	offset := 0
	for offset < len(source) {
		rest := source[offset:]
		trimmed := strings.TrimLeft(rest, " \t\r\n")
		offset += len(rest) - len(trimmed)
		switch {
		case strings.HasPrefix(trimmed, "//"):
			end := strings.IndexByte(trimmed, '\n')
			if end < 0 {
				return len(source)
			}
			offset += end + 1
		case strings.HasPrefix(trimmed, "/*"):
			end := strings.Index(trimmed, "*/")
			if end < 0 {
				return len(source)
			}
			offset += end + len("*/")
		default:
			return offset
		}
	}
	return offset
}

// metaLiteral finds the object literal assigned to meta in the wrapped body.
func metaLiteral(program *ast.Program) (ast.Expression, error) {
	notFound := fmt.Errorf("workflow script must begin with `export const meta = {...}`")
	if len(program.Body) == 0 {
		return nil, notFound
	}
	statement, ok := program.Body[0].(*ast.ExpressionStatement)
	if !ok {
		return nil, notFound
	}
	wrapper, ok := statement.Expression.(*ast.ArrowFunctionLiteral)
	if !ok {
		return nil, notFound
	}
	block, ok := wrapper.Body.(*ast.BlockStatement)
	if !ok || len(block.List) == 0 {
		return nil, notFound
	}
	declaration, ok := block.List[0].(*ast.LexicalDeclaration)
	if !ok || declaration.Token != token.CONST || len(declaration.List) != 1 {
		return nil, notFound
	}
	binding := declaration.List[0]
	if target, ok := binding.Target.(*ast.Identifier); !ok || target.Name.String() != "meta" {
		return nil, notFound
	}
	if _, ok := binding.Initializer.(*ast.ObjectLiteral); !ok {
		return nil, fmt.Errorf("workflow meta must be an object literal")
	}
	return binding.Initializer, nil
}

// literalValue converts a pure-literal expression to Go values. Anything that
// would need the script to run — a variable, a call, a spread, an
// interpolated template — is rejected, because meta is read without running
// the script.
func literalValue(expression ast.Expression) (any, error) {
	switch node := expression.(type) {
	case *ast.StringLiteral:
		return node.Value.String(), nil
	case *ast.NumberLiteral:
		return node.Value, nil
	case *ast.BooleanLiteral:
		return node.Value, nil
	case *ast.NullLiteral:
		return nil, nil
	case *ast.TemplateLiteral:
		if node.Tag != nil || len(node.Expressions) > 0 || len(node.Elements) != 1 {
			return nil, fmt.Errorf("workflow meta must be a pure literal: template strings cannot interpolate")
		}
		return node.Elements[0].Parsed.String(), nil
	case *ast.UnaryExpression:
		number, ok := node.Operand.(*ast.NumberLiteral)
		if node.Operator != token.MINUS || node.Postfix || !ok {
			return nil, fmt.Errorf("workflow meta must be a pure literal: only negative numbers may use an operator")
		}
		switch value := number.Value.(type) {
		case int64:
			return -value, nil
		case float64:
			return -value, nil
		}
		return nil, fmt.Errorf("workflow meta must be a pure literal: unsupported number %s", number.Literal)
	case *ast.ArrayLiteral:
		values := make([]any, 0, len(node.Value))
		for _, item := range node.Value {
			if item == nil {
				return nil, fmt.Errorf("workflow meta must be a pure literal: arrays cannot have holes")
			}
			value, err := literalValue(item)
			if err != nil {
				return nil, err
			}
			values = append(values, value)
		}
		return values, nil
	case *ast.ObjectLiteral:
		values := make(map[string]any, len(node.Value))
		for _, property := range node.Value {
			keyed, ok := property.(*ast.PropertyKeyed)
			if !ok || keyed.Computed || keyed.Kind != ast.PropertyKindValue {
				return nil, fmt.Errorf("workflow meta must be a pure literal: use plain `key: value` properties")
			}
			key, err := literalKey(keyed.Key)
			if err != nil {
				return nil, err
			}
			value, err := literalValue(keyed.Value)
			if err != nil {
				return nil, err
			}
			values[key] = value
		}
		return values, nil
	default:
		return nil, fmt.Errorf("workflow meta must be a pure literal: variables, calls, and spreads are not allowed")
	}
}

func literalKey(expression ast.Expression) (string, error) {
	switch key := expression.(type) {
	case *ast.Identifier:
		return key.Name.String(), nil
	case *ast.StringLiteral:
		return key.Value.String(), nil
	default:
		return "", fmt.Errorf("workflow meta must be a pure literal: property names must be identifiers or strings")
	}
}

func metaFromLiteral(expression ast.Expression) (Meta, error) {
	raw, err := literalValue(expression)
	if err != nil {
		return Meta{}, err
	}
	fields := raw.(map[string]any)

	var meta Meta
	var problems []string
	for key, value := range fields {
		switch key {
		case "name":
			meta.Name = stringField(value, "meta.name", &problems)
		case "description":
			meta.Description = stringField(value, "meta.description", &problems)
		case "whenToUse":
			meta.WhenToUse = stringField(value, "meta.whenToUse", &problems)
		case "phases":
			meta.Phases = phasesField(value, &problems)
		default:
			problems = append(problems, fmt.Sprintf("meta.%s is not a known field; use name, description, whenToUse, or phases", key))
		}
	}
	switch {
	case meta.Name == "":
		problems = append(problems, "meta.name is required")
	case !metaNamePattern.MatchString(meta.Name):
		problems = append(problems, fmt.Sprintf("meta.name %q must be 1-64 letters, digits, '.', '_' or '-', starting with a letter or digit", meta.Name))
	}
	if meta.Description == "" {
		problems = append(problems, "meta.description is required")
	}
	if len(problems) > 0 {
		return Meta{}, fmt.Errorf("invalid workflow meta: %s", strings.Join(problems, "; "))
	}
	return meta, nil
}

func stringField(value any, label string, problems *[]string) string {
	text, ok := value.(string)
	if !ok {
		*problems = append(*problems, label+" must be a string")
		return ""
	}
	return strings.TrimSpace(text)
}

func phasesField(value any, problems *[]string) []PhaseMeta {
	items, ok := value.([]any)
	if !ok {
		*problems = append(*problems, "meta.phases must be an array")
		return nil
	}
	phases := make([]PhaseMeta, 0, len(items))
	for i, item := range items {
		fields, ok := item.(map[string]any)
		if !ok {
			*problems = append(*problems, fmt.Sprintf("meta.phases[%d] must be an object", i))
			continue
		}
		var phase PhaseMeta
		for key, value := range fields {
			label := fmt.Sprintf("meta.phases[%d].%s", i, key)
			switch key {
			case "title":
				phase.Title = stringField(value, label, problems)
			case "detail":
				phase.Detail = stringField(value, label, problems)
			case "model":
				phase.Model = stringField(value, label, problems)
			default:
				*problems = append(*problems, label+" is not a known field; use title, detail, or model")
			}
		}
		if phase.Title == "" {
			*problems = append(*problems, fmt.Sprintf("meta.phases[%d].title is required", i))
		}
		phases = append(phases, phase)
	}
	return phases
}
