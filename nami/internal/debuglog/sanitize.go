package debuglog

import (
	"regexp"
	"strings"

	"github.com/channyeintun/nami/internal/textutil"
)

// secretPatterns find the secrets that raw logged traffic can carry: IPC
// frames hold tool input and output (a .env file that was read, a curl
// command with an Authorization header), and provider streams can echo
// request details. Each pattern captures the text before a secret and the
// secret itself; only the secret is replaced. A value may run to the end of
// the text, so a secret cut off by truncation is still hidden. This is a best
// effort that covers the common shapes, not a guarantee.
var secretPatterns = []*regexp.Regexp{
	// A JSON field whose name marks a secret, also inside an escaped JSON
	// string: "api_key": "...", \"access_token\":\"...\".
	regexp.MustCompile(`(?i)(\\?"[\w-]*(?:token|secret|password|passwd|api[_-]?key|authorization|credential|cookie)[\w-]*\\?"\s*:\s*\\?")([^"\\]+)`),
	// An assignment in a shell command, an env file or a URL query:
	// OPENAI_API_KEY=..., --api-key=..., ?access_token=....
	regexp.MustCompile(`(?i)(\b[\w-]*(?:token|secret|password|passwd|api[_-]?key)[\w-]*=)([^\s"'\\&;|]+)`),
	// An Authorization header outside JSON: Authorization: Bearer ....
	regexp.MustCompile(`(?i)(\bauthorization\s*:\s*(?:bearer\s+|basic\s+)?)([^\s"'\\]+)`),
	// A bearer token anywhere else.
	regexp.MustCompile(`(?i)(\bbearer\s+)([\w.~+/=-]{16,})`),
	// Keys in well-known formats, which need no telling name around them.
	// The empty first group keeps the shape of the patterns above.
	regexp.MustCompile(`()\b((?:sk-|ghp_|gho_|ghu_|ghs_|ghr_|github_pat_|glpat-|xox[abprs]-|AIza)[\w-]{16,}|AKIA[0-9A-Z]{16})`),
}

// redactionLookahead is how far past a cut Redacted still looks, so that a
// secret starting just before the cut is recognized from its name.
const redactionLookahead = 1024

// Redacted returns value with secrets hidden, cut to at most limit bytes.
// Only a little more than limit bytes is scanned: a raw frame can be
// megabytes long, and all but its start is cut anyway.
func Redacted(value string, limit int) string {
	if limit > 0 && len(value) > limit+redactionLookahead {
		value = textutil.TruncateHead(value, limit+redactionLookahead)
	}
	return Truncate(RedactSecrets(value), limit)
}

// RedactSecrets hides the secrets secretPatterns find in value.
func RedactSecrets(value string) string {
	if strings.TrimSpace(value) == "" {
		return value
	}
	for _, pattern := range secretPatterns {
		value = pattern.ReplaceAllStringFunc(value, func(match string) string {
			parts := pattern.FindStringSubmatch(match)
			if len(parts) < 3 {
				return match
			}
			return parts[1] + maskSecret(parts[2])
		})
	}
	return value
}

// maskSecret hides a secret, keeping a short head and tail of a long one so
// it can still be told apart from others in a log. It cuts on character
// boundaries so the line stays readable.
func maskSecret(secret string) string {
	if len(secret) <= 12 {
		return "[REDACTED]"
	}
	return textutil.TruncateHead(secret, 6) + "..." + textutil.TruncateTail(secret, 4)
}

// Truncate caps a logged value at limit bytes, cutting on a character boundary
// so the log line stays readable text.
func Truncate(value string, limit int) string {
	if limit <= 0 || len(value) <= limit {
		return value
	}
	return textutil.TruncateHead(value, limit) + "...(truncated)"
}
