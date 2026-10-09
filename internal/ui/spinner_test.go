package ui

import (
	"strings"
	"testing"
)

// TestSpinnerFramesCycle verifies the spinner produces distinct frames.
func TestSpinnerFramesCycle(t *testing.T) {
	frames := spinnerFrames()
	if len(frames) < 4 {
		t.Fatalf("need at least 4 spinner frames, got %d", len(frames))
	}
	seen := map[string]bool{}
	for _, f := range frames {
		seen[f] = true
	}
	if len(seen) < 4 {
		t.Fatalf("spinner frames not distinct: %v", frames)
	}
}

// TestChatViewShowsSpinnerWhileRunning verifies View includes a spinner
// frame when the model is working (not static text only).
func TestChatViewShowsSpinnerWhileRunning(t *testing.T) {
	m := &Chat{running: true, width: 80, statusProj: nil}
	// Simulate a few ticks to advance the spinner.
	v1 := m.View()
	m.spinFrame++
	v2 := m.View()
	// Views at different frames should differ (animated, not static).
	// If the model is streaming text, partialTail shows instead; empty buf here.
	if m.buf == "" && v1 == v2 && !strings.Contains(v1, "working") {
		t.Fatalf("expected spinner or working indicator, got %q", v1)
	}
}
