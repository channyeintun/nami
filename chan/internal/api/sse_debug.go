package api

import (
	"io"

	"github.com/channyeintun/gocode/internal/debuglog"
)

// sseBodyWithDebug wraps an io.Reader with debug logging when enabled.
func sseBodyWithDebug(body io.Reader, provider string) io.Reader {
	if debuglog.Enabled {
		return debuglog.NewSSEReaderProxy(body, provider)
	}
	return body
}
