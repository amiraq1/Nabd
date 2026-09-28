package presentation_test

import (
	"testing"

	"nabd/internal/agent"
	"nabd/internal/presentation"
)

func TestToolPendingTransitionsLiveEvents(t *testing.T) {
	call := &agent.ToolCall{ID: "call-1", Name: "bash", Args: []byte(`{"cmd":"ls"}`)}

	p := presentation.NewProjector()

	// 1. ToolStart emitted: card starts as ToolRunning
	if err := p.Apply(agent.Event{Seq: 1, Type: agent.ToolStart, Call: call}); err != nil {
		t.Fatal(err)
	}
	items := p.Items()
	if len(items) != 1 || items[0].Tool == nil {
		t.Fatalf("expected 1 tool item, got %+v", items)
	}
	if items[0].Tool.Status != presentation.ToolRunning {
		t.Fatalf("expected ToolRunning after ToolStart, got %v", items[0].Tool.Status)
	}

	// 2. PermAsk emitted: card moves to ToolPending ("awaiting approval")
	if err := p.Apply(agent.Event{Seq: 2, Type: agent.PermAsk, Call: call}); err != nil {
		t.Fatal(err)
	}
	items = p.Items()
	toolItem := findItem(items, presentation.ItemTool, "tool_call-1")
	if toolItem == nil || toolItem.Tool == nil {
		t.Fatalf("tool card not found after PermAsk")
	}
	if toolItem.Tool.Status != presentation.ToolPending {
		t.Fatalf("expected ToolPending after PermAsk, got %v", toolItem.Tool.Status)
	}

	// 3. Allowing PermReply: card moves back to ToolRunning
	if err := p.Apply(agent.Event{Seq: 3, Type: agent.PermReply, Call: call, Decision: agent.AllowOnce}); err != nil {
		t.Fatal(err)
	}
	items = p.Items()
	toolItem = findItem(items, presentation.ItemTool, "tool_call-1")
	if toolItem == nil || toolItem.Tool == nil {
		t.Fatalf("tool card not found after PermReply")
	}
	if toolItem.Tool.Status != presentation.ToolRunning {
		t.Fatalf("expected ToolRunning after allowing PermReply, got %v", toolItem.Tool.Status)
	}

	// 4. ToolEnd: card moves to ToolDone
	endCall := *call
	endCall.OK = true
	endCall.Output = "file.txt"
	if err := p.Apply(agent.Event{Seq: 4, Type: agent.ToolEnd, Call: &endCall}); err != nil {
		t.Fatal(err)
	}
	items = p.Items()
	toolItem = findItem(items, presentation.ItemTool, "tool_call-1")
	if toolItem == nil || toolItem.Tool == nil {
		t.Fatalf("tool card not found after ToolEnd")
	}
	if toolItem.Tool.Status != presentation.ToolDone {
		t.Fatalf("expected ToolDone after ToolEnd, got %v", toolItem.Tool.Status)
	}
}

func TestToolPendingTransitionsDeny(t *testing.T) {
	call := &agent.ToolCall{ID: "call-2", Name: "write_file", Args: []byte(`{"path":"x.txt"}`)}

	p := presentation.NewProjector()

	// ToolStart -> ToolRunning
	_ = p.Apply(agent.Event{Seq: 1, Type: agent.ToolStart, Call: call})
	// PermAsk -> ToolPending
	_ = p.Apply(agent.Event{Seq: 2, Type: agent.PermAsk, Call: call})
	toolItem := findItem(p.Items(), presentation.ItemTool, "tool_call-2")
	if toolItem.Tool.Status != presentation.ToolPending {
		t.Fatalf("expected ToolPending, got %v", toolItem.Tool.Status)
	}

	// PermReply with Deny -> ToolDenied
	_ = p.Apply(agent.Event{Seq: 3, Type: agent.PermReply, Call: call, Decision: agent.Deny, RawDecision: agent.Deny})
	toolItem = findItem(p.Items(), presentation.ItemTool, "tool_call-2")
	if toolItem.Tool.Status != presentation.ToolDenied {
		t.Fatalf("expected ToolDenied after Deny reply, got %v", toolItem.Tool.Status)
	}

	// ToolEnd with !OK -> stays ToolDenied
	endCall := *call
	endCall.OK = false
	endCall.Output = "permission denied"
	_ = p.Apply(agent.Event{Seq: 4, Type: agent.ToolEnd, Call: &endCall})
	toolItem = findItem(p.Items(), presentation.ItemTool, "tool_call-2")
	if toolItem.Tool.Status != presentation.ToolDenied {
		t.Fatalf("expected ToolDenied after ToolEnd, got %v", toolItem.Tool.Status)
	}
}

func TestReadOnlyToolNeverAsksStaysRunning(t *testing.T) {
	call := &agent.ToolCall{ID: "call-read", Name: "read_file", Args: []byte(`{"path":"main.go"}`)}

	p := presentation.NewProjector()

	// ToolStart -> ToolRunning
	_ = p.Apply(agent.Event{Seq: 1, Type: agent.ToolStart, Call: call})
	toolItem := findItem(p.Items(), presentation.ItemTool, "tool_call-read")
	if toolItem.Tool.Status != presentation.ToolRunning {
		t.Fatalf("expected ToolRunning, got %v", toolItem.Tool.Status)
	}

	// Read-only tools never emit PermAsk; they stay ToolRunning until ToolEnd
	endCall := *call
	endCall.OK = true
	endCall.Output = "package main"
	_ = p.Apply(agent.Event{Seq: 2, Type: agent.ToolEnd, Call: &endCall})
	toolItem = findItem(p.Items(), presentation.ItemTool, "tool_call-read")
	if toolItem.Tool.Status != presentation.ToolDone {
		t.Fatalf("expected ToolDone, got %v", toolItem.Tool.Status)
	}
}

func TestReplayedJournalToolPending(t *testing.T) {
	call := &agent.ToolCall{ID: "call-replay", Name: "bash", Args: []byte(`{"cmd":"pwd"}`)}

	// Journal replayed up to PermAsk (session pending approval when saved)
	eventsPending := []agent.Event{
		{Seq: 1, Type: agent.RunStart},
		{Seq: 2, Type: agent.UserMsg, Text: "run pwd"},
		{Seq: 3, Type: agent.ToolStart, Call: call},
		{Seq: 4, Type: agent.PermAsk, Call: call},
	}

	p := presentation.NewProjector()
	items, err := p.Build(eventsPending)
	if err != nil {
		t.Fatal(err)
	}
	toolItem := findItem(items, presentation.ItemTool, "tool_call-replay")
	if toolItem == nil || toolItem.Tool == nil {
		t.Fatalf("tool card not found in replayed journal")
	}
	if toolItem.Tool.Status != presentation.ToolPending {
		t.Fatalf("replayed journal at PermAsk must have ToolPending, got %v", toolItem.Tool.Status)
	}

	// Full journal with approval and execution
	eventsDone := append(eventsPending,
		agent.Event{Seq: 5, Type: agent.PermReply, Call: call, Decision: agent.AllowOnce},
		agent.Event{Seq: 6, Type: agent.ToolEnd, Call: &agent.ToolCall{ID: "call-replay", Name: "bash", OK: true, Output: "/home/user"}},
		agent.Event{Seq: 7, Type: agent.TurnEnd},
	)
	p2 := presentation.NewProjector()
	items2, err := p2.Build(eventsDone)
	if err != nil {
		t.Fatal(err)
	}
	toolItem2 := findItem(items2, presentation.ItemTool, "tool_call-replay")
	if toolItem2.Tool.Status != presentation.ToolDone {
		t.Fatalf("replayed full journal must have ToolDone, got %v", toolItem2.Tool.Status)
	}
}

func findItem(items []presentation.FeedItem, kind presentation.ItemType, id string) *presentation.FeedItem {
	for i := range items {
		if items[i].Type == kind && items[i].ID == id {
			return &items[i]
		}
	}
	return nil
}
