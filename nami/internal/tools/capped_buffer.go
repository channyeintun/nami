package tools

import (
	"bytes"
	"fmt"
)

// cappedBuffer collects command output up to a byte limit and then counts what
// it drops. It never reports a short write, so a command that keeps printing
// keeps running instead of dying with a broken pipe.
type cappedBuffer struct {
	limit   int
	buffer  bytes.Buffer
	dropped int
}

// Write keeps bytes until the buffer holds limit of them and drops the rest,
// so what is kept is always a prefix of the output.
func (b *cappedBuffer) Write(p []byte) (int, error) {
	kept := min(len(p), max(b.limit-b.buffer.Len(), 0))
	if _, err := b.buffer.Write(p[:kept]); err != nil {
		return 0, err
	}
	b.dropped += len(p) - kept
	return len(p), nil
}

// String returns the captured output, with a trailing note when output was
// dropped so the model knows the result is incomplete. The limit can fall
// inside a character, possibly one split across writes, so truncated output
// ends before that character.
func (b *cappedBuffer) String() string {
	if b.dropped == 0 {
		return b.buffer.String()
	}
	kept := trimIncompleteRune(b.buffer.Bytes())
	dropped := b.dropped + b.buffer.Len() - len(kept)
	return fmt.Sprintf("%s\n[Output truncated: %d more bytes were discarded after the %d byte limit]", kept, dropped, b.limit)
}
