package api

// sanitizeOpenAIToolSchema normalizes tool schemas to the subset accepted by
// OpenAI-style function calling endpoints, which require a top-level object
// schema and reject root combinators such as anyOf/allOf/oneOf/not/enum.
func sanitizeOpenAIToolSchema(schema any) any {
	sanitized := sanitizeGeminiSchema(schema)
	schemaMap, ok := sanitized.(map[string]any)
	if !ok {
		return map[string]any{
			"type":       "object",
			"properties": map[string]any{},
		}
	}

	result := make(map[string]any, len(schemaMap))
	for key, value := range schemaMap {
		result[key] = value
	}

	delete(result, "oneOf")
	delete(result, "anyOf")
	delete(result, "allOf")
	delete(result, "not")
	delete(result, "enum")

	properties, _ := result["properties"].(map[string]any)
	if properties == nil {
		properties = map[string]any{}
		result["properties"] = properties
	}

	required := filterRequiredProperties(schemaStringValues(result["required"]), properties)
	if len(required) > 0 {
		result["required"] = required
	} else {
		delete(result, "required")
	}

	result["type"] = "object"
	return result
}
