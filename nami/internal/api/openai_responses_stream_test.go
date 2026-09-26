package api

import (
	"strings"
	"testing"
)

// responsesTextStream streams text as deltas and then repeats it, as the
// Responses API does, in the output_text.done and output_item.done events.
func responsesTextStream(text string) string {
	quoted := strings.ReplaceAll(text, "\n", `\n`)
	return `data: {"type":"response.output_item.added","item":{"type":"message"}}

data: {"type":"response.output_text.delta","delta":"` + quoted + `"}

data: {"type":"response.output_text.done","text":"` + quoted + `"}

data: {"type":"response.output_item.done","item":{"type":"message","content":[{"type":"output_text","text":"` + quoted + `"}]}}

data: {"type":"response.completed","response":{"status":"completed"}}

`
}

func TestResponsesStreamEmitsMessageTextOnce(t *testing.T) {
	for _, text := range []string{
		"Hello world",
		// Text with surrounding whitespace was emitted a second time once
		// the final copy of the message arrived.
		"Hello world\n",
		"\nHello world",
	} {
		client, err := NewOpenAIResponsesClient("openai", "gpt-5.5", "key", serveStream(t, responsesTextStream(text)).URL)
		if err != nil {
			t.Fatalf("new client: %v", err)
		}
		events := drainStream(t, client, ModelRequest{Messages: []Message{{Role: RoleUser, Content: "hi"}}})

		var streamed strings.Builder
		for _, event := range events {
			if event.Type == ModelEventToken {
				streamed.WriteString(event.Text)
			}
		}
		if streamed.String() != text {
			t.Errorf("streamed %q, want %q", streamed.String(), text)
		}
	}
}

func TestResponsesStreamEmitsAMessageSentOnlyWhole(t *testing.T) {
	// Without deltas, the finished message is the only copy of the text.
	body := `data: {"type":"response.output_item.added","item":{"type":"message"}}

data: {"type":"response.output_item.done","item":{"type":"message","content":[{"type":"output_text","text":"Hello world"}]}}

data: {"type":"response.completed","response":{"status":"completed"}}

`
	client, err := NewOpenAIResponsesClient("openai", "gpt-5.5", "key", serveStream(t, body).URL)
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	events := drainStream(t, client, ModelRequest{Messages: []Message{{Role: RoleUser, Content: "hi"}}})

	var tokens []string
	for _, event := range events {
		if event.Type == ModelEventToken {
			tokens = append(tokens, event.Text)
		}
	}
	if len(tokens) != 1 || tokens[0] != "Hello world" {
		t.Fatalf("tokens = %q, want the message once", tokens)
	}
}
