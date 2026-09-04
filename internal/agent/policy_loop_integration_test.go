package agent_test

import (
	"context"
	"testing"

	"nabd/internal/agent"
	"nabd/internal/perm"
	"nabd/internal/provider"
	"nabd/internal/snap"
	"nabd/internal/tools"
)

type gate struct{ p *perm.Policy }

func (g gate) Check(tool string) (agent.Verdict, string) {
	v, why := g.p.Check(tool)
	switch v {
	case perm.Allow:
		return agent.VerdictAllow, why
	case perm.Deny:
		return agent.VerdictDeny, why
	}
	return agent.VerdictAsk, why
}
func (g gate) Record(tool string, d agent.Decision) { g.p.Record(tool, d) }
func (g gate) Effective(tool string, d agent.Decision) agent.Decision {
	return g.p.Effective(tool, d)
}

type fakeHuman struct {
	answer agent.Decision
}

func (f *fakeHuman) Ask(ctx context.Context, call agent.ToolCall) agent.Decision {
	return f.answer
}

type fakeProvider struct{}

func (f fakeProvider) Name() string { return "fake" }
func (f fakeProvider) Stream(ctx context.Context, req provider.Request) (<-chan provider.Chunk, error) {
	ch := make(chan provider.Chunk, 1)
	ch <- provider.Chunk{Kind: provider.ChunkToolCall, Call: &provider.ToolCall{ID: "1", Name: "bash", Input: []byte(`{"command":"echo"}`)}}
	close(ch)
	return ch, nil
}

type testSink func(agent.Event) error

func (s testSink) Emit(e agent.Event) error { return s(e) }

func TestLoopWithRealPolicy(t *testing.T) {
	root, _ := tools.NewRoot(t.TempDir())
	sh, _ := snap.New(root.Dir())
	reg := tools.NewRegistry(root, sh)
	policy := perm.New(reg)

	human := &fakeHuman{answer: agent.AllowSession}

	loop := &agent.Loop{
		Provider: fakeProvider{},
		Gate:     gate{policy},
		Human:    human,
		Budget:   agent.NewBudget(),
		System:   "sys",
		Tools:    reg,
	}

	events := []agent.Event{}
	sink := func(e agent.Event) error {
		events = append(events, e)
		return nil
	}
	loop.Sink = testSink(sink)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	loop.Sink = testSink(func(e agent.Event) error {
		events = append(events, e)
		if e.Type == agent.PermReply {
			cancel() // abort the loop so it doesn't infinite loop
		}
		return nil
	})

	_ = loop.Run(ctx, "hello")

	var firstReply *agent.Event
	for i, e := range events {
		if e.Type == agent.PermReply {
			firstReply = &events[i]
			break
		}
	}
	if firstReply == nil {
		t.Fatalf("no PermReply emitted")
	}
	if firstReply.Decision != agent.AllowOnce {
		t.Errorf("expected effective Decision=AllowOnce, got %v", firstReply.Decision)
	}
	if firstReply.RawDecision != agent.AllowSession {
		t.Errorf("expected RawDecision=AllowSession, got %v", firstReply.RawDecision)
	}
}
