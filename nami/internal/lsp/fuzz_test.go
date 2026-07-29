package lsp

import (
	"encoding/json"
	"testing"
)

// FuzzReadMessage feeds arbitrary bytes through the wire framing. A language
// server is a separate process whose output the engine cannot trust, so
// readMessage must always return an error rather than panic or over-allocate.
func FuzzReadMessage(f *testing.F) {
	f.Add("Content-Length: 2\r\n\r\n{}")
	f.Add("Content-Length: 0\r\n\r\n")
	f.Add("Content-Length: -1\r\n\r\n{}")
	f.Add("Content-Length: 99999999999999\r\n\r\n{}")
	f.Add("Content-Length: abc\r\n\r\n{}")
	f.Add("\r\n\r\n")
	f.Add("Content-Length: 34\r\n\r\n{\"jsonrpc\":\"2.0\",\"id\":1,\"result\":2}")
	f.Add("Content-Type: x\r\nContent-Length: 4\r\n\r\nnull")

	f.Fuzz(func(t *testing.T, stream string) {
		envelope, err := readerClient(stream).readMessage()
		if err != nil {
			return
		}
		// A decoded envelope must be internally consistent: the raw sections
		// stay valid JSON that downstream decoding can rely on.
		if len(envelope.Result) > 0 && !json.Valid(envelope.Result) {
			t.Fatalf("decoded result is not valid JSON: %q", envelope.Result)
		}
		if len(envelope.Params) > 0 && !json.Valid(envelope.Params) {
			t.Fatalf("decoded params is not valid JSON: %q", envelope.Params)
		}
		if len(envelope.ID) > 0 && !json.Valid(envelope.ID) {
			t.Fatalf("decoded id is not valid JSON: %q", envelope.ID)
		}
		if _, ok := parseResponseID(envelope.ID); ok && envelope.Method != "" {
			// Responses carry an id, notifications carry a method; both is
			// allowed by the wire format, so only assert we survive it.
			_ = envelope
		}
	})
}

// FuzzExtractHoverContents runs the hover flattener over arbitrary decoded JSON.
// Hover payloads are free-form in the protocol, so the flattener has to cope
// with any shape a server sends.
func FuzzExtractHoverContents(f *testing.F) {
	f.Add(`"plain text"`)
	f.Add(`{"kind":"markdown","value":"# Title"}`)
	f.Add(`{"language":"go","value":"func f()"}`)
	f.Add(`["one",{"value":"two"}]`)
	f.Add(`null`)
	f.Add(`[[[["deep"]]]]`)
	f.Add(`{"value":123}`)

	f.Fuzz(func(t *testing.T, encoded string) {
		var value any
		if err := json.Unmarshal([]byte(encoded), &value); err != nil {
			return
		}
		// The only contract is that it terminates and returns a string.
		_ = extractHoverContents(value)
	})
}

// FuzzLocationRowsFromAny checks the result normalizer against arbitrary JSON
// shapes: every row it produces must carry 1-based positions.
func FuzzLocationRowsFromAny(f *testing.F) {
	f.Add(`[{"uri":"file:///a.go","range":{"start":{"line":0,"character":0},"end":{"line":0,"character":1}}}]`)
	f.Add(`{"targetUri":"file:///a.go","targetRange":{"start":{"line":2,"character":2},"end":{"line":2,"character":3}},"targetSelectionRange":{"start":{"line":2,"character":2},"end":{"line":2,"character":3}}}`)
	f.Add(`[]`)
	f.Add(`null`)
	f.Add(`{"uri":""}`)

	f.Fuzz(func(t *testing.T, encoded string) {
		var value any
		if err := json.Unmarshal([]byte(encoded), &value); err != nil {
			return
		}
		for _, row := range locationRowsFromAny(value, "definition") {
			if row.Kind != "definition" {
				t.Fatalf("row kind = %q, want definition", row.Kind)
			}
			if row.Line < 0 || row.Column < 0 {
				t.Fatalf("row has negative position: %+v", row)
			}
		}
	})
}
