// Package provider: redact.go exposes provider-compatible wrappers around the
// shared credential-redaction implementation in internal/redact.
package provider

import "nabd/internal/redact"

// SecretKeyProvider exposes only exact secret values needed for redaction.
type SecretKeyProvider interface {
	SecretKeys() []string
}

func (a *Anthropic) SecretKeys() []string {
	if a == nil || a.Key == "" {
		return nil
	}
	return []string{a.Key}
}

func (o *OpenAICompat) SecretKeys() []string {
	if o == nil || o.Key == "" {
		return nil
	}
	return []string{o.Key}
}

const (
	redactedToken     = redact.Token
	maxBodyBytes      = redact.MaxBodyBytes
	maxAggregateBytes = 16 * 1024
)

// Redact removes recognized credential patterns.
func Redact(s string) string {
	return redact.Redact(s)
}

// RedactExactKeys removes exact configured credential values.
func RedactExactKeys(s string, keys []string) string {
	return redact.RedactExactKeys(s, keys)
}

// TruncateBody applies the provider-body size bound.
func TruncateBody(body string) string {
	return redact.TruncateBody(body)
}

// SanitizeBody redacts credentials before truncating provider bodies.
func SanitizeBody(body string, exactKeys []string) string {
	return redact.SanitizeBody(body, exactKeys)
}
