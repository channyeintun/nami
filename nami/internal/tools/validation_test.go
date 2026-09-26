package tools

import "testing"

// Required strings must be non-blank unless the property declares its own
// minLength, which is how replacement text and file content opt in to being
// empty or whitespace-only.
func TestValidateRequiredStringLength(t *testing.T) {
	cases := []struct {
		name     string
		property map[string]any
		value    string
		wantErr  bool
	}{
		{"text without minLength", map[string]any{"type": "string"}, "x", false},
		{"blank without minLength", map[string]any{"type": "string"}, "  ", true},
		{"empty with minLength 0", map[string]any{"type": "string", "minLength": 0}, "", false},
		{"whitespace with minLength 1", map[string]any{"type": "string", "minLength": 1}, "\n\n", false},
		{"empty with minLength 1", map[string]any{"type": "string", "minLength": 1}, "", true},
		// Schemas decoded from JSON, such as MCP tool schemas, carry float64.
		{"empty with JSON minLength", map[string]any{"type": "string", "minLength": float64(1)}, "", true},
		// minLength counts characters, not bytes.
		{"multibyte character under minLength", map[string]any{"type": "string", "minLength": 2}, "é", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			schema := map[string]any{
				"type":       "object",
				"properties": map[string]any{"value": tc.property},
				"required":   []string{"value"},
			}
			err := validateRequiredParams("tool", map[string]any{"value": tc.value}, schema)
			if (err != nil) != tc.wantErr {
				t.Fatalf("validateRequiredParams(%q) error = %v, wantErr %v", tc.value, err, tc.wantErr)
			}
		})
	}
}
