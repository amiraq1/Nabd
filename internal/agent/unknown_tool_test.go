package agent_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"unicode"

	"nabd/internal/agent"
	"nabd/internal/provider"
	"nabd/internal/snap"
	"nabd/internal/tools"
)

// unknownOnceProvider asks for one call to a tool that does not exist,
// then answers with plain text.
type unknownOnceProvider struct{ calls int }

func (unknownOnceProvider) Name() string { return "mock" }

func (p *unknownOnceProvider) Stream(ctx context.Context, req provider.Request) (<-chan provider.Chunk, error) {
	ch := make(chan provider.Chunk, 2)
	p.calls++
	if p.calls == 1 {
		ch <- provider.Chunk{Kind: provider.ChunkToolCall, Call: &provider.ToolCall{
			ID: "call_u1", Name: "shell", Input: json.RawMessage(`{"cmd":"ls"}`),
		}}
		ch <- provider.Chunk{Kind: provider.ChunkStop, Stop: "tool_calls"}
	} else {
		ch <- provider.Chunk{Kind: provider.ChunkText, Text: "تم."}
		ch <- provider.Chunk{Kind: provider.ChunkStop, Stop: "end_turn"}
	}
	close(ch)
	return ch, nil
}

// TestUnknownToolNamesAlternatives: an unknown tool is an existence error,
// not a permission denial — filing it under deny corrupts the evidence.
// The result must say so in English (model-directed), name the tools that
// DO exist so the model can correct its next call, and never reach the
// permission gate.
func TestUnknownToolNamesAlternatives(t *testing.T) {
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
		Provider: &unknownOnceProvider{},
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
	if err := l.Run(context.Background(), "شغّل أمراً"); err != nil {
		t.Fatal(err)
	}

	msg := ""
	for _, e := range events {
		if e.Type == agent.ToolEnd && e.Call != nil && e.Call.Name == "shell" {
			msg = e.Call.Output
		}
		if e.Type == agent.PermAsk || e.Type == agent.PermReply {
			t.Fatalf("unknown tool reached the permission gate: %s", e.Type)
		}
	}
	if msg == "" {
		t.Fatal("no tool_end for the unknown call")
	}
	if !strings.Contains(msg, `unknown tool "shell"`) {
		t.Errorf("message must name the unknown tool verbatim, got %q", msg)
	}
	if !strings.Contains(msg, "available:") || !strings.Contains(msg, "read_file") {
		t.Errorf("message must name the available tools, got %q", msg)
	}
	// The contract is English words: typographic separators (·) are fine,
	// foreign-script letters are not. Check letters only.
	for _, r := range msg {
		if r > 127 && unicode.IsLetter(r) {
			t.Errorf("model-directed message contains a non-ASCII letter: %q", msg)
			break
		}
	}
}
