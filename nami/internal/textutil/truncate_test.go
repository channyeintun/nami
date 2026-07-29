package textutil

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestTruncateHeadKeepsWholeRunes(t *testing.T) {
	cases := []struct {
		name     string
		input    string
		maxBytes int
		want     string
	}{
		{"shorter than limit", "hello", 10, "hello"},
		{"exact limit", "hello", 5, "hello"},
		{"ascii cut", "hello world", 5, "hello"},
		{"zero limit", "hello", 0, ""},
		{"negative limit", "hello", -1, ""},
		{"empty input", "", 4, ""},
		// "é" is two bytes: a limit of 2 must drop it rather than cut it.
		{"cuts before multibyte rune", "aé", 2, "a"},
		{"keeps complete multibyte rune", "aé", 3, "aé"},
		{"emoji does not fit", "ab😀", 4, "ab"},
		{"emoji fits", "ab😀", 6, "ab😀"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := TruncateHead(tc.input, tc.maxBytes)
			if got != tc.want {
				t.Fatalf("TruncateHead(%q, %d) = %q, want %q", tc.input, tc.maxBytes, got, tc.want)
			}
			if !utf8.ValidString(got) {
				t.Fatalf("TruncateHead produced invalid UTF-8: %q", got)
			}
		})
	}
}

func TestTruncateTailKeepsWholeRunes(t *testing.T) {
	cases := []struct {
		name     string
		input    string
		maxBytes int
		want     string
	}{
		{"shorter than limit", "hello", 10, "hello"},
		{"ascii cut", "hello world", 5, "world"},
		{"zero limit", "hello", 0, ""},
		{"drops partial leading rune", "éb", 2, "b"},
		{"keeps complete leading rune", "éb", 3, "éb"},
		{"emoji does not fit", "😀ab", 4, "ab"},
		{"emoji fits", "😀ab", 6, "😀ab"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := TruncateTail(tc.input, tc.maxBytes)
			if got != tc.want {
				t.Fatalf("TruncateTail(%q, %d) = %q, want %q", tc.input, tc.maxBytes, got, tc.want)
			}
			if !utf8.ValidString(got) {
				t.Fatalf("TruncateTail produced invalid UTF-8: %q", got)
			}
		})
	}
}

func TestTruncateByteHelpersMatchStringHelpers(t *testing.T) {
	input := "héllo wörld 😀 tail"
	for limit := range len(input) + 2 {
		if got, want := string(TruncateHeadBytes([]byte(input), limit)), TruncateHead(input, limit); got != want {
			t.Fatalf("TruncateHeadBytes(%d) = %q, want %q", limit, got, want)
		}
		if got, want := string(TruncateTailBytes([]byte(input), limit)), TruncateTail(input, limit); got != want {
			t.Fatalf("TruncateTailBytes(%d) = %q, want %q", limit, got, want)
		}
	}
}

func TestTruncateHelpersHandleInvalidUTF8WithoutGrowing(t *testing.T) {
	invalid := "\xff\xfe\xfd\xfc"
	for limit := range len(invalid) + 1 {
		if got := TruncateHead(invalid, limit); len(got) > limit {
			t.Fatalf("TruncateHead(invalid, %d) grew to %d bytes", limit, len(got))
		}
		if got := TruncateTail(invalid, limit); len(got) > limit {
			t.Fatalf("TruncateTail(invalid, %d) grew to %d bytes", limit, len(got))
		}
	}
}

// FuzzTruncate pins the three properties every caller relies on: the result
// never exceeds the limit, it stays a prefix or suffix of the input, and valid
// UTF-8 in means valid UTF-8 out.
func FuzzTruncate(f *testing.F) {
	f.Add("hello world", 5)
	f.Add("héllo wörld", 6)
	f.Add("😀😀😀", 5)
	f.Add("", 3)
	f.Add("\xff\xfe", 1)
	f.Add(strings.Repeat("あ", 40), 17)

	f.Fuzz(func(t *testing.T, input string, maxBytes int) {
		if maxBytes > 1<<20 {
			maxBytes = 1 << 20
		}

		head := TruncateHead(input, maxBytes)
		if len(head) > max(maxBytes, 0) {
			t.Fatalf("TruncateHead(%q, %d) returned %d bytes", input, maxBytes, len(head))
		}
		if !strings.HasPrefix(input, head) {
			t.Fatalf("TruncateHead(%q, %d) = %q is not a prefix", input, maxBytes, head)
		}
		if utf8.ValidString(input) && !utf8.ValidString(head) {
			t.Fatalf("TruncateHead(%q, %d) = %q broke a rune", input, maxBytes, head)
		}

		tail := TruncateTail(input, maxBytes)
		if len(tail) > max(maxBytes, 0) {
			t.Fatalf("TruncateTail(%q, %d) returned %d bytes", input, maxBytes, len(tail))
		}
		if !strings.HasSuffix(input, tail) {
			t.Fatalf("TruncateTail(%q, %d) = %q is not a suffix", input, maxBytes, tail)
		}
		if utf8.ValidString(input) && !utf8.ValidString(tail) {
			t.Fatalf("TruncateTail(%q, %d) = %q broke a rune", input, maxBytes, tail)
		}
	})
}
