package agent_test

import (
	"encoding/json"
	"testing"

	"nabd/internal/agent"
)

// TestOrphanToolEndReconstructed: old archives (and sessions continued
// before the tool-pairing fix) can carry a ToolEnd whose ToolStart was never
// journaled. The consumer must synthesize the missing tool_use so the next
// request has one call answered by one result — calls == 1 && results == 1 —
// and must keep the error text whole: dropping it hides from the model that
// the tool is unknown and it will re-invoke it forever.
func TestOrphanToolEndReconstructed(t *testing.T) {
	evs := []agent.Event{
		{Seq: 1, Type: agent.UserMsg, Text: "run bash"},
		// No ToolStart for this call — orphan.
		{Seq: 2, Parent: 1, Type: agent.ToolEnd, Call: &agent.ToolCall{
			ID: "call_bash", Name: "bash",
			Output: "refused to run bash: unknown tool", OK: false,
		}},
		{Seq: 3, Parent: 2, Type: agent.TurnEnd},
	}
	ms := agent.Messages(evs)

	var calls, results int
	for _, m := range ms {
		calls += len(m.ToolCalls)
		results += len(m.ToolResults)
	}
	if calls != 1 || results != 1 {
		t.Fatalf("orphan ToolEnd must yield calls==1 && results==1, got calls=%d results=%d", calls, results)
	}
	// The reconstructed call must carry the identity and the result keep the
	// full error text.
	var output string
	for _, m := range ms {
		for _, r := range m.ToolResults {
			output = r.Output
		}
	}
	if output != "refused to run bash: unknown tool" {
		t.Fatalf("error text must be preserved verbatim, got %q", output)
	}
}

// TestToolCallIDsUniquePerMessage: within a single built message the
// tool_call_id values must be unique. A duplicated ID breaks the provider
// pairing invariant (two results answered by one call).
func TestToolCallIDsUniquePerMessage(t *testing.T) {
	evs := []agent.Event{
		{Seq: 1, Type: agent.UserMsg, Text: "call twice"},
		{Seq: 2, Parent: 1, Type: agent.ToolStart, Call: &agent.ToolCall{ID: "dup", Name: "read_file", Args: json.RawMessage(`{"path":"a.go"}`)}},
		{Seq: 3, Parent: 2, Type: agent.ToolStart, Call: &agent.ToolCall{ID: "dup", Name: "read_file", Args: json.RawMessage(`{"path":"b.go"}`)}},
		{Seq: 4, Parent: 3, Type: agent.ToolEnd, Call: &agent.ToolCall{ID: "dup", Name: "read_file", Output: "a", OK: true}},
		{Seq: 5, Parent: 4, Type: agent.ToolEnd, Call: &agent.ToolCall{ID: "dup", Name: "read_file", Output: "b", OK: true}},
		{Seq: 6, Parent: 5, Type: agent.TurnEnd},
	}
	ms := agent.Messages(evs)
	for _, m := range ms {
		seen := map[string]bool{}
		for _, c := range m.ToolCalls {
			if seen[c.ID] {
				t.Fatalf("duplicate tool_call_id %q in one message", c.ID)
			}
			seen[c.ID] = true
		}
	}
}
