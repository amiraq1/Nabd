package ui

import (
	"testing"
	"time"

	"nabd/internal/agent"
	"nabd/internal/presentation"
)

// TestIntegrationFullPipeline exercises the complete path:
// scripted events → batcher → Feed.Update → Projector → Viewport.
// No external provider, no network, no API key.
func TestIntegrationFullPipeline(t *testing.T) {
	feed := NewFeed()
	feed.width = 80
	feed.height = 24

	var batches [][]agent.Event
	batcher := NewBatcher(10*time.Millisecond, 100, func(batch []agent.Event) {
		batches = append(batches, batch)
	})

	// Scripted events mirroring a real session.
	events := []agent.Event{
		{Seq: 1, Type: agent.RunStart, Text: "nabd test"},
		{Seq: 2, Type: agent.UserMsg, Text: "run inspection"},
		{Seq: 3, Type: agent.TextDelta, Text: "Starting "},
		{Seq: 4, Type: agent.TextDelta, Text: "inspection..."},
		{Seq: 5, Type: agent.ToolStart, Call: &agent.ToolCall{ID: "call_1", Name: "read_file", Args: []byte(`{"path":"main.go"}`)}},
		{Seq: 6, Type: agent.PermAsk, Call: &agent.ToolCall{ID: "call_1", Name: "read_file", Args: []byte(`{"path":"main.go"}`)}},
		{Seq: 7, Type: agent.PermReply, Call: &agent.ToolCall{ID: "call_1"}, Decision: agent.AllowOnce, EffectiveDecision: agent.AllowOnce},
		{Seq: 8, Type: agent.ToolEnd, Call: &agent.ToolCall{ID: "call_1", Name: "read_file", Output: "file content here", OK: true}},
		{Seq: 9, Type: agent.TurnEnd},
		{Seq: 10, Type: agent.RunEnd, Text: "session complete"},
	}

	// Feed events through the batcher.
	for _, e := range events {
		batcher.Add(e)
	}
	time.Sleep(50 * time.Millisecond)
	batcher.Stop()

	// Apply batches to feed.
	for _, batch := range batches {
		_, _ = feed.Update(agentEventBatchMsg{Events: batch})
	}

	// Verify feed state.
	items := feed.proj.Items()

	// 1. Assistant message merged from 2 deltas.
	var asst *presentation.FeedItem
	var tool, perm *presentation.FeedItem
	for i := range items {
		switch items[i].Type {
		case presentation.ItemAssistant:
			asst = &items[i]
		case presentation.ItemTool:
			tool = &items[i]
		case presentation.ItemPermission:
			perm = &items[i]
		}
	}
	if asst == nil {
		t.Fatal("no assistant item")
	}
	if asst.Text != "Starting inspection..." {
		t.Errorf("assistant text = %q, want 'Starting inspection...'", asst.Text)
	}
	if tool == nil {
		t.Fatal("no tool item")
	}
	if tool.Tool.Status != presentation.ToolDone {
		t.Errorf("tool status = %v, want ToolDone", tool.Tool.Status)
	}
	if perm == nil {
		t.Fatal("no permission item")
	}
	if perm.Perm.Status != presentation.PermAllow {
		t.Errorf("perm status = %v, want PermAllow", perm.Perm.Status)
	}

	// 2. Batcher preserved all events.
	totalBatched := 0
	for _, b := range batches {
		totalBatched += len(b)
	}
	if totalBatched != len(events) {
		t.Errorf("batcher dropped events: got %d, want %d", totalBatched, len(events))
	}

	// 3. View renders without panic.
	view := feed.View()
	if view == "" {
		t.Error("View() returned empty string")
	}
}

// TestIntegrationReplayThenIncremental verifies replay via Build followed by
// incremental Apply produces the same result as Build on the final state.
func TestIntegrationReplayThenIncremental(t *testing.T) {
	allEvents := []agent.Event{
		{Seq: 1, Type: agent.RunStart, Text: "start"},
		{Seq: 2, Type: agent.UserMsg, Text: "first"},
		{Seq: 3, Type: agent.TextDelta, Text: "reply one"},
		{Seq: 4, Type: agent.TurnEnd},
		{Seq: 5, Type: agent.UserMsg, Text: "second"},
		{Seq: 6, Type: agent.TextDelta, Text: "reply two"},
		{Seq: 7, Type: agent.TurnEnd},
		{Seq: 8, Type: agent.RunEnd, Text: "end"},
	}

	// Path 1: Build all at once.
	p1 := presentation.NewProjector()
	built, err := p1.Build(allEvents)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	// Path 2: Build first half, then Apply second half.
	p2 := presentation.NewProjector()
	half := len(allEvents) / 2
	if _, err := p2.Build(allEvents[:half]); err != nil {
		t.Fatalf("Build half: %v", err)
	}
	for _, e := range allEvents[half:] {
		if err := p2.Apply(e); err != nil {
			t.Fatalf("Apply: %v", err)
		}
	}
	incremental := p2.Items()

	if len(built) != len(incremental) {
		t.Fatalf("Build produced %d items, incremental produced %d", len(built), len(incremental))
	}
	for i := range built {
		if built[i].Type != incremental[i].Type || built[i].ID != incremental[i].ID ||
			built[i].Text != incremental[i].Text {
			t.Errorf("item[%d] mismatch:\n  built:      %+v\n  incremental: %+v", i, built[i], incremental[i])
		}
	}
}

// TestIntegrationBatcherReducesMessageCount verifies backpressure: many deltas
// produce fewer Bubble Tea messages than events.
func TestIntegrationBatcherReducesMessageCount(t *testing.T) {
	const numEvents = 1000
	var batches [][]agent.Event
	batcher := NewBatcher(20*time.Millisecond, 128, func(batch []agent.Event) {
		batches = append(batches, batch)
	})

	// Emit 1000 text deltas rapidly.
	for i := 0; i < numEvents; i++ {
		batcher.Add(agent.Event{Seq: i + 1, Type: agent.TextDelta, Text: "x"})
	}
	// Allow at least one timer flush.
	time.Sleep(30 * time.Millisecond)
	batcher.Stop()

	// Without batching, we'd send 1000 messages. With batching, far fewer.
	if len(batches) >= numEvents {
		t.Errorf("batcher produced %d batches for %d events — no backpressure", len(batches), numEvents)
	}
	// Verify no events lost.
	total := 0
	for _, b := range batches {
		total += len(b)
	}
	if total != numEvents {
		t.Errorf("lost events: got %d, want %d", total, numEvents)
	}
	t.Logf("backpressure: %d events → %d batches (%.1f%% reduction)",
		numEvents, len(batches), 100*(1-float64(len(batches))/float64(numEvents)))
}
