package tools

import "testing"

func TestFirstStringParamUsesPreferenceOrder(t *testing.T) {
	params := map[string]any{"path": "from-path", "file_path": "from-file-path"}
	if got, ok := firstStringParam(params, "file_path", "path"); !ok || got != "from-file-path" {
		t.Fatalf("firstStringParam = %q ok=%v, want the first listed alias", got, ok)
	}
	if got, ok := firstStringParam(params, "path", "file_path"); !ok || got != "from-path" {
		t.Fatalf("firstStringParam = %q ok=%v, want the reordered alias", got, ok)
	}
}

func TestFirstStringParamSkipsBlankAndWrongTypes(t *testing.T) {
	// A key present but blank must fall through to the next alias, or a tool
	// silently receives an empty path.
	params := map[string]any{"path": "   ", "file_path": "real.go"}
	if got, ok := firstStringParam(params, "path", "file_path"); !ok || got != "real.go" {
		t.Fatalf("firstStringParam = %q ok=%v, want real.go", got, ok)
	}

	// Non-string values are not coerced.
	if _, ok := firstStringParam(map[string]any{"path": 42}, "path"); ok {
		t.Fatal("firstStringParam accepted a non-string value")
	}
	if _, ok := firstStringParam(map[string]any{}, "path"); ok {
		t.Fatal("firstStringParam succeeded on an empty map")
	}
}

func TestFirstIntParamCoercesJSONNumbers(t *testing.T) {
	// Tool params arrive from JSON, so integers surface as float64.
	cases := map[string]any{"float": float64(12), "int": 12, "int64": int64(12), "string": "12"}
	for name, value := range cases {
		got, ok := firstIntParam(map[string]any{"limit": value}, "limit")
		if !ok || got != 12 {
			t.Errorf("%s: firstIntParam = %d ok=%v, want 12", name, got, ok)
		}
	}

	if _, ok := firstIntParam(map[string]any{"limit": "abc"}, "limit"); ok {
		t.Error("firstIntParam accepted a non-numeric string")
	}
}

func TestFirstIntParamFallsThroughToNextAlias(t *testing.T) {
	params := map[string]any{"offset": "not a number", "start_line": 7}
	if got, ok := firstIntParam(params, "offset", "start_line"); !ok || got != 7 {
		t.Fatalf("firstIntParam = %d ok=%v, want the usable alias", got, ok)
	}
}

func TestFirstBoolParamAcceptsBoolsAndStrings(t *testing.T) {
	truthy := []any{true, "true", "TRUE", "True"}
	for _, value := range truthy {
		if !firstBoolParam(map[string]any{"background": value}, "background") {
			t.Errorf("firstBoolParam(%#v) = false, want true", value)
		}
	}

	falsy := []any{false, "false", "yes", "1", 1, nil}
	for _, value := range falsy {
		if firstBoolParam(map[string]any{"background": value}, "background") {
			t.Errorf("firstBoolParam(%#v) = true, want false", value)
		}
	}

	if firstBoolParam(map[string]any{}, "background") {
		t.Error("firstBoolParam on a missing key = true, want false")
	}
}

func TestFirstBoolParamStopsAtFirstPresentKey(t *testing.T) {
	// The first present alias decides, even when it is false and a later alias
	// is true; otherwise an explicit false could be overridden.
	params := map[string]any{"background": false, "run_in_background": true}
	if firstBoolParam(params, "background", "run_in_background") {
		t.Fatal("firstBoolParam ignored an explicit false on the preferred key")
	}
}

func TestFirstParamReturnsRawValue(t *testing.T) {
	params := map[string]any{"b": []any{1, 2}}
	value, ok := firstParam(params, "a", "b")
	if !ok {
		t.Fatal("firstParam did not find the second alias")
	}
	if _, isSlice := value.([]any); !isSlice {
		t.Fatalf("firstParam = %#v, want the raw slice", value)
	}
	if _, ok := firstParam(params, "missing"); ok {
		t.Fatal("firstParam succeeded on a missing key")
	}
}
