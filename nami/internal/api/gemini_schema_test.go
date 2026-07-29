package api

import (
	"reflect"
	"testing"
)

func sanitizedMap(t *testing.T, schema map[string]any) map[string]any {
	t.Helper()
	out, ok := sanitizeGeminiSchema(schema).(map[string]any)
	if !ok {
		t.Fatalf("sanitizeGeminiSchema returned %T, want map", sanitizeGeminiSchema(schema))
	}
	return out
}

func TestSanitizeGeminiSchemaStripsUnsupportedKeysRecursively(t *testing.T) {
	out := sanitizedMap(t, map[string]any{
		"$schema":              "https://json-schema.org/draft/2020-12/schema",
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"path": map[string]any{
				"type":                 "string",
				"default":              "x",
				"additionalProperties": false,
			},
		},
	})

	for _, key := range []string{"$schema", "additionalProperties"} {
		if _, present := out[key]; present {
			t.Fatalf("%q survived sanitization: %+v", key, out)
		}
	}
	// Stripping has to reach nested property schemas, not just the top level.
	nested := out["properties"].(map[string]any)["path"].(map[string]any)
	if _, present := nested["default"]; present {
		t.Fatalf("nested default survived: %+v", nested)
	}
	if _, present := nested["additionalProperties"]; present {
		t.Fatalf("nested additionalProperties survived: %+v", nested)
	}
}

func TestSanitizeGeminiSchemaCollapsesAnyOfToOneRequiredField(t *testing.T) {
	// Gemini rejects anyOf, so a "one of these fields" constraint collapses to a
	// single required field chosen by naming convention.
	out := sanitizedMap(t, map[string]any{
		"type": "object",
		"properties": map[string]any{
			"path":     map[string]any{"type": "string"},
			"filePath": map[string]any{"type": "string"},
		},
		"anyOf": []any{
			map[string]any{"required": []any{"filePath"}},
			map[string]any{"required": []any{"path"}},
		},
	})

	if _, present := out["anyOf"]; present {
		t.Fatalf("anyOf survived: %+v", out)
	}
	// "path" is plain lower-alphanumeric (rank 0) and beats lowerCamelCase.
	if got := out["required"]; !reflect.DeepEqual(got, []string{"path"}) {
		t.Fatalf("required = %#v, want [path]", got)
	}
}

func TestSanitizeGeminiSchemaUnionsAllOfRequiredFields(t *testing.T) {
	out := sanitizedMap(t, map[string]any{
		"type": "object",
		"properties": map[string]any{
			"a": map[string]any{"type": "string"},
			"b": map[string]any{"type": "string"},
		},
		"allOf": []any{
			map[string]any{"required": []any{"a"}},
			map[string]any{"required": []any{"b"}},
		},
	})

	if _, present := out["allOf"]; present {
		t.Fatalf("allOf survived: %+v", out)
	}
	// allOf means every branch applies, so both fields stay required.
	got, _ := out["required"].([]string)
	if len(got) != 2 {
		t.Fatalf("required = %#v, want both fields", out["required"])
	}
}

func TestSanitizeGeminiSchemaDropsRequiredNamesWithoutProperties(t *testing.T) {
	out := sanitizedMap(t, map[string]any{
		"type":       "object",
		"properties": map[string]any{"real": map[string]any{"type": "string"}},
		"required":   []any{"real", "ghost"},
	})

	if got := out["required"]; !reflect.DeepEqual(got, []string{"real"}) {
		t.Fatalf("required = %#v, want only declared properties", got)
	}
}

func TestSanitizeGeminiSchemaDropsPropertiesOnNonObjectTypes(t *testing.T) {
	out := sanitizedMap(t, map[string]any{
		"type":       "string",
		"properties": map[string]any{"nope": map[string]any{"type": "string"}},
		"required":   []any{"nope"},
	})

	if _, present := out["properties"]; present {
		t.Fatalf("properties survived on a string schema: %+v", out)
	}
	if _, present := out["required"]; present {
		t.Fatalf("required survived on a string schema: %+v", out)
	}
}

func TestGeminiFieldRankPrefersSimplerNaming(t *testing.T) {
	ranks := map[string]int{
		"path":     0, // lower alphanumeric
		"file_dir": 1, // snake_case
		"filePath": 2, // lowerCamelCase
		"FilePath": 3, // UpperCamelCase
		"file-dir": 4, // anything else
	}
	for name, want := range ranks {
		if got := geminiFieldRank(name); got != want {
			t.Errorf("geminiFieldRank(%q) = %d, want %d", name, got, want)
		}
	}
}

func TestPickGeminiPreferredFieldIgnoresUndeclaredCandidates(t *testing.T) {
	properties := map[string]any{"filePath": map[string]any{}}
	// "path" ranks better but is not a declared property, so it cannot win.
	if got := pickGeminiPreferredField([]string{"path", "filePath"}, properties); got != "filePath" {
		t.Fatalf("pickGeminiPreferredField = %q, want filePath", got)
	}
}

func TestAppendUniqueStringsSkipsDuplicatesAndBlanks(t *testing.T) {
	got := appendUniqueStrings([]string{"a"}, "a", "", "b", "b")
	if !reflect.DeepEqual(got, []string{"a", "b"}) {
		t.Fatalf("appendUniqueStrings = %#v", got)
	}
}
