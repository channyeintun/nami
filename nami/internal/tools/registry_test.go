package tools

import (
	"slices"
	"testing"
)

// Tool definitions form the start of every model request. OpenAI-compatible,
// Gemini, and Ollama requests keep them in the order given, and those
// providers cache prompts by exact prefix, so an order that changed from
// request to request, as map iteration does, missed the cache every time.
func TestRegistryDefinitionsComeInAStableOrder(t *testing.T) {
	registry := NewRegistry()

	first := registry.Definitions()
	names := make([]string, 0, len(first))
	for _, definition := range first {
		names = append(names, definition.Name)
	}
	if !slices.IsSorted(names) {
		t.Fatalf("definitions are not sorted by name: %v", names)
	}
	for range 20 {
		again := registry.Definitions()
		for index, definition := range again {
			if definition.Name != names[index] {
				t.Fatalf("definition %d is %q, previously %q", index, definition.Name, names[index])
			}
		}
	}
}
