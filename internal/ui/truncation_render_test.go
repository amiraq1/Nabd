package ui

import (
	"strings"
	"testing"

	"nabd/internal/agent"
)

// TestToolEndStatesTheLoss puts the cut where it is read. The in-payload
// marker sits at the bottom of the output, and the feed keeps only the last
// few lines of that output, so the marker can be clipped away by the very
// truncation it describes.
func TestToolEndStatesTheLoss(t *testing.T) {
	out := RenderEvent(agent.Event{
		Type: agent.ToolEnd,
		Call: &agent.ToolCall{
			ID:             "call_1",
			Name:           "bash",
			Output:         "last line of a long log",
			OK:             true,
			MS:             1200,
			TruncatedBytes: 4096,
		},
	}, 66)

	if !strings.Contains(out, "4096") {
		t.Errorf("cut size missing from the tool row: %q", out)
	}
	if !strings.Contains(out, "✂") {
		t.Errorf("truncation glyph missing from the tool row: %q", out)
	}
}

func TestToolEndSaysNothingWhenNothingWasCut(t *testing.T) {
	out := RenderEvent(agent.Event{
		Type: agent.ToolEnd,
		Call: &agent.ToolCall{ID: "call_1", Name: "glob", Output: "three files", OK: true},
	}, 66)
	if strings.Contains(out, "✂") {
		t.Fatalf("clean result claims truncation: %q", out)
	}
}

func TestTruncatedToolRowRespectsWidth(t *testing.T) {
	for _, width := range []int{20, 40, 66, 80} {
		out := RenderEvent(agent.Event{
			Type: agent.ToolEnd,
			Call: &agent.ToolCall{
				ID:             "call_1",
				Name:           "bash",
				Output:         "tail",
				OK:             true,
				MS:             1200,
				TruncatedBytes: 16384,
			},
		}, width)
		for _, line := range strings.Split(out, "\n") {
			if got := lineWidth(line); got > width {
				t.Fatalf("width %d: line %q is %d cells wide", width, line, got)
			}
		}
	}
}
