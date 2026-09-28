package agent

import (
	"nabd/internal/event"
	"slices"
	"testing"
)

func TestUncorroboratedReadLinesUsesToolProvenance(t *testing.T) {
	evs := []event.Event{
		{Seq: 1, Type: event.UserMsg, Text: "read"},
		{Seq: 2, Parent: 1, Type: event.ToolEnd, Call: &event.ToolCall{ID: "r1", Name: "read_file", OK: true, Output: "10|real line"}},
		{Seq: 3, Parent: 2, Type: event.TextDelta, Text: "10|real line\n11|fabricated"},
	}
	got := UncorroboratedReadLines(evs)
	if !slices.Equal(got, []string{"11|fabricated"}) {
		t.Fatalf("uncorroborated lines = %q", got)
	}
}
