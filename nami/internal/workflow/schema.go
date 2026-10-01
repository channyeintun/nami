package workflow

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/jsonschema-go/jsonschema"
)

// compileSchema checks an agent() schema and returns it in canonical form. The
// root has to be an object schema: providers require tool parameters to be an
// object, and the agent returns its output by calling a tool with the schema
// as its parameters.
func compileSchema(raw any) (json.RawMessage, *jsonschema.Resolved, error) {
	fields, ok := raw.(map[string]any)
	if !ok {
		return nil, nil, errors.New("agent() option schema must be a JSON Schema object")
	}
	if fields["type"] != "object" {
		return nil, nil, errors.New("agent() option schema must have type: 'object' at its root")
	}
	properties, ok := fields["properties"].(map[string]any)
	if !ok {
		return nil, nil, errors.New("agent() option schema must declare properties at its root")
	}
	if required, ok := fields["required"]; ok {
		names, ok := required.([]any)
		if !ok {
			return nil, nil, errors.New("agent() option schema required must be an array of property names")
		}
		for _, name := range names {
			text, ok := name.(string)
			if !ok {
				return nil, nil, errors.New("agent() option schema required must be an array of property names")
			}
			if _, declared := properties[text]; !declared {
				return nil, nil, fmt.Errorf("agent() option schema requires %q, which is not one of its properties", text)
			}
		}
	}

	// encoding/json writes map keys in sorted order, so equal schemas encode
	// to equal bytes and key the same journal entries.
	canonical, err := json.Marshal(fields)
	if err != nil {
		return nil, nil, fmt.Errorf("agent() option schema cannot be encoded as JSON: %w", err)
	}
	resolved, err := ResolveSchema(canonical)
	if err != nil {
		return nil, nil, fmt.Errorf("agent() option schema is not a valid JSON Schema: %w", err)
	}
	return canonical, resolved, nil
}

// ResolveSchema prepares a JSON Schema for validation. It refuses references:
// the validator follows $ref without a cycle check, so a schema such as
// {allOf: [{$ref: '#'}]} would recurse until the engine's stack overflows,
// which no recover can catch. Tool parameters rarely need them, and several
// providers handle them poorly anyway.
func ResolveSchema(raw json.RawMessage) (*jsonschema.Resolved, error) {
	var fields any
	if err := json.Unmarshal(raw, &fields); err != nil {
		return nil, err
	}
	if keyword, found := findSchemaReference(fields, 0); found {
		return nil, fmt.Errorf("schema uses %s; write the schema out in full instead of referring to parts of it", keyword)
	}
	var schema jsonschema.Schema
	if err := json.Unmarshal(raw, &schema); err != nil {
		return nil, err
	}
	return schema.Resolve(nil)
}

// referenceKeywords are the JSON Schema keywords that point elsewhere in a
// schema, and the places such targets are kept.
var referenceKeywords = []string{"$ref", "$dynamicRef", "$recursiveRef", "$defs", "definitions", "$anchor", "$dynamicAnchor"}

// maxSchemaDepth bounds how deep findSchemaReference looks. A schema nested
// deeper than this is refused rather than walked.
const maxSchemaDepth = 128

func findSchemaReference(value any, depth int) (string, bool) {
	if depth > maxSchemaDepth {
		return fmt.Sprintf("more than %d levels of nesting", maxSchemaDepth), true
	}
	switch node := value.(type) {
	case map[string]any:
		for _, keyword := range referenceKeywords {
			if _, ok := node[keyword]; ok {
				return keyword, true
			}
		}
		for _, child := range node {
			if keyword, found := findSchemaReference(child, depth+1); found {
				return keyword, true
			}
		}
	case []any:
		for _, child := range node {
			if keyword, found := findSchemaReference(child, depth+1); found {
				return keyword, true
			}
		}
	}
	return "", false
}

// validateStructured checks an agent's output against its call's schema.
func validateStructured(schema *jsonschema.Resolved, output json.RawMessage) error {
	if len(output) == 0 {
		return errors.New("the agent returned no structured output")
	}
	var instance any
	if err := json.Unmarshal(output, &instance); err != nil {
		return fmt.Errorf("structured output is not valid JSON: %w", err)
	}
	return schema.Validate(instance)
}

// ValidateStructured checks an agent's output against a schema, for a runner
// that wants to reject a mismatch while the agent can still retry.
func ValidateStructured(schema *jsonschema.Resolved, output json.RawMessage) error {
	return validateStructured(schema, output)
}
