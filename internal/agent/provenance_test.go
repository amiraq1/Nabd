package agent

import (
	"slices"
	"testing"
)

func TestUncorroboratedReadLinesUsesToolProvenance(t *testing.T) {
	evs := []Event{
		{Seq: 1, Type: UserMsg, Text: "read"},
		{Seq: 2, Parent: 1, Type: ToolEnd, Call: &ToolCall{ID: "r1", Name: "read_file", OK: true, Output: "10|real line"}},
		{Seq: 3, Parent: 2, Type: TextDelta, Text: "10|real line\n11|fabricated"},
	}
	got := UncorroboratedReadLines(evs)
	if !slices.Equal(got, []string{"11|fabricated"}) {
		t.Fatalf("uncorroborated lines = %q", got)
	}
}
