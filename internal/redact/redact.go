// Package redact removes credential-like values from text before the text
// reaches logs, displays, exported data, or optionally persisted journals.
package redact

import (
	"regexp"
	"strings"
)

const (
	Token        = "[REDACTED]"
	MaxBodyBytes = 4 * 1024
)

// secretPatterns deliberately target credential formats rather than arbitrary
// high-entropy text, reducing false positives in ordinary output and model IDs.
var secretPatterns = []*regexp.Regexp{
	regexp.MustCompile(`sk-ant-[A-Za-z0-9_\-]{8,}`),
	regexp.MustCompile(`sk-or-[A-Za-z0-9_\-]{8,}`),
	regexp.MustCompile(`gsk_[A-Za-z0-9_]{8,}`),
	regexp.MustCompile(`nvapi-[A-Za-z0-9_\-]{8,}`),
	regexp.MustCompile(`(?i)Bearer\s+[A-Za-z0-9_\-\.]{8,}`),
	regexp.MustCompile(`(?i)authorization[:\s]+[A-Za-z0-9_\-\.]{8,}`),
	regexp.MustCompile(`github_pat_[A-Za-z0-9_]{16,}`),
	regexp.MustCompile(`gh[pousr]_[A-Za-z0-9_]{16,}`),
	regexp.MustCompile(`glpat-[A-Za-z0-9_\-]{16,}`),
	regexp.MustCompile(`xox[baprs]-[A-Za-z0-9_\-]{10,}`),
}

// Redact replaces recognized credential patterns with Token.
// It is deterministic, idempotent, and safe for concurrent use.
func Redact(s string) string {
	for _, re := range secretPatterns {
		s = re.ReplaceAllString(s, Token)
	}
	return s
}

// RedactExactKeys replaces exact configured credential values.
// Empty values are ignored because replacing an empty string would corrupt
// every boundary in the input.
func RedactExactKeys(s string, keys []string) string {
	for _, key := range keys {
		if key == "" {
			continue
		}
		s = strings.ReplaceAll(s, key, Token)
	}
	return s
}

// TruncateBody applies the provider-body storage bound.
// Callers must redact before calling this function.
func TruncateBody(body string) string {
	if len(body) <= MaxBodyBytes {
		return body
	}
	return body[:MaxBodyBytes] + "…[truncated]"
}

// SanitizeBody redacts exact and recognized credentials before truncation.
func SanitizeBody(body string, exactKeys []string) string {
	body = RedactExactKeys(body, exactKeys)
	body = Redact(body)
	return TruncateBody(body)
}
