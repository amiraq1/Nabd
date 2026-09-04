package agent

import (
	"context"
	"errors"
	"sync"
	"testing"

	"nabd/internal/provider"
)

type mockProvider struct {
	chunks []provider.Chunk
}

func (m mockProvider) Name() string { return "mock" }

func (m mockProvider) Stream(ctx context.Context, req provider.Request) (<-chan provider.Chunk, error) {
	ch := make(chan provider.Chunk, len(m.chunks))
	for _, c := range m.chunks {
		ch <- c
	}
	close(ch)
	return ch, nil
}

type mockTools struct {
	allowed bool
}

func (m mockTools) Specs() []provider.ToolSpec {
	// The gate tests model a KNOWN tool going through the permission
	// ladder; the loop intercepts unknown tools before the gate.
	return []provider.ToolSpec{{Name: "test_tool"}}
}
func (m mockTools) Check(tool string) (Verdict, string) {
	if m.allowed {
		return VerdictAsk, ""
	}
	return VerdictDeny, "mock deny"
}
func (m mockTools) Run(ctx context.Context, c provider.ToolCall) (string, bool, error) {
	return "mock output", true, nil
}
func (m mockTools) Record(string, Decision)                    {}
func (m mockTools) Effective(tool string, d Decision) Decision { return d }
func (m mockTools) Ask(ctx context.Context, c ToolCall) Decision {
	if m.allowed {
		return AllowOnce
	}
	return Deny
}

type mockSink struct {
	fn func(Event) error
}

func (m mockSink) Emit(e Event) error {
	if m.fn != nil {
		return m.fn(e)
	}
	return nil
}

func TestToolStartEmission(t *testing.T) {
	tests := []struct {
		name      string
		allowed   bool
		wantStart bool
	}{
		{"RejectedTool", false, true},
		{"AcceptedTool", true, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			l := &Loop{
				Provider: mockProvider{
					chunks: []provider.Chunk{
						{Kind: provider.ChunkToolCall, Call: &provider.ToolCall{ID: "call_1", Name: "test_tool"}},
						{Kind: provider.ChunkStop, Stop: "tool_calls"},
					},
				},
				Tools:  mockTools{allowed: tt.allowed},
				Budget: &Budget{},
				Gate:   &mockTools{allowed: tt.allowed},
				Human:  &mockTools{allowed: tt.allowed},
			}

			var events []Event
			l.Sink = mockSink{fn: func(e Event) error {
				events = append(events, e)
				return nil
			}}

			_ = l.Run(context.Background(), "test")

			startSeen := false
			for _, e := range events {
				if e.Type == ToolStart {
					startSeen = true
				}
			}

			if !startSeen {
				t.Errorf("ToolStart should always be emitted, even before PermReply")
			}
		})
	}
}

func TestToolPairing(t *testing.T) {
	cases := []struct {
		name     string
		toolName string
		allowed  bool
	}{
		{"UnknownTool", "unknown_tool", true},
		{"DeniedTool", "test_tool", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			l := &Loop{
				Provider: mockProvider{
					chunks: []provider.Chunk{
						{Kind: provider.ChunkToolCall, Call: &provider.ToolCall{ID: "call_pair", Name: tc.toolName}},
						{Kind: provider.ChunkStop, Stop: "tool_calls"},
					},
				},
				Tools:  mockTools{allowed: tc.allowed},
				Budget: &Budget{},
				Gate:   &mockTools{allowed: tc.allowed},
				Human:  &mockTools{allowed: tc.allowed},
			}

			var events []Event
			l.Sink = mockSink{fn: func(e Event) error {
				events = append(events, e)
				return nil
			}}

			_ = l.Run(context.Background(), "test")

			var endFound bool
			for i, e := range events {
				if e.Type == ToolEnd {
					endFound = true
					startFound := false
					for j := i - 1; j >= 0; j-- {
						if events[j].Type == ToolStart && events[j].Call != nil && events[j].Call.ID == e.Call.ID {
							startFound = true
							break
						}
					}
					if !startFound {
						t.Errorf("ToolEnd at index %d has no preceding ToolStart", i)
					}
				}
			}
			if !endFound {
				t.Fatalf("expected ToolEnd event for %s, got none", tc.name)
			}
		})
	}
}

// errSink fails after a configurable number of successful emits, then
// returns the injected error. Used to test that sink failures stop the loop.
type errSink struct {
	mu        sync.Mutex
	emitErr   error
	allow     int
	emitCount int
}

func (s *errSink) Emit(e Event) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.emitCount >= s.allow {
		return s.emitErr
	}
	s.emitCount++
	return nil
}

// TestSinkFailureStopsStart proves that a journal failure during Start()
// propagates to the caller instead of being swallowed.
func TestSinkFailureStopsStart(t *testing.T) {
	p := &retryProvider{failFirst: false}
	sink := &errSink{allow: 0, emitErr: errors.New("disk full")}
	l := &Loop{
		Provider: p,
		Sink:     sink,
		Budget:   NewBudget(),
	}
	if err := l.Start("nabd test"); err == nil {
		t.Fatal("expected error from Start() when sink fails, got nil")
	}
}

// TestSinkFailureStopsRunOnUserMsg proves that a sink failure during the
// initial UserMsg stops Run() before any turn executes.
func TestSinkFailureStopsRunOnUserMsg(t *testing.T) {
	p := &retryProvider{failFirst: false}
	sink := &errSink{allow: 0, emitErr: errors.New("disk full")}
	l := &Loop{
		Provider: p,
		Sink:     sink,
		Budget:   NewBudget(),
	}
	if err := l.Run(context.Background(), "hi"); err == nil {
		t.Fatal("expected error from Run() when sink fails on UserMsg")
	}
}

// TestSinkFailureStopsTurnOnTextDelta proves that a sink failure while
// emitting a text delta stops the turn and propagates to Run().
func TestSinkFailureStopsTurnOnTextDelta(t *testing.T) {
	p := &textAfterRateLimitProvider{}
	// Allow RunStart + UserMsg + TurnStart to succeed, then fail.
	sink := &errSink{allow: 3, emitErr: errors.New("disk full")}
	l := &Loop{
		Provider: p,
		Sink:     sink,
		Budget:   NewBudget(),
	}
	if err := l.Run(context.Background(), "hi"); err == nil {
		t.Fatal("expected error from Run() when sink fails on TextDelta")
	}
}

// textAfterRateLimitProvider emits one 429 then a successful text response.
type textAfterRateLimitProvider struct{ calls int }

func (*textAfterRateLimitProvider) Name() string { return "text-after-rl" }

func (r *textAfterRateLimitProvider) Stream(ctx context.Context, req provider.Request) (<-chan provider.Chunk, error) {
	ch := make(chan provider.Chunk, 8)
	go func() {
		defer close(ch)
		r.calls++
		if r.calls == 1 {
			ch <- provider.Chunk{Kind: provider.ChunkRateLimit, RateLimit: &provider.RateLimitInfo{Code: 429, WaitSec: 0.001, Attempt: 1}}
			return
		}
		ch <- provider.Chunk{Kind: provider.ChunkText, Text: "ok"}
		ch <- provider.Chunk{Kind: provider.ChunkStop, Stop: "end_turn"}
	}()
	return ch, nil
}

// sinkFunc adapts a func to the agent.Sink interface.
type sinkFunc func(Event) error

func (f sinkFunc) Emit(e Event) error { return f(e) }

// TestFanoutOrderIsDeterministic proves that Fanout calls sinks in order
// and stops at the first error.
func TestFanoutOrderIsDeterministic(t *testing.T) {
	order := []int{}
	s1 := sinkFunc(func(e Event) error { order = append(order, 1); return nil })
	s2 := sinkFunc(func(e Event) error { order = append(order, 2); return nil })
	f := Fanout{s1, s2}
	_ = f.Emit(Event{})
	if len(order) != 2 || order[0] != 1 || order[1] != 2 {
		t.Fatalf("fanout order = %v, want [1 2]", order)
	}
	// Second sink should not be called if first fails.
	order = order[:0]
	sFail := sinkFunc(func(e Event) error { return errors.New("boom") })
	f = Fanout{sFail, s2}
	_ = f.Emit(Event{})
	if len(order) != 0 {
		t.Fatalf("second sink called after first failed: %v", order)
	}
}

// TestEndEmitsExactlyOneRunEnd verifies that Loop.End emits exactly one
// RunEnd event regardless of how many times it is called, and that the
// event carries the supplied text.
func TestEndEmitsExactlyOneRunEnd(t *testing.T) {
	sink := &recordSink{}
	l := &Loop{Sink: sink, Budget: NewBudget()}

	if err := l.End("first"); err != nil {
		t.Fatalf("End: %v", err)
	}
	if err := l.End("second"); err != nil {
		t.Fatalf("End: %v", err)
	}
	if err := l.End("third"); err != nil {
		t.Fatalf("End: %v", err)
	}

	var runEndCount int
	var lastText string
	for _, e := range sink.evs {
		if e.Type == RunEnd {
			runEndCount++
			lastText = e.Text
		}
	}
	if runEndCount != 1 {
		t.Fatalf("expected exactly 1 RunEnd, got %d", runEndCount)
	}
	if lastText != "first" {
		t.Errorf("RunEnd text = %q, want \"first\"", lastText)
	}
}
