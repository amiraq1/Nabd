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

// secretPatternSources is one credential format per line. Each entry targets a
// literal prefix rather than arbitrary high-entropy text, reducing false
// positives in ordinary output and model IDs. Redaction is best effort: it
// removes recognized formats and cannot find an unrecognized secret, one split
// across two streamed events, or one that matches no shape below. Inline flags
// are scoped with (?i:...) so they cannot leak into a neighbouring alternative
// once the sources are joined.
var secretPatternSources = []string{
	`sk-ant-[A-Za-z0-9_\-]{8,}`,
	`sk-or-[A-Za-z0-9_\-]{8,}`,
	`sk-proj-[A-Za-z0-9_\-]{20,}`,
	// The long minimum keeps ordinary words such as sk-learn from matching.
	`sk-[A-Za-z0-9]{32,}`,
	`gsk_[A-Za-z0-9_]{8,}`,
	`nvapi-[A-Za-z0-9_\-]{8,}`,
	`\b(AKIA|ASIA)[0-9A-Z]{16}\b`,
	`(?i:Bearer\s+[A-Za-z0-9_\-\.]{8,})`,
	`(?i:authorization[:\s]+[A-Za-z0-9_\-\.]{8,})`,
	`github_pat_[A-Za-z0-9_]{16,}`,
	`gh[pousr]_[A-Za-z0-9_]{16,}`,
	`glpat-[A-Za-z0-9_\-]{16,}`,
	`xox[baprs]-[A-Za-z0-9_\-]{10,}`,
	`eyJ[A-Za-z0-9_\-]{8,}\.eyJ[A-Za-z0-9_\-]{8,}\.[A-Za-z0-9_\-]{8,}`,
	// A whole PEM private-key block, matched non-greedily so several blocks in
	// one input are each redacted.
	`-----BEGIN [A-Z ]*PRIVATE KEY-----[\s\S]*?-----END [A-Z ]*PRIVATE KEY-----`,
	// A BEGIN line with no matching END: redact from BEGIN to end of input. This
	// alternative is last, after every complete block is already consumed.
	`-----BEGIN [A-Z ]*PRIVATE KEY-----[\s\S]*`,
}

// secretPattern is the alternation of every source, compiled once. One
// leftmost-first pass over the input replaces one regexp execution per format
// (each of which allocated even when nothing matched) with a single one. Order
// is significant: a more specific prefix must precede a more general one, and
// the terminated PEM block must precede its unterminated fallback.
var secretPattern = regexp.MustCompile(strings.Join(secretPatternSources, "|"))

// Redact replaces recognized credential patterns with Token.
// It is deterministic, idempotent, and safe for concurrent use.
func Redact(s string) string {
	if s == "" {
		return s
	}
	return secretPattern.ReplaceAllString(s, Token)
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
