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

// SecretKeys aggregates the exact secret values of every route client,
// deduplicated. It lets session-level redaction (journal, stream sink, export)
// cover all configured routes instead of only the route that happened to fail.
func (r *Router) SecretKeys() []string {
	if r == nil {
		return nil
	}
	var out []string
	seen := make(map[string]struct{})
	for _, re := range r.routes {
		for _, k := range exactKeys(re.Client) {
			if _, dup := seen[k]; dup {
				continue
			}
			seen[k] = struct{}{}
			out = append(out, k)
		}
	}
	return out
}

// ExactKeys returns the exact configured secret values exposed by p, or nil
// when p exposes none. A *Router contributes the keys of every route client.
func ExactKeys(p Provider) []string {
	if p == nil {
		return nil
	}
	if skp, ok := p.(SecretKeyProvider); ok {
		return skp.SecretKeys()
	}
	return nil
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
