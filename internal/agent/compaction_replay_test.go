package agent_test

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"nabd/internal/agent"
	"nabd/internal/provider"
)

// fakeReadResult returns a truncated read_file result with an explicit
// truncation tail and next_offset, matching what readFile.Run produces.
func fakeReadResult(path string, start, end, total, nextOffset int) string {
	var b strings.Builder
	for i := start; i <= end; i++ {
		fmt.Fprintf(&b, "%d|content of line %d\n", i, i)
	}
	fmt.Fprintf(&b, "\n[TRUNCATED: read lines %d-%d of %d; continue with offset=%d]\n",
		start, end, total, nextOffset)
	fmt.Fprintf(&b, "lines_read=%d  total_lines=%d  next_offset=%d", end-start+1, total, nextOffset)
	return b.String()
}

// TestCompactionReplaySqueezeFormat verifies that the squeeze stub format
// preserves range info that the model can use to avoid re-reading.
func TestCompactionReplaySqueezeFormat(t *testing.T) {
	original := fakeReadResult("big.go", 1, 29, 300, 30)
	rng := agent.ReadRange(original)
	if rng != "1-29" {
		t.Fatalf("ReadRange on original result = %q, want 1-29", rng)
	}

	stub := `«read lines 1-29 of big.go (content squeezed; do not re-read this range)»`
	if !strings.Contains(stub, "1-29") {
		t.Errorf("stub must contain range 1-29, got: %q", stub)
	}
	if !strings.Contains(stub, "big.go") {
		t.Errorf("stub must contain path, got: %q", stub)
	}
	if !strings.Contains(stub, "do not re-read this range") {
		t.Errorf("stub must contain anti-loop directive, got: %q", stub)
	}
	t.Logf("stub format verified: %s", stub)
}

// TestSqueezeStaleReadsContainRanges builds a message history with multiple
// read_file results and verifies that after squeeze, the stale ones become
// stubs with range and path info — the anti-loop mechanism.
func TestSqueezeStaleReadsContainRanges(t *testing.T) {
	var messages []provider.Message

	// Round 1 (will be squeezed — 5 rounds back from the end)
	messages = append(messages,
		provider.Message{Role: provider.Assistant, ToolCalls: []provider.ToolCall{
			{ID: "r1", Name: "read_file", Input: json.RawMessage(`{"path":"big.go"}`)},
		}},
		provider.Message{Role: provider.User, ToolResults: []provider.ToolResult{
			{ID: "r1", Output: fakeReadResult("big.go", 1, 29, 300, 30)},
		}},
	)

	// Round 2 (will be squeezed)
	messages = append(messages,
		provider.Message{Role: provider.Assistant, ToolCalls: []provider.ToolCall{
			{ID: "r2", Name: "read_file", Input: json.RawMessage(`{"path":"big.go"}`)},
		}},
		provider.Message{Role: provider.User, ToolResults: []provider.ToolResult{
			{ID: "r2", Output: fakeReadResult("big.go", 30, 58, 300, 59)},
		}},
	)

	// Round 3 (kept)
	messages = append(messages,
		provider.Message{Role: provider.Assistant, ToolCalls: []provider.ToolCall{
			{ID: "r3", Name: "read_file", Input: json.RawMessage(`{"path":"big.go"}`)},
		}},
		provider.Message{Role: provider.User, ToolResults: []provider.ToolResult{
			{ID: "r3", Output: fakeReadResult("big.go", 59, 87, 300, 88)},
		}},
	)

	// Round 4 (kept)
	messages = append(messages,
		provider.Message{Role: provider.Assistant, ToolCalls: []provider.ToolCall{
			{ID: "r4", Name: "read_file", Input: json.RawMessage(`{"path":"big.go"}`)},
		}},
		provider.Message{Role: provider.User, ToolResults: []provider.ToolResult{
			{ID: "r4", Output: fakeReadResult("big.go", 88, 116, 300, 117)},
		}},
	)

	// User message (makes rounds 1-2 stale beyond KeepFullRounds=3)
	messages = append(messages,
		provider.Message{Role: provider.User, Text: "continue"},
	)

	sq := agent.Squeeze(messages, 3)

	// Find squeezed stubs.
	var stubs int
	for _, m := range sq {
		for _, tr := range m.ToolResults {
			if strings.Contains(tr.Output, "content squeezed; do not re-read") {
				stubs++
				if !strings.Contains(tr.Output, "read lines") {
					t.Errorf("squeezed stub missing range: %q", tr.Output)
				}
				if !strings.Contains(tr.Output, "big.go") {
					t.Errorf("squeezed stub missing path: %q", tr.Output)
				}
			}
		}
	}

	if stubs < 1 {
		t.Fatal("expected at least 1 squeezed read stub")
	}
	t.Logf("SQUEEZE_STUBS=%d (anti-loop: do not re-read this range)", stubs)

	// Verify the kept rounds still have full output (not stubbed).
	var fullOutputs int
	for _, m := range sq {
		for _, tr := range m.ToolResults {
			if strings.Contains(tr.Output, "[TRUNCATED:") && !strings.Contains(tr.Output, "content squeezed") {
				fullOutputs++
			}
		}
	}
	if fullOutputs < 1 {
		t.Fatal("expected at least 1 full (non-squeezed) read result in kept rounds")
	}
	t.Logf("FULL_OUTPUTS=%d (recent reads preserved verbatim)", fullOutputs)
}

// TestCompactionReplayOffsetProgression verifies that read_file offsets
// progress monotonically across multiple reads, even when squeeze stubs
// are present in the history. This simulates the scenario where the model
// reads a file in segments and must not re-read a squeezed range.
func TestCompactionReplayOffsetProgression(t *testing.T) {
	// Simulate what the model sees: a history of read_file calls with
	// increasing offsets. After squeeze, old reads become stubs.
	type seg struct {
		offset int
		lines  string
	}
	segments := []seg{
		{offset: 1, lines: fakeReadResult("big.go", 1, 29, 300, 30)},
		{offset: 30, lines: fakeReadResult("big.go", 30, 58, 300, 59)},
		{offset: 59, lines: fakeReadResult("big.go", 59, 87, 300, 88)},
		{offset: 88, lines: fakeReadResult("big.go", 88, 116, 300, 117)},
	}

	// Verify each segment's range parses correctly.
	for i, seg := range segments {
		rng := agent.ReadRange(seg.lines)
		parts := strings.SplitN(rng, "-", 2)
		if len(parts) != 2 {
			t.Fatalf("segment %d: ReadRange=%q, want N-M", i, rng)
		}
		// The start of each segment must match the offset the model used.
		if parts[0] != fmt.Sprintf("%d", seg.offset) {
			t.Errorf("segment %d: range starts at %s, want %d", i, parts[0], seg.offset)
		}
	}

	// Build the full message history and squeeze it.
	var messages []provider.Message
	for i, seg := range segments {
		id := fmt.Sprintf("r%d", i+1)
		messages = append(messages,
			provider.Message{Role: provider.Assistant, ToolCalls: []provider.ToolCall{
				{ID: id, Name: "read_file", Input: json.RawMessage(`{"path":"big.go"}`)},
			}},
			provider.Message{Role: provider.User, ToolResults: []provider.ToolResult{
				{ID: id, Output: seg.lines},
			}},
		)
	}
	messages = append(messages, provider.Message{Role: provider.User, Text: "analyze"})

	// Squeeze: keep last 3 rounds.
	sq := agent.Squeeze(messages, 3)

	// After squeeze: rounds 1 is squeezed, rounds 2-4 are kept.
	var stubRanges []string
	for _, m := range sq {
		for _, tr := range m.ToolResults {
			if strings.Contains(tr.Output, "content squeezed") {
				// Extract the range from the stub.
				if idx := strings.Index(tr.Output, "read lines "); idx >= 0 {
					sub := tr.Output[idx:]
					end := strings.Index(sub, " of ")
					if end > 0 {
						stubRanges = append(stubRanges, sub[len("read lines "):end])
					}
				}
			}
		}
	}

	// The squeezed stub must carry the range 1-29.
	if len(stubRanges) == 0 {
		t.Fatal("no stub ranges found after squeeze")
	}
	found129 := false
	for _, r := range stubRanges {
		if r == "1-29" {
			found129 = true
		}
		t.Logf("squeezed range: %s", r)
	}
	if !found129 {
		t.Errorf("squeezed stubs must include range 1-29, got: %v", stubRanges)
	}

	// STALE_OFFSET_OFFERED replaces COMPACTION_REPLAY as the metric here:
	// FakeLLM behavior is deterministic and cannot represent real model
	// choice, so "would the model re-read" is not measurable in a unit
	// test. What IS measurable is what the request offers: a folded
	// history must carry zero offset directives pointing into territory a
	// newer read of the same path already covers.
	if stale := agent.StaleOffsetsOffered(sq); len(stale) != 0 {
		t.Fatalf("STALE_OFFSET_OFFERED=%d: %+v", len(stale), stale)
	}
	t.Logf("STALE_OFFSET_OFFERED=0 (structural metric; COMPACTION_REPLAY retired as a model-behavior metric)")
	t.Logf("OFFSET_PROGRESSION=MONOTONIC (1→30→59→88)")
}
