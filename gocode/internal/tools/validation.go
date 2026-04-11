package tools

import (
	"fmt"
	"reflect"
	"strings"
)

// ValidateToolCall performs schema-backed validation plus any optional
// tool-specific semantic validation before permission resolution.
func ValidateToolCall(tool Tool, input ToolInput) error {
	if tool == nil {
		return fmt.Errorf("tool is required")
	}
	if err := validateRequiredParams(tool.Name(), input.Params, tool.InputSchema()); err != nil {
		return err
	}
	if validator, ok := tool.(SemanticValidator); ok {
		if err := validator.Validate(input); err != nil {
			return err
		}
	}
	return nil
}

func validateRequiredParams(toolName string, params map[string]any, schema any) error {
	schemaMap, ok := schema.(map[string]any)
	if !ok {
		return nil
	}
	properties, _ := schemaMap["properties"].(map[string]any)
	required, _ := schemaMap["required"].([]string)
	if len(required) == 0 {
		if requiredAny, ok := schemaMap["required"].([]any); ok {
			required = make([]string, 0, len(requiredAny))
			for _, value := range requiredAny {
				name, ok := value.(string)
				if ok {
					required = append(required, name)
				}
			}
		}
	}

	for _, name := range required {
		value, exists := params[name]
		if !exists {
			return fmt.Errorf("%s requires %s", toolName, name)
		}
		propertySchema, _ := properties[name].(map[string]any)
		if err := validatePropertyValue(toolName, name, value, propertySchema, true); err != nil {
			return err
		}
	}

	for name, value := range params {
		propertySchema, ok := properties[name].(map[string]any)
		if !ok {
			continue
		}
		if err := validatePropertyValue(toolName, name, value, propertySchema, false); err != nil {
			return err
		}
	}

	return nil
}

func validatePropertyValue(toolName, propertyName string, value any, schema map[string]any, required bool) error {
	typeName, _ := schema["type"].(string)
	switch typeName {
	case "string":
		stringValue, ok := value.(string)
		if !ok {
			return fmt.Errorf("%s %s must be a string", toolName, propertyName)
		}
		if required && strings.TrimSpace(stringValue) == "" {
			return fmt.Errorf("%s requires %s", toolName, propertyName)
		}
	case "array":
		if reflect.TypeOf(value) == nil || reflect.TypeOf(value).Kind() != reflect.Slice {
			return fmt.Errorf("%s %s must be an array", toolName, propertyName)
		}
		if required && reflect.ValueOf(value).Len() == 0 {
			return fmt.Errorf("%s requires a non-empty %s", toolName, propertyName)
		}
	case "integer":
		switch value.(type) {
		case int, int8, int16, int32, int64, float64, float32:
		default:
			return fmt.Errorf("%s %s must be an integer", toolName, propertyName)
		}
	case "boolean":
		if _, ok := value.(bool); !ok {
			return fmt.Errorf("%s %s must be a boolean", toolName, propertyName)
		}
	}
	return nil
}
