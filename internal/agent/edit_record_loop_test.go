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

// loopTools wraps a real Registry so the loop drives actual disk writes.
type loopTools struct{ reg *tools.Registry }

func (l loopTools) Specs() []provider.ToolSpec { return l.reg.Specs() }
func (l loopTools) Run(ctx context.Context, c provider.ToolCall) (string, bool, error) {
	return l.reg.Run(ctx, c)
}
func (l loopTools) Check(tool string) (agent.Verdict, string) {
	return agent.VerdictAllow, ""
}
func (l loopTools) Record(tool string, d agent.Decision)              {}
func (l loopTools) Ask(ctx context.Context, c agent.ToolCall) agent.Decision {
	return agent.AllowOnce
}
func (l loopTools) RunDetailed(ctx context.Context, name string, raw json.RawMessage) (agent.Outcome, error) {
	return l.reg.RunDetailed(ctx, name, raw)
}

// LastEdit lets the loop's EventEdit emission find the persisted record.
func (l loopTools) LastEdit() *agent.EditRecord { return l.reg.LastEdit() }

// writeOnceProvider asks for one write_file call, then on the next turn
// (which carries the tool_result) answers with plain text and stops.
type writeOnceProvider struct{ calls int }

func (writeOnceProvider) Name() string { return "mock" }

func (p *writeOnceProvider) Stream(ctx context.Context, req provider.Request) (<-chan provider.Chunk, error) {
	ch := make(chan provider.Chunk, 2)
	p.calls++
	if p.calls == 1 {
		raw, _ := json.Marshal(map[string]any{"path": "out.md", "content": "نتيجة التعديل\n"})
		ch <- provider.Chunk{Kind: provider.ChunkToolCall, Call: &provider.ToolCall{
			ID: "call_1", Name: "write_file", Input: raw,
		}}
		ch <- provider.Chunk{Kind: provider.ChunkStop, Stop: "tool_calls"}
	} else {
		ch <- provider.Chunk{Kind: provider.ChunkText, Text: "تم."}
		ch <- provider.Chunk{Kind: provider.ChunkStop, Stop: "end_turn"}
	}
	close(ch)
	return ch, nil
}

// TestLoopEmitsEditRecordEvent drives a full Run with a real Registry and
// verifies the edit_record event is emitted via l.emit — carrying Seq/Parent
// ordering — after ToolEnd and before the turn closes.
func TestLoopEmitsEditRecordEvent(t *testing.T) {
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

	l := &agent.Loop{
		Provider: &writeOnceProvider{},
		Tools:    loopTools{reg},
		Budget:   agent.NewBudget(),
		Gate:     loopTools{reg},
		Human:    loopTools{reg},
	}
	var events []agent.Event
	l.Sink = sinkFunc(func(e agent.Event) error {
		events = append(events, e)
		return nil
	})

	if err := l.Run(context.Background(), "اكتب out.md"); err != nil {
		t.Fatal(err)
	}

	var editIdx, toolEndIdx, turnEndIdx = -1, -1, -1
	for i, e := range events {
		switch e.Type {
		case agent.EventEdit:
			editIdx = i
		case agent.ToolEnd:
			toolEndIdx = i
		case agent.TurnEnd:
			turnEndIdx = i
		}
	}
	if editIdx < 0 {
		t.Fatal("no edit_record event emitted")
	}
	if toolEndIdx < 0 || editIdx < toolEndIdx {
		t.Errorf("edit_record (idx %d) must come after ToolEnd (idx %d)", editIdx, toolEndIdx)
	}
	if turnEndIdx >= 0 && editIdx > turnEndIdx {
		t.Errorf("edit_record (idx %d) must come before TurnEnd (idx %d)", editIdx, turnEndIdx)
	}

	// The event carries the persisted record.
	ev := events[editIdx]
	if ev.Edit == nil {
		t.Fatal("edit_record event has no Edit payload")
	}
	if ev.Edit.Path != "out.md" || ev.Edit.HashAfter == "" || ev.Edit.Patch == "" {
		t.Errorf("Edit payload incomplete: %+v", ev.Edit)
	}
	if ev.Seq == 0 {
		t.Error("edit_record event has no Seq (must go through l.emit)")
	}

	// Filesystem proof: the file exists with the written content.
	b, err := os.ReadFile(filepath.Join(dir, "out.md"))
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != "نتيجة التعديل\n" {
		t.Errorf("filesystem content: %q", string(b))
	}

	// Messages() must not carry the patch.
	live := agent.Live(events)
	for _, m := range agent.Messages(live) {
		if strings.Contains(m.Text, "--- a/out.md") || strings.Contains(m.Text, "نتيجة التعديل") && strings.Contains(m.Text, "+") {
			t.Errorf("patch leaked into messages: %q", m.Text)
		}
	}
}

// sinkFunc adapts a func to the agent.Sink interface.
type sinkFunc func(agent.Event) error

func (f sinkFunc) Emit(e agent.Event) error { return f(e) }

