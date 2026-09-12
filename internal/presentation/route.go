package presentation

import (
	"fmt"
	"strings"

	"nabd/internal/agent"
	"nabd/internal/display"
)

// FormatRouteNotice formats a provider-route event for user presentation.
// It returns the sanitized notice text and true if the event should be shown,
// or false if the event should remain hidden.
//
// Visibility rules:
//   - status == "failed": visible for any attempt (including attempt 0 or negative).
//   - status == "selected": visible ONLY when attempt > 1. Attempt <= 1 (primary or malformed) is hidden.
//   - status == "attempted": hidden.
//   - status == "exhausted": hidden.
//   - route == nil or unknown status: hidden, no panic.
//
// Safety:
//   - Provider, Model, and Reason are sanitized via display.SanitizeForDisplay.
//   - StreamID is never exposed.
//   - Reason is not appended to "selected" notices.
//   - Empty or effectively blank fields are replaced with safe placeholders.
//   - Output is strictly a single logical line without newlines or terminal controls.
func FormatRouteNotice(r *agent.ProviderRoute) (string, bool) {
	if r == nil {
		return "", false
	}

	switch r.Status {
	case "failed":
		prov := cleanField(r.Provider, "unknown-provider")
		model := cleanField(r.Model, "unknown-model")
		reason := cleanField(r.Reason, "failed")
		raw := fmt.Sprintf("route failed: %s/%s (attempt %d): %s", prov, model, r.Attempt, reason)
		clean := display.SanitizeForDisplay(raw, display.DisplayPolicy{
			AllowNewline: false,
			Redact:       true,
		})
		return clean, true

	case "selected":
		if r.Attempt <= 1 {
			return "", false
		}
		prov := cleanField(r.Provider, "unknown-provider")
		model := cleanField(r.Model, "unknown-model")
		raw := fmt.Sprintf("route selected: %s/%s (attempt %d)", prov, model, r.Attempt)
		clean := display.SanitizeForDisplay(raw, display.DisplayPolicy{
			AllowNewline: false,
			Redact:       true,
		})
		return clean, true

	default:
		return "", false
	}
}

func cleanField(val, placeholder string) string {
	cleaned := display.SanitizeForDisplay(val, display.DisplayPolicy{
		AllowNewline: false,
		Redact:       true,
	})
	cleaned = strings.TrimSpace(cleaned)
	if cleaned == "" {
		return placeholder
	}
	return cleaned
}
