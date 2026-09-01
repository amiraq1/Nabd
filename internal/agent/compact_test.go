package agent

import (
	"context"
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
