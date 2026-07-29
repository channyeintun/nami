// Package textutil holds small text helpers shared across the engine. The
// truncation helpers here exist because slicing a string by byte count splits
// multi-byte characters and leaks replacement characters into transcripts and
// tool output.
package textutil

import "unicode/utf8"

// TruncateHead keeps at most maxBytes bytes from the start of s, backing off to
// the previous rune boundary rather than cutting a character in half.
func TruncateHead(s string, maxBytes int) string {
	if maxBytes <= 0 {
		return ""
	}
	if len(s) <= maxBytes {
		return s
	}
	return s[:headBoundary(s, maxBytes)]
}

// TruncateTail keeps at most maxBytes bytes from the end of s, starting at a
// rune boundary so the result never begins with a partial character.
func TruncateTail(s string, maxBytes int) string {
	if maxBytes <= 0 {
		return ""
	}
	if len(s) <= maxBytes {
		return s
	}
	return s[tailBoundary(s, maxBytes):]
}

// TruncateHeadBytes is TruncateHead for byte slices. The result aliases b.
func TruncateHeadBytes(b []byte, maxBytes int) []byte {
	if maxBytes <= 0 {
		return nil
	}
	if len(b) <= maxBytes {
		return b
	}
	return b[:headBoundary(string(b), maxBytes)]
}

// TruncateTailBytes is TruncateTail for byte slices. The result aliases b.
func TruncateTailBytes(b []byte, maxBytes int) []byte {
	if maxBytes <= 0 {
		return nil
	}
	if len(b) <= maxBytes {
		return b
	}
	return b[tailBoundary(string(b), maxBytes):]
}

// headBoundary returns the largest cut point at or below limit that does not
// land inside a rune. Walking back past the start of the string only happens
// for input that is not valid UTF-8, and there the limit is cut as given.
func headBoundary(s string, limit int) int {
	cut := limit
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	if cut == 0 && !utf8.RuneStart(s[0]) {
		return limit
	}
	return cut
}

// tailBoundary returns the smallest start offset at or above len(s)-limit that
// begins a rune.
func tailBoundary(s string, limit int) int {
	start := len(s) - limit
	for start < len(s) && !utf8.RuneStart(s[start]) {
		start++
	}
	return start
}
