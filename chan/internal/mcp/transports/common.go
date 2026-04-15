package transports

import (
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

type Config struct {
	Kind           string
	Command        string
	Args           []string
	Env            map[string]string
	WorkingDir     string
	URL            string
	Headers        map[string]string
	ConnectTimeout time.Duration
	ShutdownGrace  time.Duration
}

func Build(definition Config) (sdkmcp.Transport, error) {
	switch definition.Kind {
	case "stdio":
		return NewStdio(definition), nil
	case "sse":
		return NewSSE(definition), nil
	case "http":
		return NewHTTP(definition), nil
	case "ws":
		return NewWS(definition), nil
	default:
		return nil, fmt.Errorf("unsupported transport kind %q", definition.Kind)
	}
}

func newHeaderHTTPClient(headers map[string]string) *http.Client {
	if len(headers) == 0 {
		return &http.Client{}
	}
	return &http.Client{
		Transport: headerRoundTripper{
			Base:    http.DefaultTransport,
			Headers: cloneHeaders(headers),
		},
	}
}

func cloneHeaders(headers map[string]string) http.Header {
	if len(headers) == 0 {
		return nil
	}
	cloned := make(http.Header, len(headers))
	for key, value := range headers {
		cloned.Set(key, value)
	}
	return cloned
}

type headerRoundTripper struct {
	Base    http.RoundTripper
	Headers http.Header
}

func (r headerRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	clone := req.Clone(req.Context())
	clone.Header = clone.Header.Clone()
	for key, values := range r.Headers {
		clone.Header.Del(key)
		for _, value := range values {
			clone.Header.Add(key, value)
		}
	}
	base := r.Base
	if base == nil {
		base = http.DefaultTransport
	}
	return base.RoundTrip(clone)
}

func mergeEnv(overrides map[string]string) []string {
	if len(overrides) == 0 {
		return nil
	}
	envMap := make(map[string]string)
	for _, entry := range os.Environ() {
		key, value, ok := splitEnv(entry)
		if ok {
			envMap[key] = value
		}
	}
	for key, value := range overrides {
		envMap[key] = value
	}
	merged := make([]string, 0, len(envMap))
	for key, value := range envMap {
		merged = append(merged, key+"="+value)
	}
	return merged
}

func splitEnv(entry string) (string, string, bool) {
	return strings.Cut(entry, "=")
}
