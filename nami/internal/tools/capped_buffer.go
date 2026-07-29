package tools

import (
	"bytes"
	"fmt"

	"github.com/channyeintun/nami/internal/textutil"
)

// cappedBuffer collects command output up to a byte limit and then counts what
// it drops. It never reports a short write, so a command that keeps printing
// keeps running instead of dying with a broken pipe.
type cappedBuffer struct {
	limit   int
	buffer  bytes.Buffer
	dropped int
}

func (b *cappedBuffer) Write(p []byte) (int, error) {
	remaining := b.limit - b.buffer.Len()
	if remaining <= 0 {
		b.dropped += len(p)
		return len(p), nil
	}
	if len(p) <= remaining {
		return b.buffer.Write(p)
	}
	kept := textutil.TruncateHeadBytes(p, remaining)
	if _, err := b.buffer.Write(kept); err != nil {
		return 0, err
	}
	b.dropped += len(p) - len(kept)
	return len(p), nil
}

// String returns the captured output, with a trailing note when output was
// dropped so the model knows the result is incomplete.
func (b *cappedBuffer) String() string {
	if b.dropped == 0 {
		return b.buffer.String()
	}
	return fmt.Sprintf("%s\n[Output truncated: %d more bytes were discarded after the %d byte limit]", b.buffer.String(), b.dropped, b.limit)
}
