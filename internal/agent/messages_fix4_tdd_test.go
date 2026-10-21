package agent

import (
	"encoding/json"
	"nabd/internal/event"
	"strings"
	"testing"
)

// TestMessagesPreservesSequentialSlices guards the FIX 4 regression: two
// consecutive read_file results for the SAME path at DIFFERENT offsets must
// BOTH be delivered to the model. The previous read-dedup collapsed them to
// one, silently dropping the first slice (README 54-87 hole in session 62).
func TestMessagesPreservesSequentialSlices(t *testing.T) {
	// read_file main.go offset=1  -> lines 1-53
	// read_file main.go offset=54 -> lines 54-87
	evs := []event.Event{
		{Seq: 1, Type: event.UserMsg, Text: "read main.go in two slices"},
		{Seq: 2, Parent: 1, Type: event.ToolStart, Call: &event.ToolCall{ID: "c1", Name: "read_file", Args: json.RawMessage(`{"path":"main.go","offset":1}`)}},
		{Seq: 3, Parent: 2, Type: event.ToolStart, Call: &event.ToolCall{ID: "c2", Name: "read_file", Args: json.RawMessage(`{"path":"main.go","offset":54}`)}},
		{Seq: 4, Parent: 3, Type: event.ToolEnd, Call: &event.ToolCall{ID: "c1", Name: "read_file", Output: "lines 1-53 of main.go", OK: true}},
		{Seq: 5, Parent: 4, Type: event.ToolEnd, Call: &event.ToolCall{ID: "c2", Name: "read_file", Output: "lines 54-87 of main.go", OK: true}},
		{Seq: 6, Parent: 5, Type: event.TurnEnd},
	}
	ms := Messages(evs)
	var results int
	var combined strings.Builder
	for _, m := range ms {
		results += len(m.ToolResults)
		for _, r := range m.ToolResults {
			combined.WriteString(r.Output)
		}
	}
	if results != 2 {
		t.Fatalf("expected 2 preserved read slices, got %d (FIX 4 dedup dropped the first slice)", results)
	}
	all := combined.String()
	if !strings.Contains(all, "lines 1-53") || !strings.Contains(all, "lines 54-87") {
		t.Fatalf("expected both slices fully preserved, got %q", all)
	}
}
