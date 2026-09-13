package presentation_test

import (
	"strings"
	"testing"

	"nabd/internal/agent"
	"nabd/internal/presentation"
)

// TestFormatRouteNoticeWaitingIsVisible pins the E9 regression: the bounded
// Retry-After pause used to be invisible, so a deliberate wait looked like a
// hang.
func TestFormatRouteNoticeWaitingIsVisible(t *testing.T) {
	got, ok := presentation.FormatRouteNotice(&agent.ProviderRoute{
		Status:   "waiting",
		Provider: "groq",
		Model:    "openai/gpt-oss-120b",
		Attempt:  2,
		Reason:   "retry-after 20s",
		StreamID: "stream-secret-xyz",
	})
	if !ok {
		t.Fatal("waiting route notice is hidden")
	}
	if !strings.Contains(got, "waiting before retry") {
		t.Fatalf("missing waiting prefix: %q", got)
	}
	if !strings.Contains(got, "retry-after 20s") {
		t.Fatalf("declared wait not shown: %q", got)
	}
	if strings.Contains(got, "stream-secret-xyz") {
		t.Fatalf("stream ID leaked: %q", got)
	}
	if strings.ContainsAny(got, "\n\r") {
		t.Fatalf("notice is not a single line: %q", got)
	}
}

// TestFormatRouteNoticeWaitingWithoutReason falls back to a placeholder rather
// than printing an empty explanation.
func TestFormatRouteNoticeWaitingWithoutReason(t *testing.T) {
	got, ok := presentation.FormatRouteNotice(&agent.ProviderRoute{
		Status: "waiting",
		Reason: "   ",
	})
	if !ok {
		t.Fatal("waiting route notice is hidden")
	}
	if !strings.Contains(got, "provider asked to wait") {
		t.Fatalf("missing placeholder reason: %q", got)
	}
}

// TestFormatRouteNoticeBlockedIsVisible covers the breaker path: a skipped
// route must be explained, not silently dropped.
func TestFormatRouteNoticeBlockedIsVisible(t *testing.T) {
	got, ok := presentation.FormatRouteNotice(&agent.ProviderRoute{
		Status:   "blocked",
		Provider: "nvidia",
		Model:    "moonshotai/kimi-k2.6",
		Reason:   "breaker cooldown",
	})
	if !ok {
		t.Fatal("blocked route notice is hidden")
	}
	for _, want := range []string{"route skipped", "nvidia/moonshotai/kimi-k2.6", "breaker cooldown"} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in %q", want, got)
		}
	}
}

// TestFormatRouteNoticeWaitingRedactsSecrets keeps the new statuses inside the
// same sanitizer contract as the existing ones.
func TestFormatRouteNoticeWaitingRedactsSecrets(t *testing.T) {
	got, ok := presentation.FormatRouteNotice(&agent.ProviderRoute{
		Status: "waiting",
		Reason: "upstream said Bearer sk-ant-api03-abcdef0123456789abcdef0123456789",
	})
	if !ok {
		t.Fatal("waiting route notice is hidden")
	}
	if strings.Contains(got, "sk-ant-api03-") {
		t.Fatalf("secret leaked: %q", got)
	}
	if !strings.Contains(got, "[REDACTED]") {
		t.Fatalf("expected redaction placeholder: %q", got)
	}
}

// TestFormatRouteNoticeStillHidesStructuralStatuses guards the statuses that
// must stay invisible, so widening visibility does not become a habit.
func TestFormatRouteNoticeStillHidesStructuralStatuses(t *testing.T) {
	for _, status := range []string{"attempted", "exhausted", "unknown_status", ""} {
		if _, ok := presentation.FormatRouteNotice(&agent.ProviderRoute{
			Status:   status,
			Provider: "groq",
			Model:    "openai/gpt-oss-120b",
			Attempt:  3,
			Reason:   "all routes exhausted",
		}); ok {
			t.Fatalf("status %q became visible", status)
		}
	}
}
