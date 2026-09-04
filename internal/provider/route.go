// Package provider: route.go defines the types and parsing for
// NABD_ROUTES-based multi-provider routing (v1.2.0).
//
// Canonical grammar (F-section):
//
//	routes  = entry ("," entry)*
//	entry   = provider ":" model
//	provider = 1..32 bytes, trimmed, lowercased, allow-listed
//	model    = 1..256 bytes, trimmed, case-preserved, may contain ':'
//
// The comma is the only field separator and cannot be escaped in v1.2.0.
// Document this limitation in README; do not silently accept it.
package provider

import (
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"
)

// RouteEntry is a parsed, validated (provider, model) pair from NABD_ROUTES.
// It holds no key material and is safe to log after redaction.
type RouteEntry struct {
	// Provider is normalized (lowercase, allow-listed).
	Provider string
	// Model is case-preserved; may contain ':' (e.g. "some-model:free").
	Model string
}

// AllowedProviders is the closed set of provider identifiers supported in v1.2.0.
// Any value not in this set is rejected at startup with a clear error.
var AllowedProviders = []string{"anthropic", "groq", "openrouter", "nvidia"}

// allowedProviderSet is the map form for O(1) lookup.
var allowedProviderSet = func() map[string]struct{} {
	m := make(map[string]struct{}, len(AllowedProviders))
	for _, p := range AllowedProviders {
		m[p] = struct{}{}
	}
	return m
}()

const (
	maxRoutes        = 16
	maxProviderBytes = 32
	maxModelBytes    = 256
	routeCommaNote   = "Note: the comma ',' is the route separator and cannot appear inside a model name in v1.2.0."
)

// ParseRouteErrors collects every validation error found during parsing.
// It is returned when any entry fails, so the user sees all problems at once.
type ParseRouteErrors []error

func (e ParseRouteErrors) Error() string {
	msgs := make([]string, len(e))
	for i, err := range e {
		msgs[i] = err.Error()
	}
	return strings.Join(msgs, "; ")
}

func (e ParseRouteErrors) Unwrap() []error { return []error(e) }

// ParseRoutes parses the value of NABD_ROUTES into a validated, ordered slice
// of RouteEntry values. All validation errors are returned together.
//
// Rules enforced (F1-F14 abbreviated):
//   - 1..16 entries (F4).
//   - No trailing comma; no empty entry (F6).
//   - Each entry must have exactly one ':' separator (split at first ':') (F6).
//   - Provider: non-empty, ≤32 bytes, lowercased, in AllowedProviders (F6).
//   - Model: non-empty, ≤256 bytes, case-preserved (F6).
//   - No ASCII control chars, CR, LF, NUL, or invalid UTF-8 (F7).
//   - No exact duplicate (provider+model) pairs after normalization (F5).
//   - Comma is unescapable; document it, don't silently ignore (F section note).
func ParseRoutes(raw string) ([]RouteEntry, error) {
	if err := rejectBadBytes(raw); err != nil {
		return nil, fmt.Errorf("NABD_ROUTES: %w", err)
	}

	// Split on comma; trailing comma produces an empty final entry which
	// is caught by the empty-entry check below.
	parts := strings.Split(raw, ",")

	if len(parts) > maxRoutes {
		return nil, fmt.Errorf(
			"NABD_ROUTES: too many routes (%d); maximum is %d",
			len(parts), maxRoutes)
	}

	var (
		errs   ParseRouteErrors
		result []RouteEntry
		seen   = make(map[string]struct{}) // "provider\x00model" after normalization
	)

	for i, part := range parts {
		entry, err := parseRouteEntry(part, i)
		if err != nil {
			errs = append(errs, err)
			continue
		}

		key := entry.Provider + "\x00" + entry.Model
		if _, dup := seen[key]; dup {
			errs = append(errs, fmt.Errorf(
				"NABD_ROUTES[%d]: duplicate route %q:%q (exact provider+model pair already listed); same provider with different models is allowed",
				i, entry.Provider, entry.Model))
			continue
		}
		seen[key] = struct{}{}
		result = append(result, entry)
	}

	if len(errs) > 0 {
		return nil, errs
	}
	return result, nil
}

// parseRouteEntry validates one comma-delimited entry at index i.
func parseRouteEntry(raw string, i int) (RouteEntry, error) {
	// Trim leading/trailing ASCII space/tab only — not other whitespace —
	// so that NUL or CR in padding is caught by rejectBadBytes above.
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		msg := fmt.Sprintf("NABD_ROUTES[%d]: empty entry", i)
		if i == 0 && raw == "" {
			msg = "NABD_ROUTES: value is empty"
		}
		return RouteEntry{}, errors.New(msg)
	}

	// Split at the FIRST colon only (F section: "split at FIRST colon").
	prov, model, ok := strings.Cut(trimmed, ":")
	if !ok {
		return RouteEntry{}, fmt.Errorf(
			"NABD_ROUTES[%d]: missing ':' separator in %q — format is provider:model",
			i, sanitizeForError(trimmed))
	}

	prov = strings.TrimSpace(prov)
	model = strings.TrimSpace(model)

	if prov == "" {
		return RouteEntry{}, fmt.Errorf(
			"NABD_ROUTES[%d]: provider part is empty", i)
	}
	if model == "" {
		return RouteEntry{}, fmt.Errorf(
			"NABD_ROUTES[%d]: model part is empty", i)
	}

	// Normalize provider to lowercase.
	prov = strings.ToLower(prov)

	if len(prov) > maxProviderBytes {
		return RouteEntry{}, fmt.Errorf(
			"NABD_ROUTES[%d]: provider name too long (%d bytes, max %d)",
			i, len(prov), maxProviderBytes)
	}
	if len(model) > maxModelBytes {
		return RouteEntry{}, fmt.Errorf(
			"NABD_ROUTES[%d]: model name too long (%d bytes, max %d)",
			i, len(model), maxModelBytes)
	}

	if _, ok := allowedProviderSet[prov]; !ok {
		return RouteEntry{}, fmt.Errorf(
			"NABD_ROUTES[%d]: unknown provider %q; supported: %s",
			i, prov, strings.Join(AllowedProviders, ", "))
	}

	return RouteEntry{Provider: prov, Model: model}, nil
}

// rejectBadBytes returns an error if s contains any ASCII control character
// (0x00–0x1F, 0x7F), or invalid UTF-8 sequences (F7).
// This is checked on the entire raw value before splitting, so a control
// character inside any position — provider, model, or separator — is caught.
func rejectBadBytes(s string) error {
	if !utf8.ValidString(s) {
		return errors.New("value contains invalid UTF-8 bytes")
	}
	for i := 0; i < len(s); i++ {
		b := s[i]
		if b < 0x20 || b == 0x7F {
			return fmt.Errorf(
				"value contains forbidden control character 0x%02X at byte %d", b, i)
		}
	}
	return nil
}

// sanitizeForError returns a display-safe version of an entry string for use
// in error messages. Limits to 64 runes and strips anything non-printable.
// Never placed in a context where secrets could appear (entries are provider:model only).
func sanitizeForError(s string) string {
	var b strings.Builder
	count := 0
	for _, r := range s {
		if count >= 64 {
			b.WriteString("…")
			break
		}
		if r >= 0x20 && r != 0x7F {
			b.WriteRune(r)
		} else {
			b.WriteString("·")
		}
		count++
	}
	return b.String()
}

// RouterConfig is the validated, fully parsed configuration for the router.
// It is built once at startup from environment/config file values and is
// thereafter read-only. Building it reads config values exactly once and
// passes them explicitly to all sub-constructors (F14, X12).
type RouterConfig struct {
	Routes           []RouteEntry
	Mode             RouterMode
	PrestreamTimeout int // seconds, validated to [5, 120]
}

// RouterMode is the routing strategy. Only RouterModeFallback is supported
// in v1.2.0; any other value is a startup error (F3).
type RouterMode string

const (
	RouterModeFallback RouterMode = "fallback"
)

// ParseRouterMode validates the NABD_ROUTER_MODE value. Empty string and
// unknown values are explicit errors, not silent defaults (F3).
func ParseRouterMode(raw string) (RouterMode, error) {
	switch raw {
	case string(RouterModeFallback):
		return RouterModeFallback, nil
	case "":
		return "", fmt.Errorf(
			"NABD_ROUTER_MODE: value is empty; supported modes: fallback")
	default:
		return "", fmt.Errorf(
			"NABD_ROUTER_MODE: unknown value %q; supported modes: fallback",
			raw)
	}
}

// ParsePrestreamTimeout validates the NABD_ROUTER_PRESTREAM_TIMEOUT integer
// (seconds). Bounds: [5, 120]. Default if raw is "" is 30 (G section).
// A malformed or out-of-bounds value is a startup error.
func ParsePrestreamTimeout(raw string) (int, error) {
	if raw == "" {
		return 30, nil
	}
	n, err := parsePositiveInt(raw)
	if err != nil {
		return 0, fmt.Errorf(
			"NABD_ROUTER_PRESTREAM_TIMEOUT: %w; must be an integer in [5, 120]", err)
	}
	if n < 5 || n > 120 {
		return 0, fmt.Errorf(
			"NABD_ROUTER_PRESTREAM_TIMEOUT: value %d out of range; must be in [5, 120]", n)
	}
	return n, nil
}

// parsePositiveInt parses a decimal integer string. Returns an error if the
// string is not a valid non-negative integer or overflows int.
func parsePositiveInt(s string) (int, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, errors.New("empty value")
	}
	var n int
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, fmt.Errorf("non-digit character %q in integer", c)
		}
		prev := n
		n = n*10 + int(c-'0')
		if n < prev {
			return 0, errors.New("integer overflow")
		}
	}
	return n, nil
}
