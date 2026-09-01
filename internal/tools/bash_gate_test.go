package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"nabd/internal/agent"
	"nabd/internal/provider"
	"nabd/internal/snap"
)

// bashGate is an agent.Gate that answers from a fixed verdict map.
type bashGate struct {
	verdict map[string]agent.Verdict
}

func (g bashGate) Check(tool string) (agent.Verdict, string) {
	if v, ok := g.verdict[tool]; ok {
		return v, ""
	}
	return agent.VerdictDeny, "غير معروف"
}
func (g bashGate) Record(tool string, d agent.Decision) {}

// bashProvider asks for one bash tool call.
type bashProvider struct {
	cmd  string
	calls int
}

func (bashProvider) Name() string { return "mock" }

func (p *bashProvider) Stream(ctx context.Context, req provider.Request) (<-chan provider.Chunk, error) {
	ch := make(chan provider.Chunk, 2)
	p.calls++
	if p.calls == 1 {
		raw, _ := json.Marshal(map[string]any{"cmd": p.cmd})
		ch <- provider.Chunk{Kind: provider.ChunkToolCall, Call: &provider.ToolCall{
			ID: "call_bash", Name: "bash", Input: raw,
		}}
		ch <- provider.Chunk{Kind: provider.ChunkStop, Stop: "tool_calls"}
	} else {
		ch <- provider.Chunk{Kind: provider.ChunkText, Text: "تم."}
		ch <- provider.Chunk{Kind: provider.ChunkStop, Stop: "end_turn"}
	}
	close(ch)
	return ch, nil
}

// runBash drives one loop run with a real Registry and returns the events
// plus the temp dir.
func runBash(t *testing.T, verdict agent.Verdict, cmd string) ([]agent.Event, string) {
	t.Helper()
	dir := t.TempDir()
	root, err := NewRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	sh, err := snap.New(root.Dir())
	if err != nil {
		t.Fatal(err)
	}
	reg := NewRegistry(root, sh)

	l := &agent.Loop{
		Provider: &bashProvider{cmd: cmd},
		Tools:    bashLoopTools{reg},
		Budget:   agent.NewBudget(),
		Gate:     bashGate{verdict: map[string]agent.Verdict{"bash": verdict}},
		Human:    bashAsker{verdict},
	}
	var events []agent.Event
	l.Sink = toolsSink(func(e agent.Event) error {
		events = append(events, e)
		return nil
	})
	if err := l.Run(context.Background(), "شغّل أمرًا"); err != nil {
		t.Fatal(err)
	}
	return events, dir
}

// TestBashDeniedRunsNoSubprocess: a denied bash call must produce no
// tool_start and no filesystem side effect.
func TestBashDeniedRunsNoSubprocess(t *testing.T) {
	events, dir := runBash(t, agent.VerdictDeny, "touch denied.txt")

	for _, e := range events {
		if e.Type == agent.ToolStart {
			t.Fatalf("denied bash call produced ToolStart: %+v", e)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "denied.txt")); !os.IsNotExist(err) {
		t.Fatalf("denied bash call created a side effect file")
	}
}

// TestBashApprovedRunsSubprocess: a bash call approved at the prompt must
// produce PermReply → ToolStart → ToolEnd in order and the filesystem side
// effect. VerdictAsk is the realistic path: the gate asks, the human says y.
func TestBashApprovedRunsSubprocess(t *testing.T) {
	events, dir := runBash(t, agent.VerdictAsk, "touch approved.txt")

	var permIdx, startIdx, endIdx = -1, -1, -1
	for i, e := range events {
		switch e.Type {
		case agent.PermReply:
			permIdx = i
		case agent.ToolStart:
			startIdx = i
		case agent.ToolEnd:
			endIdx = i
		}
	}
	if permIdx < 0 || startIdx < 0 || endIdx < 0 {
		t.Fatalf("missing events: perm=%d start=%d end=%d", permIdx, startIdx, endIdx)
	}
	if !(permIdx < startIdx && startIdx < endIdx) {
		t.Errorf("event ordering wrong: perm=%d start=%d end=%d", permIdx, startIdx, endIdx)
	}
	if _, err := os.Stat(filepath.Join(dir, "approved.txt")); err != nil {
		t.Fatalf("approved bash call did not create the file: %v", err)
	}
}

// bashLoopTools satisfies agent.Tools with a real Registry.
type bashLoopTools struct{ reg *Registry }

func (b bashLoopTools) Specs() []provider.ToolSpec { return b.reg.Specs() }
func (b bashLoopTools) Run(ctx context.Context, c provider.ToolCall) (string, bool, error) {
	return b.reg.Run(ctx, c)
}
func (b bashLoopTools) RunDetailed(ctx context.Context, name string, raw json.RawMessage) (agent.Outcome, error) {
	return b.reg.RunDetailed(ctx, name, raw)
}

// bashAsker answers the permission prompt from the same verdict.
type bashAsker struct{ verdict agent.Verdict }

func (a bashAsker) Ask(ctx context.Context, c agent.ToolCall) agent.Decision {
	switch a.verdict {
	case agent.VerdictAllow, agent.VerdictAsk:
		return agent.AllowOnce
	default:
		return agent.Deny
	}
}

// toolsSink adapts a func to the agent.Sink interface.
type toolsSink func(agent.Event) error

func (f toolsSink) Emit(e agent.Event) error { return f(e) }
