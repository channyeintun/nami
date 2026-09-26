package tools

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestCappedBufferKeepsOutputUnderTheLimit(t *testing.T) {
	buffer := &cappedBuffer{limit: 100}
	n, err := buffer.Write([]byte("hello"))
	if err != nil || n != 5 {
		t.Fatalf("Write = %d, %v", n, err)
	}
	if got := buffer.String(); got != "hello" {
		t.Fatalf("String = %q", got)
	}
}

// A command that keeps printing must not be able to grow the buffer without
// bound, and must not see a short write that would kill it with a broken pipe.
func TestCappedBufferTruncatesAndReportsDroppedBytes(t *testing.T) {
	buffer := &cappedBuffer{limit: 16}
	payload := []byte(strings.Repeat("a", 64))

	n, err := buffer.Write(payload)
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if n != len(payload) {
		t.Fatalf("Write reported %d of %d bytes; a short write would break the command", n, len(payload))
	}

	got := buffer.String()
	if !strings.HasPrefix(got, strings.Repeat("a", 16)) {
		t.Fatalf("String = %q, want the first 16 bytes", got)
	}
	if !strings.Contains(got, "Output truncated") {
		t.Fatalf("String = %q, want a truncation notice", got)
	}
	if !strings.Contains(got, "48 more bytes") {
		t.Fatalf("String = %q, want the dropped byte count", got)
	}
}

func TestCappedBufferAccumulatesDroppedBytesAcrossWrites(t *testing.T) {
	buffer := &cappedBuffer{limit: 4}
	for range 3 {
		if _, err := buffer.Write([]byte("0123456789")); err != nil {
			t.Fatalf("Write: %v", err)
		}
	}
	if !strings.Contains(buffer.String(), "26 more bytes") {
		t.Fatalf("String = %q, want 26 dropped bytes", buffer.String())
	}
}

func TestCappedBufferCutsOnRuneBoundary(t *testing.T) {
	// The limit lands in the middle of a two-byte rune.
	buffer := &cappedBuffer{limit: 3}
	if _, err := buffer.Write([]byte("aéb")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	got := buffer.String()
	if !utf8.ValidString(got) {
		t.Fatalf("String = %q, want valid UTF-8", got)
	}
	if !strings.HasPrefix(got, "a") || strings.Contains(got, "�") {
		t.Fatalf("String = %q", got)
	}
}

// What the buffer keeps must be a prefix of the output: once anything is
// dropped, later writes are dropped too, even ones small enough to fit, and a
// character split across writes at the limit is not left half there.
func TestCappedBufferKeepsAPrefixOfTheOutput(t *testing.T) {
	cases := []struct {
		name   string
		limit  int
		writes []string
		kept   string
		counts string
	}{
		{"small write after a dropped one", 4, []string{"aé", "é", "b"}, "aé", "3 more bytes"},
		{"character split across writes at the limit", 2, []string{"a\xc3", "\xa9b"}, "a", "3 more bytes"},
		{"exact fit", 3, []string{"aé"}, "aé", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			buffer := &cappedBuffer{limit: tc.limit}
			for _, write := range tc.writes {
				if n, err := buffer.Write([]byte(write)); err != nil || n != len(write) {
					t.Fatalf("Write(%q) = %d, %v", write, n, err)
				}
			}
			got := buffer.String()
			if !utf8.ValidString(got) {
				t.Fatalf("String = %q, want valid UTF-8", got)
			}
			kept, _, _ := strings.Cut(got, "\n[Output truncated")
			if kept != tc.kept {
				t.Fatalf("kept %q, want %q (String = %q)", kept, tc.kept, got)
			}
			if tc.counts != "" && !strings.Contains(got, tc.counts) {
				t.Fatalf("String = %q, want it to report %q", got, tc.counts)
			}
		})
	}
}

func TestCappedBufferWithZeroLimitDropsEverything(t *testing.T) {
	buffer := &cappedBuffer{}
	n, err := buffer.Write([]byte("output"))
	if err != nil || n != 6 {
		t.Fatalf("Write = %d, %v", n, err)
	}
	if !strings.Contains(buffer.String(), "Output truncated") {
		t.Fatalf("String = %q", buffer.String())
	}
}
