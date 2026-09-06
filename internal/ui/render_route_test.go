package ui

import (
	"strings"
	"testing"

	"nabd/internal/agent"
)

// TestRenderEvent_ProviderRoute_Failed tests rendering a failed provider route in Classic/Replay UI.
func TestRenderEvent_ProviderRoute_Failed(t *testing.T) {
	ev := agent.Event{
		Seq:  10,
		Type: agent.EventProviderRoute,
		Route: &agent.ProviderRoute{
			Status:   "failed",
			Provider: "anthropic",
			Model:    "claude-3-5-sonnet",
			Attempt:  1,
			Reason:   "429 rate limit exceeded",
		},
	}
	// Test at wide width where no line-wrap occurs
	outWide := RenderEvent(ev, 120)
	if outWide == "" {
		t.Fatal("expected visible output for failed route event, got empty string")
	}
	if !strings.Contains(outWide, "⚑") {
		t.Errorf("expected notice badge '⚑' in rendered output, got: %q", outWide)
	}
	expected := "route failed: anthropic/claude-3-5-sonnet (attempt 1): 429 rate limit exceeded"
	if !strings.Contains(outWide, expected) {
		t.Errorf("missing expected formatted notice text in wide output: %q", outWide)
	}

	// Test at DefaultWidth to verify wrapping indentation
	outNarrow := RenderEvent(ev, DefaultWidth)
	if !strings.Contains(outNarrow, "⚑") {
		t.Errorf("expected notice badge '⚑' in narrow output, got: %q", outNarrow)
	}
	if !strings.Contains(outNarrow, "route failed: anthropic/claude-3-5-sonnet") {
		t.Errorf("missing first line in narrow output: %q", outNarrow)
	}
	if !strings.Contains(outNarrow, "(attempt 1): 429 rate limit exceeded") {
		t.Errorf("missing second line in narrow output: %q", outNarrow)
	}
}

// TestRenderEvent_ProviderRoute_FallbackSelected tests rendering a fallback selected route in Classic/Replay UI.
func TestRenderEvent_ProviderRoute_FallbackSelected(t *testing.T) {
	ev := agent.Event{
		Seq:  11,
		Type: agent.EventProviderRoute,
		Route: &agent.ProviderRoute{
			Status:   "selected",
			Provider: "openrouter",
			Model:    "deepseek/deepseek-chat",
			Attempt:  2,
			Reason:   "should not be shown",
			StreamID: "stream-secret-xyz",
		},
	}
	outWide := RenderEvent(ev, 120)
	if outWide == "" {
		t.Fatal("expected visible output for fallback selected route event, got empty string")
	}
	if !strings.Contains(outWide, "⚑") {
		t.Errorf("expected notice badge '⚑' in rendered output, got: %q", outWide)
	}
	expected := "route selected: openrouter/deepseek/deepseek-chat (attempt 2)"
	if !strings.Contains(outWide, expected) {
		t.Errorf("missing expected formatted notice text in output: %q", outWide)
	}
	if strings.Contains(outWide, "should not be shown") {
		t.Errorf("reason leaked in selected route notice: %q", outWide)
	}
	if strings.Contains(outWide, "stream-secret-xyz") {
		t.Errorf("stream ID leaked in selected route notice: %q", outWide)
	}
}

// TestRenderEvent_ProviderRoute_HiddenCases asserts that non-observable route events render to empty string.
func TestRenderEvent_ProviderRoute_HiddenCases(t *testing.T) {
	cases := []struct {
		name  string
		event agent.Event
	}{
		{
			name: "selected attempt 1",
			event: agent.Event{
				Type: agent.EventProviderRoute,
				Route: &agent.ProviderRoute{
					Status:   "selected",
					Provider: "anthropic",
					Model:    "claude-3-5-sonnet",
					Attempt:  1,
				},
			},
		},
		{
			name: "selected attempt 0",
			event: agent.Event{
				Type: agent.EventProviderRoute,
				Route: &agent.ProviderRoute{
					Status:   "selected",
					Provider: "anthropic",
					Model:    "claude-3-5-sonnet",
					Attempt:  0,
				},
			},
		},
		{
			name: "selected negative attempt",
			event: agent.Event{
				Type: agent.EventProviderRoute,
				Route: &agent.ProviderRoute{
					Status:   "selected",
					Provider: "anthropic",
					Model:    "claude-3-5-sonnet",
					Attempt:  -1,
				},
			},
		},
		{
			name: "attempted status",
			event: agent.Event{
				Type: agent.EventProviderRoute,
				Route: &agent.ProviderRoute{
					Status:   "attempted",
					Provider: "anthropic",
					Model:    "claude-3-5-sonnet",
					Attempt:  1,
				},
			},
		},
		{
			name: "exhausted status",
			event: agent.Event{
				Type: agent.EventProviderRoute,
				Route: &agent.ProviderRoute{
					Status:   "exhausted",
					Provider: "anthropic",
					Model:    "claude-3-5-sonnet",
					Attempt:  3,
					Reason:   "all routes exhausted",
				},
			},
		},
		{
			name: "nil route pointer",
			event: agent.Event{
				Type:  agent.EventProviderRoute,
				Route: nil,
			},
		},
		{
			name: "unknown status",
			event: agent.Event{
				Type: agent.EventProviderRoute,
				Route: &agent.ProviderRoute{
					Status:   "unknown_status",
					Provider: "anthropic",
					Model:    "claude-3-5-sonnet",
					Attempt:  2,
				},
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out := RenderEvent(tc.event, DefaultWidth)
			if out != "" {
				t.Fatalf("expected empty render output for hidden case, got: %q", out)
			}
		})
	}
}

// TestRenderEvent_ProviderRoute_Redaction verifies that sensitive tokens in route reasons are sanitized before rendering.
func TestRenderEvent_ProviderRoute_Redaction(t *testing.T) {
	ev := agent.Event{
		Seq:  12,
		Type: agent.EventProviderRoute,
		Route: &agent.ProviderRoute{
			Status:   "failed",
			Provider: "openrouter",
			Model:    "anthropic/claude-3",
			Attempt:  1,
			Reason:   "upstream error: Bearer sk-ant-api03-abcdef0123456789abcdef0123456789",
		},
	}
	out := RenderEvent(ev, 120)
	if strings.Contains(out, "sk-ant-api03-") {
		t.Fatalf("secret leaked in rendered event: %q", out)
	}
	if !strings.Contains(out, "[REDACTED]") {
		t.Fatalf("expected [REDACTED] placeholder in rendered output, got: %q", out)
	}
}

// TestFeed_ProviderRoute_Integration verifies end-to-end event batch processing in Feed.
func TestFeed_ProviderRoute_Integration(t *testing.T) {
	f := NewFeed()
	f.width = 80
	f.height = 20

	batch := []agent.Event{
		{Seq: 1, Type: agent.RunStart, Text: "run started"},
		{Seq: 2, Type: agent.EventProviderRoute, Route: &agent.ProviderRoute{
			Status:   "attempted",
			Provider: "anthropic",
			Model:    "claude-3-5-sonnet",
			Attempt:  1,
		}},
		{Seq: 3, Type: agent.EventProviderRoute, Route: &agent.ProviderRoute{
			Status:   "failed",
			Provider: "anthropic",
			Model:    "claude-3-5-sonnet",
			Attempt:  1,
			Reason:   "429 Too Many Requests",
		}},
		{Seq: 4, Type: agent.EventProviderRoute, Route: &agent.ProviderRoute{
			Status:   "selected",
			Provider: "openrouter",
			Model:    "deepseek-v3",
			Attempt:  2,
		}},
	}

	_, _ = f.Update(agentEventBatchMsg{Events: batch})

	failedLines := linesContaining(f.lines, "route failed: anthropic/claude-3-5-sonnet (attempt 1): 429 Too Many Requests")
	if len(failedLines) != 1 {
		t.Fatalf("expected exactly 1 failed route line in feed, got %d:\n%s", len(failedLines), strings.Join(f.lines, "\n"))
	}
	if !strings.Contains(failedLines[0], "⚑") {
		t.Errorf("expected notice badge '⚑' on failed line in feed, got: %q", failedLines[0])
	}

	selectedLines := linesContaining(f.lines, "route selected: openrouter/deepseek-v3 (attempt 2)")
	if len(selectedLines) != 1 {
		t.Fatalf("expected exactly 1 selected fallback line in feed, got %d:\n%s", len(selectedLines), strings.Join(f.lines, "\n"))
	}
	if !strings.Contains(selectedLines[0], "⚑") {
		t.Errorf("expected notice badge '⚑' on selected fallback line in feed, got: %q", selectedLines[0])
	}

	attemptedLines := linesContaining(f.lines, "attempted")
	if len(attemptedLines) > 0 {
		t.Fatalf("feed contains unprojected attempted route event: %v", attemptedLines)
	}
}
