package ui

import (
	"strings"
	"testing"

	"nabd/internal/agent"
)

func metaFeed(t *testing.T, width int) *Feed {
	t.Helper()
	f := NewFeed()
	f.width = width
	f.height = 24
	f.running = true
	_, _ = f.Update(agentEventBatchMsg{Events: []agent.Event{
		{Seq: 1, Type: agent.RunStart},
		{Seq: 2, Type: agent.TurnStart},
		{Seq: 3, Type: agent.EventProviderRoute, Route: &agent.ProviderRoute{
			Status: "selected", Provider: "nvidia", Model: "moonshotai/kimi-k2.6", Attempt: 2,
		}},
		{Seq: 4, Type: agent.EventProviderUsage, Usage: &agent.ProviderUsage{
			PromptTokens: 6000, CompletionTokens: 605,
		}},
	}})
	return f
}

// TestRuntimeStatusRowCarriesMeta is the E-series fix: the row used to say
// "Generating…" and nothing about turn, cost, or which route is serving.
func TestRuntimeStatusRowCarriesMeta(t *testing.T) {
	f := metaFeed(t, 100)
	line := f.computeLayout().runtimeStatusLine
	for _, want := range []string{"Generating…", "turn 1", "6.6k tok", "nvidia"} {
		if !strings.Contains(line, want) {
			t.Errorf("missing %q in status row %q", want, line)
		}
	}
}

// TestRuntimeStatusRowDegradesInsteadOfTruncating proves the phase text
// survives on a narrow phone terminal and metadata is dropped instead.
func TestRuntimeStatusRowDegradesInsteadOfTruncating(t *testing.T) {
	f := metaFeed(t, 24)
	line := f.computeLayout().runtimeStatusLine
	if !strings.Contains(line, "Generating…") {
		t.Fatalf("phase text lost on narrow width: %q", line)
	}
	if strings.Contains(line, "moonshotai") {
		t.Fatalf("long model name should have been dropped: %q", line)
	}
}

// TestRuntimeStatusRowStaysOneRow guards the layout contract: metadata is
// appended to the existing row and must not add chrome.
func TestRuntimeStatusRowStaysOneRow(t *testing.T) {
	for _, width := range []int{20, 24, 40, 66, 80, 120} {
		f := metaFeed(t, width)
		lm := f.computeLayout()
		if lm.RuntimeStatusRows != 1 {
			t.Fatalf("width %d: RuntimeStatusRows = %d, want 1", width, lm.RuntimeStatusRows)
		}
		if strings.ContainsAny(lm.runtimeStatusLine, "\n\r") {
			t.Fatalf("width %d: status row is multi-line: %q", width, lm.runtimeStatusLine)
		}
		if got := lineWidth(lm.runtimeStatusLine); got > max(width, minViewportWidth) {
			t.Fatalf("width %d: status row width = %d", width, got)
		}
	}
}

// TestStatusLineWithMetaKeepsBaseWhenNothingKnown asserts the row is untouched
// before any run has produced metadata.
func TestStatusLineWithMetaKeepsBaseWhenNothingKnown(t *testing.T) {
	f := NewFeed()
	f.width = 80
	f.height = 24
	if got := f.statusLineWithMeta("Working…", 78); got != "Working…" {
		t.Fatalf("status line = %q, want unchanged", got)
	}
}
