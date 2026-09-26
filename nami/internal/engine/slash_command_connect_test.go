package engine

import "testing"

func TestConnectProviderFromChoice(t *testing.T) {
	tests := []struct {
		name   string
		choice modelSelectionChoice
		want   string
	}{
		// A cancelled picker returns an empty choice. That has to read as
		// "cancelled", not as a request to connect a default provider.
		{name: "cancelled picker", choice: modelSelectionChoice{}, want: ""},
		{name: "blank choice", choice: modelSelectionChoice{Provider: "  ", Model: " "}, want: ""},
		{name: "provider option", choice: modelSelectionChoice{Provider: "openai", Model: "gpt-5"}, want: "openai"},
		{name: "model-only option", choice: modelSelectionChoice{Model: " ollama "}, want: "ollama"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := connectProviderFromChoice(tt.choice); got != tt.want {
				t.Fatalf("connectProviderFromChoice(%+v) = %q, want %q", tt.choice, got, tt.want)
			}
		})
	}
}
