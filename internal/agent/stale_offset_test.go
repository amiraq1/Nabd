package agent_test

import (
	"encoding/json"
	"testing"

	"nabd/internal/agent"
	"nabd/internal/provider"
)

// TestStaleOffsetsOfferedMetric: the structural replacement for the
// COMPACTION_REPLAY metric. FakeLLM behavior is deterministic and cannot
// represent real model choice, so "would the model re-read after
// compaction" is not measurable in a unit test. What IS measurable is what
// the request actually offers: offset directives pointing into territory a
// newer read of the same path already covered.
func TestStaleOffsetsOfferedMetric(t *testing.T) {
	var messages []provider.Message
	segs := []struct {
		id           string
		s, e, next   int
	}{
		{"r1", 1, 29, 30},
		{"r2", 30, 58, 59},
		{"r3", 59, 87, 88},
	}
	for _, s := range segs {
		messages = append(messages,
			provider.Message{Role: provider.Assistant, ToolCalls: []provider.ToolCall{
				{ID: s.id, Name: "read_file", Input: json.RawMessage(`{"path":"big.go"}`)},
			}},
			provider.Message{Role: provider.User, ToolResults: []provider.ToolResult{
				{ID: s.id, Output: fakeReadResult("big.go", s.s, s.e, 300, s.next)},
			}},
		)
	}

	// Raw (unfolded) history: the defect is visible. Offsets 30 and 59
	// point into territory the newest read (1-87) already covers.
	if got := len(agent.OfferedOffsets(messages)); got != 3 {
		t.Fatalf("raw offered=%d, want 3", got)
	}
	stale := agent.StaleOffsetsOffered(messages)
	if len(stale) != 2 || stale[0].Offset != 30 || stale[1].Offset != 59 {
		t.Fatalf("raw STALE_OFFSET_OFFERED=%+v, want offsets [30 59] (covered by the newest read 1-87)", stale)
	}
	t.Logf("RAW: offered=3 stale=2 (offsets %d, %d already covered)", stale[0].Offset, stale[1].Offset)

	// After the fold Run actually sends: zero stale offers, one live tail.
	sq := agent.Squeeze(messages, 5)
	if got := agent.StaleOffsetsOffered(sq); len(got) != 0 {
		t.Fatalf("STALE_OFFSET_OFFERED=%d after fold, want 0: %+v", len(got), got)
	}
	if got := len(agent.OfferedOffsets(sq)); got != 1 {
		t.Fatalf("offered=%d after fold, want exactly 1", got)
	}
	t.Logf("STALE_OFFSET_OFFERED=0 · OFFERED=1 (offset=88, newest)")
}

// TestStaleOffsetDetectsReread: a model-side re-read with a wider window
// leaves the older offer pointing into covered territory — the metric
// catches the loop even when every read was truncated. (A verbatim
// duplicate re-read — same range, same offered offset — is not staleness;
// it is the one-live-tail-per-path defect, caught by the dedupe gate.)
func TestStaleOffsetDetectsReread(t *testing.T) {
	var messages []provider.Message
	// r2 re-reads the same file with a wider window: 1-58, next=59. The
	// older offer (offset=30) now points into the newer read's range.
	segs := []struct {
		id         string
		s, e, next int
	}{
		{"r1", 1, 29, 30},
		{"r2", 1, 58, 59},
	}
	for _, s := range segs {
		messages = append(messages,
			provider.Message{Role: provider.Assistant, ToolCalls: []provider.ToolCall{
				{ID: s.id, Name: "read_file", Input: json.RawMessage(`{"path":"big.go"}`)},
			}},
			provider.Message{Role: provider.User, ToolResults: []provider.ToolResult{
				{ID: s.id, Output: fakeReadResult("big.go", s.s, s.e, 300, s.next)},
			}},
		)
	}
	stale := agent.StaleOffsetsOffered(messages)
	if len(stale) != 1 || stale[0].Offset != 30 {
		t.Fatalf("re-read history: STALE_OFFSET_OFFERED=%+v, want exactly the older offer (offset=30)", stale)
	}
	t.Logf("REREAD DETECTED: older offer offset=30 is stale against the newer 1-58 read")
}
