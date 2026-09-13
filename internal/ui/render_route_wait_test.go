package ui

import (
	"strings"
	"testing"

	"nabd/internal/agent"
)

// TestRenderEvent_ProviderRoute_Waiting checks the UI side of the waiting
// notice: the badge is owned by the renderer, the text by presentation.
func TestRenderEvent_ProviderRoute_Waiting(t *testing.T) {
	ev := agent.Event{
		Seq:  20,
		Type: agent.EventProviderRoute,
		Route: &agent.ProviderRoute{
			Status:   "waiting",
			Provider: "groq",
			Model:    "openai/gpt-oss-120b",
			Attempt:  2,
			Reason:   "retry-after 20s",
			StreamID: "stream-secret-xyz",
		},
	}
	out := RenderEvent(ev, 120)
	if out == "" {
		t.Fatal("waiting route event rendered empty")
	}
	if !strings.Contains(out, "\u2691") {
		t.Errorf("missing notice badge: %q", out)
	}
	if !strings.Contains(out, "waiting before retry: retry-after 20s") {
		t.Errorf("missing waiting text: %q", out)
	}
	if strings.Contains(out, "stream-secret-xyz") {
		t.Errorf("stream ID leaked: %q", out)
	}
}

// TestFeed_ProviderRoute_WaitingReachesFeed proves the notice survives the
// projector and lands in the feed the user actually reads.
func TestFeed_ProviderRoute_WaitingReachesFeed(t *testing.T) {
	f := NewFeed()
	f.width = 80
	f.height = 20

	_, _ = f.Update(agentEventBatchMsg{Events: []agent.Event{
		{Seq: 1, Type: agent.RunStart, Text: "run started"},
		{Seq: 2, Type: agent.EventProviderRoute, Route: &agent.ProviderRoute{
			Status:   "waiting",
			Provider: "groq",
			Model:    "openai/gpt-oss-120b",
			Attempt:  2,
			Reason:   "retry-after 20s",
		}},
	}})

	if len(linesContaining(f.lines, "waiting before retry: retry-after 20s")) != 1 {
		t.Fatalf("waiting notice missing from feed:\n%s", strings.Join(f.lines, "\n"))
	}
}
