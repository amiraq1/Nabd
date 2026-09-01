package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"nabd/internal/provider"
)

func TestBoundaryIsAlwaysUserMessage(t *testing.T) {
	live2 := []Event{
		{Seq: 1, Type: UserMsg, Text: strings.Repeat("a", 1000)},
		{Seq: 2, Parent: 1, Type: TurnEnd},
		{Seq: 3, Parent: 2, Type: UserMsg, Text: "b"},
	}
	// target is 100, which means we must cut at the start of "a" or "b".
	// "a" is too big. So it will cut at "b".
	seq, _, ok := chooseBoundary(live2, 100)
	if !ok {
		t.Fatal("expected to find a boundary")
	}
	if seq != 3 {
		t.Fatalf("expected boundary at 3, got %d", seq)
	}
}

func TestCompactKeepsPairing(t *testing.T) {
	l := &Loop{Budget: NewBudget()}
	for i := 0; i < 6; i++ {
		id := fmt.Sprintf("t%d", i)
		l.emit(Event{Type: UserMsg, Text: strings.Repeat("سؤال طويل ", 200)})
		l.emit(Event{Type: ToolStart, Call: &ToolCall{ID: id, Name: "read_file"}})
		l.emit(Event{Type: ToolEnd, Call: &ToolCall{ID: id, OK: true,
			Output: strings.Repeat("سطر\n", 500)}})
		l.emit(Event{Type: TurnEnd})
	}
	if err := l.Compact(context.Background(), 2000); err != nil {
		t.Fatal(err)
	}
	ms := Messages(Live(l.hist))
	var calls, results int
	for _, m := range ms {
		calls += len(m.ToolCalls)
		results += len(m.ToolResults)
	}
	if calls != results {
		t.Fatalf("%d نداء مقابل %d نتيجة بعد الضغط", calls, results)
	}
	if Live(l.hist)[0].Type != Compact {
		t.Fatal("الفرع الحيّ لا يبدأ بالملخّص")
	}
}

func TestSqueezePreservesIDs(t *testing.T) {
	ms := []provider.Message{
		{Role: provider.User, ToolResults: []provider.ToolResult{
			{ID: "t1", Output: strings.Repeat("a", 1000), IsErr: false},
			{ID: "t2", Output: "error msg", IsErr: true},
		}},
		{Role: provider.User, Text: "user"},
		{Role: provider.User, Text: "user"},
		{Role: provider.User, Text: "user"},
		{Role: provider.User, Text: "user"}, // 4 users later => stale
	}
	sq := Squeeze(ms, 3)
	if len(sq[0].ToolResults) != 2 {
		t.Fatalf("expected 2 results, got %d", len(sq[0].ToolResults))
	}
	r1 := sq[0].ToolResults[0]
	r2 := sq[0].ToolResults[1]

	if r1.ID != "t1" || len(r1.Output) > 500 {
		t.Fatalf("t1 output not squeezed correctly: %v", r1.Output)
	}
	if r2.ID != "t2" || r2.Output != "error msg" {
		t.Fatalf("t2 (error) was squeezed unexpectedly")
	}
}

// TestSqueezeStubsStaleReadResults: an old read_file result must become one
// line naming the range and path, while the tool_use pairing survives.
func TestSqueezeStubsStaleReadResults(t *testing.T) {
	ms := []provider.Message{
		{Role: provider.Assistant, ToolCalls: []provider.ToolCall{
			{ID: "r1", Name: "read_file", Input: json.RawMessage(`{"path":"big.go"}`)},
		}},
		{Role: provider.User, ToolResults: []provider.ToolResult{
			{ID: "r1", Output: "1|line one\n2|line two\n3|line three\n[TRUNCATED: stopped at line 3 of 300; use offset=4 to continue]\n", IsErr: false},
		}},
		{Role: provider.User, Text: "keep"},
		{Role: provider.User, Text: "u1"},
		{Role: provider.User, Text: "u2"},
		{Role: provider.User, Text: "u3"},
		{Role: provider.User, Text: "u4"}, // makes the first round stale
	}
	sq := Squeeze(ms, 3)
	if len(sq) != 7 {
		t.Fatalf("message count changed: %d", len(sq))
	}
	got := sq[1].ToolResults[0].Output
	if !strings.Contains(got, "قرأتُ الأسطر 1-3 من big.go") {
		t.Errorf("read stub must name range and path, got: %q", got)
	}
	// The tool_use (assistant message 0) keeps its call and path.
	if len(sq[0].ToolCalls) != 1 || sq[0].ToolCalls[0].ID != "r1" {
		t.Errorf("tool_use pairing lost: %+v", sq[0].ToolCalls)
	}
}

// TestReadRangeParsesTail: the range comes from the truncation tail.
func TestReadRangeParsesTail(t *testing.T) {
	out := "1|a\n2|b\n[TRUNCATED: stopped at line 3 of 300; use offset=4 to continue]\n"
	if got := readRange(out); got != "1-2" {
		t.Errorf("readRange=%q, want 1-2", got)
	}
	out2 := "10|x\n11|y\n12|z\n"
	if got := readRange(out2); got != "10-12" {
		t.Errorf("readRange=%q, want 10-12", got)
	}
}

func TestEstimateDoesNotUndercountArabic(t *testing.T) {
	eng := EstimateText("hello world this is english text")
	ar := EstimateText("مرحبا بالعالم هذا نص عربي")

	// Arabic should cost more tokens per character due to runesPerTokOther
	if ar < eng {
		t.Fatalf("Arabic estimate %d should be >= English estimate %d for same length", ar, eng)
	}
}

func TestSummaryFallsBackWithoutProvider(t *testing.T) {
	l := &Loop{} // No provider
	evs := []Event{
		{Seq: 1, Type: UserMsg, Text: "do a thing"},
	}
	sum := l.summarise(context.Background(), evs)
	if !strings.Contains(sum, "do a thing") {
		t.Fatalf("mechanical summary missing text: %s", sum)
	}
}
