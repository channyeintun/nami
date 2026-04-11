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
	return validateObjectSchema(toolName, "", params, schemaMap)
}

func validateObjectSchema(toolName, path string, params map[string]any, schema map[string]any) error {
	properties, _ := schema["properties"].(map[string]any)
	for _, name := range schemaRequiredFields(schema["required"]) {
		value, exists := params[name]
		if !exists {
			return missingFieldError(toolName, path, name)
		}
		propertySchema, _ := properties[name].(map[string]any)
		if err := validatePropertyValue(toolName, joinSchemaPath(path, name), value, propertySchema, true); err != nil {
			return err
		}
	}

	if err := validateCompositeObjectSchema(toolName, path, params, schema); err != nil {
		return err
	}

	for name, value := range params {
		propertySchema, ok := properties[name].(map[string]any)
		if !ok {
			continue
		}
		if err := validatePropertyValue(toolName, joinSchemaPath(path, name), value, propertySchema, false); err != nil {
			return err
		}
	}

	return nil
}

func validateCompositeObjectSchema(toolName, path string, params map[string]any, schema map[string]any) error {
	for _, subschema := range schemaChildren(schema["allOf"]) {
		if err := validateObjectSchema(toolName, path, params, subschema); err != nil {
			return err
		}
	}

	anyOfSchemas := schemaChildren(schema["anyOf"])
	if len(anyOfSchemas) == 0 {
		return nil
	}
	for _, subschema := range anyOfSchemas {
		if err := validateObjectSchema(toolName, path, params, subschema); err == nil {
			return nil
		}
	}

	options := anyOfRequiredOptions(path, anyOfSchemas)
	if len(options) > 0 {
		return fmt.Errorf("%s requires one of %s", toolName, strings.Join(options, ", "))
	}

	return fmt.Errorf("%s %s does not satisfy any allowed schema variant", toolName, displaySchemaPath(path))
}

func validatePropertyValue(toolName, propertyName string, value any, schema map[string]any, required bool) error {
	if len(schema) == 0 {
		return nil
	}

	if needsObjectValidation(schema) {
		params, ok := value.(map[string]any)
		if !ok {
			return fmt.Errorf("%s %s must be an object", toolName, propertyName)
		}
		if err := validateObjectSchema(toolName, propertyName, params, schema); err != nil {
			return err
		}
	}

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
		arrayValue := reflect.ValueOf(value)
		if required && arrayValue.Len() == 0 {
			return fmt.Errorf("%s requires a non-empty %s", toolName, propertyName)
		}
		itemSchema, _ := schema["items"].(map[string]any)
		for index := 0; index < arrayValue.Len() && len(itemSchema) > 0; index++ {
			if err := validatePropertyValue(toolName, fmt.Sprintf("%s[%d]", propertyName, index), arrayValue.Index(index).Interface(), itemSchema, false); err != nil {
				return err
			}
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

func schemaRequiredFields(value any) []string {
	required, _ := value.([]string)
	if len(required) > 0 {
		return required
	}
	requiredAny, ok := value.([]any)
	if !ok {
		return nil
	}
	required = make([]string, 0, len(requiredAny))
	for _, entry := range requiredAny {
		name, ok := entry.(string)
		if ok {
			required = append(required, name)
		}
	}
	return required
}

func schemaChildren(value any) []map[string]any {
	children, _ := value.([]map[string]any)
	if len(children) > 0 {
		return children
	}
	values, ok := value.([]any)
	if !ok {
		return nil
	}
	children = make([]map[string]any, 0, len(values))
	for _, entry := range values {
		child, ok := entry.(map[string]any)
		if ok {
			children = append(children, child)
		}
	}
	return children
}

func anyOfRequiredOptions(path string, schemas []map[string]any) []string {
	options := make([]string, 0, len(schemas))
	for _, schema := range schemas {
		required := schemaRequiredFields(schema["required"])
		if len(required) != 1 {
			return nil
		}
		options = append(options, joinSchemaPath(path, required[0]))
	}
	return options
}

func needsObjectValidation(schema map[string]any) bool {
	if typeName, _ := schema["type"].(string); typeName == "object" {
		return true
	}
	if len(schemaRequiredFields(schema["required"])) > 0 {
		return true
	}
	if properties, ok := schema["properties"].(map[string]any); ok && len(properties) > 0 {
		return true
	}
	if len(schemaChildren(schema["allOf"])) > 0 || len(schemaChildren(schema["anyOf"])) > 0 {
		return true
	}
	return false
}

func joinSchemaPath(parent, child string) string {
	if parent == "" {
		return child
	}
	return parent + "." + child
}

func displaySchemaPath(path string) string {
	if path == "" {
		return "input"
	}
	return path
}

func missingFieldError(toolName, path, propertyName string) error {
	if path == "" {
		return fmt.Errorf("%s requires %s", toolName, propertyName)
	}
	return fmt.Errorf("%s requires %s", toolName, joinSchemaPath(path, propertyName))
}
