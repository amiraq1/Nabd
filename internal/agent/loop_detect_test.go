package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"nabd/internal/provider"
)

// loopingMockProvider returns an identical tool call up to maxCalls times.
type loopingMockProvider struct {
	mu         sync.Mutex
	calls      int
	maxCalls   int
	toolName   string
	toolArgs   string
	receivedMs [][]provider.Message
}

func (p *loopingMockProvider) Name() string { return "looping-mock" }

func (p *loopingMockProvider) Stream(ctx context.Context, req provider.Request) (<-chan provider.Chunk, error) {
	p.mu.Lock()
	p.calls++
	callIdx := p.calls
	p.receivedMs = append(p.receivedMs, req.Messages)
	p.mu.Unlock()

	ch := make(chan provider.Chunk, 4)
	if callIdx <= p.maxCalls {
		ch <- provider.Chunk{
			Kind: provider.ChunkToolCall,
			Call: &provider.ToolCall{
				ID:    fmt.Sprintf("call_%d", callIdx),
				Name:  p.toolName,
				Input: json.RawMessage(p.toolArgs),
			},
		}
		ch <- provider.Chunk{
			Kind: provider.ChunkStop,
			Stop: "tool_calls",
		}
	} else {
		ch <- provider.Chunk{
			Kind: provider.ChunkText,
			Text: "done",
		}
		ch <- provider.Chunk{
			Kind: provider.ChunkStop,
			Stop: "end_turn",
		}
	}
	close(ch)
	return ch, nil
}

type staticMockTools struct {
	output string
	ok     bool
}

func (m *staticMockTools) Specs() []provider.ToolSpec {
	return []provider.ToolSpec{{Name: "read_file"}}
}
func (m *staticMockTools) Run(_ context.Context, _ provider.ToolCall) (string, bool, error) {
	return m.output, m.ok, nil
}
func (m *staticMockTools) Check(_ string) (Verdict, string)           { return VerdictAllow, "" }
func (m *staticMockTools) Record(_ string, _ Decision)                {}
func (m *staticMockTools) Effective(_ string, d Decision) Decision    { return d }
func (m *staticMockTools) Ask(_ context.Context, _ ToolCall) Decision { return AllowOnce }

type eventCollectorSink struct {
	mu     sync.Mutex
	events []Event
}

func (s *eventCollectorSink) Emit(e Event) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, e)
	return nil
}

// TestToolLoopNoticeAtThreeRepeats proves that repeating an identical tool call 3 times
// emits an advisory Notice into the journal and injects a user-role notice into the
// conversation history for the next turn.
func TestToolLoopNoticeAtThreeRepeats(t *testing.T) {
	prov := &loopingMockProvider{
		maxCalls: 4,
		toolName: "read_file",
		toolArgs: `{"path":"missing.go"}`,
	}
	sink := &eventCollectorSink{}
	tools := &staticMockTools{output: "file not found", ok: false}
	l := &Loop{
		Provider: prov,
		Tools:    tools,
		Gate:     tools,
		Human:    tools,
		Sink:     sink,
		Budget:   NewBudget(),
	}

	_ = l.Run(context.Background(), "test prompt")

	// Verify that a notice was emitted to the journal.
	var noticeFound bool
	for _, ev := range sink.events {
		if ev.Type == Notice && strings.Contains(ev.Text, "loop detected") && strings.Contains(ev.Text, "3 times") {
			noticeFound = true
			break
		}
	}
	if !noticeFound {
		t.Fatalf("expected loop notice at 3 repeats in journal events, got none")
	}

	// Verify that the provider received the notice in turn 4.
	prov.mu.Lock()
	defer prov.mu.Unlock()
	if len(prov.receivedMs) < 4 {
		t.Fatalf("expected at least 4 provider calls, got %d", len(prov.receivedMs))
	}
	lastMessages := prov.receivedMs[3]
	var wireNoticeFound bool
	for _, m := range lastMessages {
		if m.Role == provider.User && strings.Contains(m.Text, "«notice» loop detected") {
			wireNoticeFound = true
			break
		}
	}
	if !wireNoticeFound {
		t.Errorf("provider messages in turn 4 missing injected «notice» loop detected: %+v", lastMessages)
	}
}

// TestToolLoopHardCutAtFiveRepeats proves that when the provider returns identical calls 6 times,
// the loop terminates on the 5th attempt with ErrToolLoop and does not execute the 6th attempt.
func TestToolLoopHardCutAtFiveRepeats(t *testing.T) {
	prov := &loopingMockProvider{
		maxCalls: 6, // provider is prepared to yield 6 calls
		toolName: "read_file",
		toolArgs: `{"path":"missing.go"}`,
	}
	sink := &eventCollectorSink{}
	tools := &staticMockTools{output: "file not found", ok: false}
	l := &Loop{
		Provider: prov,
		Tools:    tools,
		Gate:     tools,
		Human:    tools,
		Sink:     sink,
		Budget:   NewBudget(),
	}

	err := l.Run(context.Background(), "test prompt")
	if !errors.Is(err, ErrToolLoop) {
		t.Fatalf("Run() error = %v, want ErrToolLoop", err)
	}

	// Acceptance criteria: provider must have been called exactly 5 times, never 6.
	prov.mu.Lock()
	calls := prov.calls
	prov.mu.Unlock()
	if calls != 5 {
		t.Fatalf("provider called %d times, want exactly 5 (hard cut on 5th attempt)", calls)
	}

	// Verify that RunError event was journaled with ErrorCode "loop_detected".
	var runErrorEvent *Event
	for i := range sink.events {
		if sink.events[i].Type == RunError {
			runErrorEvent = &sink.events[i]
		}
	}
	if runErrorEvent == nil {
		t.Fatalf("expected RunError event in journal, got none")
	}
	if runErrorEvent.ErrorCode != string(ErrCodeLoopDetected) {
		t.Errorf("RunError ErrorCode = %q, want %q", runErrorEvent.ErrorCode, ErrCodeLoopDetected)
	}
}

// TestToolLoopCanonicalJSONKeyOrdering verifies that JSON inputs with different key orders
// produce identical fingerprints and are correctly identified as repetitions.
func TestToolLoopCanonicalJSONKeyOrdering(t *testing.T) {
	fp1 := computeFingerprint("read_file", []byte(`{"path":"a.go","offset":10}`), true, "content")
	fp2 := computeFingerprint("read_file", []byte(`{"offset":10,"path":"a.go"}`), true, "content")
	if fp1 != fp2 {
		t.Fatalf("fingerprints differ for reordered JSON keys:\nfp1=%+v\nfp2=%+v", fp1, fp2)
	}
}

// TestToolLoopVaryingArgsDoesNotTrip verifies that calls with changing parameters
// do not increment the repetition count.
func TestToolLoopVaryingArgsDoesNotTrip(t *testing.T) {
	d := newLoopDetector()
	for i := 0; i < 10; i++ {
		args := fmt.Sprintf(`{"offset":%d}`, i*100)
		fp := computeFingerprint("read_file", []byte(args), true, "data")
		count := d.record(fp)
		if count != 1 {
			t.Fatalf("varying args produced count=%d, want 1", count)
		}
	}
}

// TestToolLoopResetAcrossRuns verifies that loop detector counts are scoped to a single Run()
// and reset between independent user prompts.
func TestToolLoopResetAcrossRuns(t *testing.T) {
	prov := &loopingMockProvider{
		maxCalls: 2,
		toolName: "read_file",
		toolArgs: `{"path":"missing.go"}`,
	}
	sink := &eventCollectorSink{}
	tools := &staticMockTools{output: "file not found", ok: false}
	l := &Loop{
		Provider: prov,
		Tools:    tools,
		Gate:     tools,
		Human:    tools,
		Sink:     sink,
		Budget:   NewBudget(),
	}

	// First run makes 2 calls.
	_ = l.Run(context.Background(), "prompt 1")
	// Second run makes 2 calls.
	prov.mu.Lock()
	prov.calls = 0
	prov.maxCalls = 2
	prov.mu.Unlock()
	_ = l.Run(context.Background(), "prompt 2")

	// Verify that the 3-repeat notice was NEVER emitted, because each run had only 2 calls.
	for _, ev := range sink.events {
		if ev.Type == Notice && strings.Contains(ev.Text, "loop detected") {
			t.Fatalf("unexpected loop notice emitted across distinct runs: %s", ev.Text)
		}
	}
}
