package agent_test

import (
	"context"
	"nabd/internal/event"
	"testing"

	"nabd/internal/agent"
	"nabd/internal/perm"
	"nabd/internal/provider"
	"nabd/internal/snap"
	"nabd/internal/tools"
)

// fakeProviderBatch emits the specified calls.
type fakeProviderBatch struct {
	calls []provider.ToolCall
}

func (f fakeProviderBatch) Name() string { return "fake" }
func (f fakeProviderBatch) Stream(ctx context.Context, req provider.Request) (<-chan provider.Chunk, error) {
	ch := make(chan provider.Chunk, len(f.calls)+1)
	for _, c := range f.calls {
		// Make a copy so pointers don't share
		cCopy := c
		ch <- provider.Chunk{Kind: provider.ChunkToolCall, Call: &cCopy}
	}
	ch <- provider.Chunk{Kind: provider.ChunkStop, Stop: "tool_calls"}
	close(ch)
	return ch, nil
}

func setupLoop(calls []provider.ToolCall, answer event.Decision) (*agent.Loop, func() []event.Event) {
	root, _ := tools.NewRoot(".")
	sh, _ := snap.New(root.Dir())
	reg := tools.NewRegistry(root, sh)
	policy := perm.New(reg)
	policy.SetYOLO(false) // Strict

	loop := &agent.Loop{
		Provider: fakeProviderBatch{calls},
		Gate:     gate{policy},
		Human:    &fakeHuman{answer},
		Budget:   agent.NewBudget(),
		System:   "sys",
		Tools:    reg,
	}

	var events []event.Event
	loop.Sink = testSink(func(e event.Event) error {
		events = append(events, e)
		return nil
	})

	return loop, func() []event.Event { return events }
}

func checkPairing(t *testing.T, events []event.Event, name string) {
	live := event.Live(events)
	msgs := agent.Messages(live)

	// We check if every tool result is paired with a tool call in the same turn or earlier.
	for i, m := range msgs {
		if m.Role == provider.User && len(m.ToolResults) > 0 {
			// Find preceding assistant message with tool calls
			var calls []provider.ToolCall
			for j := i - 1; j >= 0; j-- {
				if msgs[j].Role == provider.Assistant && len(msgs[j].ToolCalls) > 0 {
					calls = msgs[j].ToolCalls
					break
				}
			}
			for _, res := range m.ToolResults {
				found := false
				for _, c := range calls {
					if c.ID == res.ID {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("[%s] ToolResult %q has no matching ToolCall in the preceding assistant message", name, res.ID)
				}
			}
		}
	}
}

func TestMessagesPairingIntegration(t *testing.T) {
	t.Run("denied tool", func(t *testing.T) {
		l, get := setupLoop([]provider.ToolCall{{ID: "1", Name: "bash", Input: []byte(`{}`)}}, event.Deny)
		_ = l.Run(context.Background(), "do it")
		checkPairing(t, get(), "denied tool")
	})

	t.Run("unknown tool", func(t *testing.T) {
		l, get := setupLoop([]provider.ToolCall{{ID: "1", Name: "magic", Input: []byte(`{}`)}}, event.AllowOnce)
		_ = l.Run(context.Background(), "do it")
		checkPairing(t, get(), "unknown tool")
	})

	t.Run("allowed tool", func(t *testing.T) {
		l, get := setupLoop([]provider.ToolCall{{ID: "1", Name: "bash", Input: []byte(`{"command":"echo"}`)}}, event.AllowOnce)
		_ = l.Run(context.Background(), "do it")
		checkPairing(t, get(), "allowed tool")
	})

	t.Run("partial denial in batch", func(t *testing.T) {
		l, get := setupLoop([]provider.ToolCall{
			{ID: "1", Name: "read_file", Input: []byte(`{"path":"main.go"}`)}, // implicitly allowed
			{ID: "2", Name: "bash", Input: []byte(`{"command":"rm -rf /"}`)},  // denied
		}, event.Deny)
		_ = l.Run(context.Background(), "do it")
		checkPairing(t, get(), "partial denial")
	})

	t.Run("canceled context", func(t *testing.T) {
		l, get := setupLoop([]provider.ToolCall{{ID: "1", Name: "bash", Input: []byte(`{}`)}}, event.AllowOnce)
		ctx, cancel := context.WithCancel(context.Background())
		cancel() // immediately cancel
		_ = l.Run(ctx, "do it")
		checkPairing(t, get(), "canceled context")
	})

	t.Run("interruption during batch", func(t *testing.T) {
		l, get := setupLoop([]provider.ToolCall{
			{ID: "1", Name: "read_file", Input: []byte(`{"path":"main.go"}`)},
			{ID: "2", Name: "bash", Input: []byte(`{}`)},
		}, event.AllowOnce)
		ctx, cancel := context.WithCancel(context.Background())
		// Cancel inside the first tool result emission to simulate ctrl+c during batch
		l.Sink = testSink(func(e event.Event) error {
			if e.Type == event.ToolEnd && e.Call.ID == "1" {
				cancel()
			}
			get() // just for side effects, we will append locally
			return nil
		})
		// Re-implement appending to events array for this custom sink
		var evs []event.Event
		l.Sink = testSink(func(e event.Event) error {
			evs = append(evs, e)
			if e.Type == event.ToolEnd && e.Call != nil && e.Call.ID == "1" {
				cancel()
			}
			return nil
		})
		_ = l.Run(ctx, "do it")
		checkPairing(t, evs, "interruption")
	})

	t.Run("legacy session orphan ToolEnd", func(t *testing.T) {
		events := []event.Event{
			{Seq: 1, Type: event.UserMsg, Text: "do it"},
			{Seq: 2, Type: event.ToolEnd, Call: &event.ToolCall{ID: "orphan1", Name: "bash", Output: "done", OK: true}},
			{Seq: 3, Type: event.ToolEnd, Call: &event.ToolCall{ID: "orphan2", Name: "", Output: "fail", OK: false}}, // missing name
		}
		checkPairing(t, events, "legacy orphan")

		// specifically assert that orphan2 gets name="unknown" and pairing succeeds
		msgs := agent.Messages(event.Live(events))
		if len(msgs) < 2 {
			t.Fatalf("expected msgs to have generated calls")
		}
		foundOrphan2Call := false
		for _, c := range msgs[0].ToolCalls {
			if c.ID == "orphan2" && c.Name == "unknown" {
				foundOrphan2Call = true
			}
		}
		if !foundOrphan2Call {
			t.Errorf("expected orphan2 to synthesize an 'unknown' tool_call, got %+v", msgs[0].ToolCalls)
		}
	})
}
