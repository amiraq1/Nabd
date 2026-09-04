package agent_test

import (
	"encoding/json"
	"strings"
	"testing"

	"nabd/internal/agent"
	"nabd/internal/provider"
)

// TestOneLiveTailPerPath: consecutive truncated reads of one path leave
// several "continue with offset=" directives in one history — a model that
// follows an older tail re-reads an already-covered range. This is the C
// exposure: duplicated tails, not wrong arithmetic. The fold must keep
// exactly one live tail per path and flatten the older ones to the same
// stub the fold uses, so the newest offset is the only one to follow.
func TestOneLiveTailPerPath(t *testing.T) {
	var messages []provider.Message
	rounds := []struct {
		id                string
		start, end, total int
		next              int
	}{
		{"r1", 1, 29, 300, 30},
		{"r2", 30, 58, 300, 59},
		{"r3", 59, 87, 300, 88},
	}
	for _, r := range rounds {
		messages = append(messages,
			provider.Message{Role: provider.Assistant, ToolCalls: []provider.ToolCall{
				{ID: r.id, Name: "read_file", Input: json.RawMessage(`{"path":"big.go"}`)},
			}},
			provider.Message{Role: provider.User, ToolResults: []provider.ToolResult{
				{ID: r.id, Output: fakeReadResult("big.go", r.start, r.end, r.total, r.next)},
			}},
		)
	}
	// A different file's tail must survive untouched.
	messages = append(messages,
		provider.Message{Role: provider.Assistant, ToolCalls: []provider.ToolCall{
			{ID: "o1", Name: "read_file", Input: json.RawMessage(`{"path":"other.go"}`)},
		}},
		provider.Message{Role: provider.User, ToolResults: []provider.ToolResult{
			{ID: "o1", Output: fakeReadResult("other.go", 1, 10, 50, 11)},
		}},
	)
	messages = append(messages, provider.Message{Role: provider.User, Text: "continue"})

	// keepRounds=5: nothing here is stale, so the ONLY mechanism that may
	// flatten an older tail is the dedupe, not the stale-fold.
	sq := agent.Squeeze(messages, 5)

	// path by ID: the read result body does not name the path, so the
	// classification must come from the call that produced the result.
	byPath := map[string]string{"r1": "big.go", "r2": "big.go", "r3": "big.go", "o1": "other.go"}
	var liveTails, otherTails int
	survivingTail := ""
	for _, m := range sq {
		for _, tr := range m.ToolResults {
			if !strings.Contains(tr.Output, "[TRUNCATED:") {
				continue
			}
			if byPath[tr.ID] == "other.go" {
				otherTails++
				continue
			}
			liveTails++
			survivingTail = tr.Output
		}
	}
	if liveTails != 1 {
		t.Fatalf("big.go has %d live tails in one history — competing offset directives", liveTails)
	}
	if !strings.Contains(survivingTail, "offset=88") {
		t.Errorf("the surviving tail must be the newest (offset=88), got %q", survivingTail)
	}
	if otherTails != 1 {
		t.Errorf("unrelated path tail must survive untouched, got %d", otherTails)
	}

	// Pairing survives: every tool_use still has its result, same ID.
	ids := map[string]bool{}
	stubs := 0
	for _, m := range sq {
		for _, tr := range m.ToolResults {
			ids[tr.ID] = true
			if strings.Contains(tr.Output, "content squeezed; do not re-read") &&
				strings.Contains(tr.Output, "big.go") {
				stubs++
				if !strings.Contains(tr.Output, "read lines") {
					t.Errorf("flattened stub lost the range: %q", tr.Output)
				}
			}
		}
	}
	for _, id := range []string{"r1", "r2", "r3", "o1"} {
		if !ids[id] {
			t.Errorf("tool_result %s lost from history", id)
		}
	}
	if stubs != 2 {
		t.Errorf("expected 2 flattened stubs for big.go, got %d", stubs)
	}
	t.Logf("ONE_LIVE_TAIL: big.go offset=88 · FLATTENED=%d · other.go untouched", stubs)
}
