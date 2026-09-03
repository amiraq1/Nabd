package agent_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"nabd/internal/agent"
	"nabd/internal/provider"
	"nabd/internal/snap"
	"nabd/internal/tools"
)

// readProvider asks for one read_file call, then on the next turn (which
// carries the tool_result) answers with plain text and stops.
type readProvider struct {
	path  string
	calls int
}

func (readProvider) Name() string { return "mock" }

func (p *readProvider) Stream(ctx context.Context, req provider.Request) (<-chan provider.Chunk, error) {
	ch := make(chan provider.Chunk, 2)
	p.calls++
	if p.calls == 1 {
		raw, _ := json.Marshal(map[string]any{"path": p.path})
		ch <- provider.Chunk{Kind: provider.ChunkToolCall, Call: &provider.ToolCall{
			ID: "call_read", Name: "read_file", Input: raw,
		}}
		ch <- provider.Chunk{Kind: provider.ChunkStop, Stop: "tool_calls"}
	} else {
		ch <- provider.Chunk{Kind: provider.ChunkText, Text: "تم."}
		ch <- provider.Chunk{Kind: provider.ChunkStop, Stop: "end_turn"}
	}
	close(ch)
	return ch, nil
}

// TestLoopEmitsReadRecordWhenTruncated: a read_file call on a file larger
// than the byte cap must emit a read_record event with truncated=true, and
// the tool_result must carry the explicit truncation tail.
func TestLoopEmitsReadRecordWhenTruncated(t *testing.T) {
	dir := t.TempDir()
	root, err := tools.NewRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	sh, err := snap.New(root.Dir())
	if err != nil {
		t.Fatal(err)
	}
	reg := tools.NewRegistry(root, sh)

	// A file far over the 16 KiB cap.
	big := filepath.Join(dir, "big.go")
	var b strings.Builder
	for i := 0; i < 300; i++ {
		b.WriteString(strings.Repeat("z", 120) + "\n")
	}
	os.WriteFile(big, []byte(b.String()), 0o644)

	l := &agent.Loop{
		Provider: &readProvider{path: "big.go"},
		Tools:    loopTools2{reg},
		Budget:   agent.NewBudget(),
		Gate:     allowGate{},
		Human:    allowGate{},
	}
	var events []agent.Event
	l.Sink = sinkFunc2(func(e agent.Event) error {
		events = append(events, e)
		return nil
	})
	if err := l.Run(context.Background(), "اقرأ big.go"); err != nil {
		t.Fatal(err)
	}

	// Event truth: a read_record event with truncated=true must exist.
	var found *agent.Event
	for i := range events {
		if events[i].Type == agent.EventRead {
			found = &events[i]
		}
	}
	if found == nil {
		t.Fatal("no read_record event emitted for a truncated read")
	}
	if found.Read == nil || !found.Read.Truncated {
		t.Fatalf("read_record must carry truncated=true: %+v", found.Read)
	}
	if found.Read.Path != "big.go" {
		t.Errorf("read_record path=%q, want big.go", found.Read.Path)
	}
	if found.Seq == 0 {
		t.Error("read_record has no Seq (must go through l.emit)")
	}

	// Tool result truth: the model saw the truncation tail.
	var toolResult string
	for _, e := range events {
		if e.Type == agent.ToolEnd && e.Call != nil && e.Call.Name == "read_file" {
			toolResult = e.Call.Output
		}
	}
	if !strings.Contains(toolResult, "[TRUNCATED:") {
		t.Errorf("tool_result must carry the truncation tail, got %q", toolResult)
	}
	if !strings.Contains(toolResult, "continue with offset=") {
		t.Errorf("tail must say how to continue, got %q", toolResult)
	}
}

// allowGate approves everything without asking.
type allowGate struct{}

func (allowGate) Check(tool string) (agent.Verdict, string) { return agent.VerdictAllow, "" }
func (allowGate) Record(tool string, d agent.Decision)      {}
func (allowGate) Effective(tool string, d agent.Decision) agent.Decision { return d }
func (allowGate) Ask(ctx context.Context, c agent.ToolCall) agent.Decision {
	return agent.AllowOnce
}

// loopTools2 satisfies agent.Tools with a real Registry.
type loopTools2 struct{ reg *tools.Registry }

func (l loopTools2) Specs() []provider.ToolSpec { return l.reg.Specs() }
func (l loopTools2) Run(ctx context.Context, c provider.ToolCall) (string, bool, error) {
	return l.reg.Run(ctx, c)
}
func (l loopTools2) RunDetailed(ctx context.Context, name string, raw json.RawMessage) (agent.Outcome, error) {
	return l.reg.RunDetailed(ctx, name, raw)
}

// sinkFunc2 adapts a func to the agent.Sink interface.
type sinkFunc2 func(agent.Event) error

func (f sinkFunc2) Emit(e agent.Event) error { return f(e) }
