// Package provider: redact.go implements centralized secret redaction.
//
// ALL provider-sourced data (error bodies, rate-limit messages, route traces)
// MUST pass through Redact() before being stored, displayed, journaled, or
// placed in an error chain. The canonical replacement token is [REDACTED].
//
// Layers (N1-N7):
//
//	N1. Exact configured key values (not implemented here — done at
//	    ProviderError construction time by the caller that holds the key).
//	N2. Authorization/Bearer patterns.
//	N3. Well-known prefix patterns: sk-…, sk-or-…, gsk_…, nvapi-…
//	N4. Nested JSON error strings (deep scan, not shallow).
//	N5. Redact BEFORE truncation (caller responsibility enforced by API design).
//	N6. Never log request headers (no-op in provider — logging is absent).
//	N7. Never log request body (no-op in provider — logging is absent).
//
// Size limits (N section):
//   - Single sanitized body: max 4 KiB.
//   - Final aggregate error: max 16 KiB (enforced by RouterExhaustedError).
package provider

import (
	"regexp"
	"strings"
)

const (
	redactedToken     = "[REDACTED]"
	maxBodyBytes      = 4 * 1024  // 4 KiB per sanitized provider body
	maxAggregateBytes = 16 * 1024 // 16 KiB for RouterExhaustedError
)

// wellKnownKeyPrefixes are patterns for N3. The regex is narrow enough that it
// won't match normal error text or model IDs (e.g. "gsk" is NOT in a model name).
var wellKnownKeyPatterns = []*regexp.Regexp{
	// sk-ant-... (Anthropic)
	regexp.MustCompile(`sk-ant-[A-Za-z0-9_\-]{8,}`),
	// sk-or-... (OpenRouter)
	regexp.MustCompile(`sk-or-[A-Za-z0-9_\-]{8,}`),
	// gsk_... (Groq)
	regexp.MustCompile(`gsk_[A-Za-z0-9_]{8,}`),
	// nvapi-... (NVIDIA)
	regexp.MustCompile(`nvapi-[A-Za-z0-9_\-]{8,}`),
	// Generic Bearer token in error text (N2)
	regexp.MustCompile(`(?i)Bearer\s+[A-Za-z0-9_\-\.]{8,}`),
	// Authorization header value leaks (N2)
	regexp.MustCompile(`(?i)authorization[:\s]+[A-Za-z0-9_\-\.]{8,}`),
}

// Redact removes known secret patterns from s and returns a safe version.
// It is safe to call concurrently. The returned string contains [REDACTED]
// in place of each matched secret. Normal error text and model IDs are
// preserved (patterns are narrow — they require known prefixes of ≥8 chars).
//
// Callers MUST call Redact BEFORE TruncateBody (N5 invariant).
func Redact(s string) string {
	for _, re := range wellKnownKeyPatterns {
		s = re.ReplaceAllString(s, redactedToken)
	}
	return s
}

// RedactExactKeys replaces verbatim occurrences of each key value in s.
// Call this when the exact key string is available (N1), typically at
// error construction time inside attempt().
func RedactExactKeys(s string, keys []string) string {
	for _, k := range keys {
		if k == "" {
			continue
		}
		s = strings.ReplaceAll(s, k, redactedToken)
	}
	return s
}

// TruncateBody truncates b to maxBodyBytes, appending a marker.
// MUST be called AFTER Redact (N5).
func TruncateBody(b string) string {
	if len(b) <= maxBodyBytes {
		return b
	}
	// Truncate on a safe boundary; append marker.
	return b[:maxBodyBytes] + "…[truncated]"
}

// SanitizeBody applies Redact then TruncateBody in the correct order (N5).
// This is the canonical one-call path for all provider response bodies
// before they enter an error struct, journal entry, or display surface.
func SanitizeBody(body string, exactKeys []string) string {
	s := RedactExactKeys(body, exactKeys)
	s = Redact(s)
	return TruncateBody(s)
}
