package debuglog

import (
	"regexp"
	"strings"

	"github.com/channyeintun/nami/internal/textutil"
)

var secretPattern = regexp.MustCompile(`(?i)"?(session_ingress_token|environment_secret|access_token|authorization|secret|token)"?\s*:\s*"([^"]*)"`)

func RedactSecrets(value string) string {
	if strings.TrimSpace(value) == "" {
		return value
	}
	return secretPattern.ReplaceAllStringFunc(value, func(match string) string {
		parts := secretPattern.FindStringSubmatch(match)
		if len(parts) < 3 {
			return match
		}
		field := parts[1]
		secret := parts[2]
		if len(secret) <= 12 {
			return `"` + field + `":"[REDACTED]"`
		}
		// Keep a short head and tail so a token can still be recognized in a
		// log, cutting on character boundaries so the line stays readable.
		return `"` + field + `":"` + textutil.TruncateHead(secret, 6) + `...` + textutil.TruncateTail(secret, 4) + `"`
	})
}

// Truncate caps a logged value at limit bytes, cutting on a character boundary
// so the log line stays readable text.
func Truncate(value string, limit int) string {
	if limit <= 0 || len(value) <= limit {
		return value
	}
	return textutil.TruncateHead(value, limit) + "...(truncated)"
}
