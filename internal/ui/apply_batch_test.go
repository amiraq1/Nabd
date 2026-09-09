package ui

import (
	"strings"
	"testing"

	"nabd/internal/agent"
)

// TestApplyBatchNoChange verifies that a batch with only Compact/Rewind
// (events that don't render to the feed) leaves the display untouched.
func TestApplyBatchNoChange(t *testing.T) {
	f := NewFeed()
	f.width = 80
	f.height = 24
	f.SetTouch(false)

	f.applyBatch([]agent.Event{
		{Seq: 1, Type: agent.UserMsg, Text: "hello"},
		{Seq: 2, Type: agent.TurnEnd},
	})

	before := strings.Join(f.lines, "\n")
	scrollTopBefore := f.scrollTop
	followBefore := f.follow
	unseenBefore := f.unseen

	// Compact and Rewind are intentionally not shown in the feed.
	f.applyBatch([]agent.Event{
		{Seq: 3, Type: agent.Compact},
	})

	after := strings.Join(f.lines, "\n")
	if before != after {
		t.Fatalf("lines changed:\nbefore:\n%s\nafter:\n%s", before, after)
	}
	if f.scrollTop != scrollTopBefore {
		t.Fatalf("scrollTop changed: %d -> %d", scrollTopBefore, f.scrollTop)
	}
	if f.follow != followBefore {
		t.Fatalf("follow changed: %v -> %v", followBefore, f.follow)
	}
	if f.unseen != unseenBefore {
		t.Fatalf("unseen changed: %d -> %d", unseenBefore, f.unseen)
	}
}

// TestApplyBatchStreamingGrowth verifies that TextDelta extends the last
// assistant item and the feed re-renders the growing text.
func TestApplyBatchStreamingGrowth(t *testing.T) {
	f := NewFeed()
	f.width = 80
	f.height = 24

	f.applyBatch([]agent.Event{
		{Seq: 1, Type: agent.UserMsg, Text: "say hello"},
		{Seq: 2, Type: agent.TextDelta, Text: "hel"},
	})

	before := strings.Join(f.lines, "\n")

	f.applyBatch([]agent.Event{
		{Seq: 3, Type: agent.TextDelta, Text: "lo world"},
	})

	after := strings.Join(f.lines, "\n")
	if before == after {
		t.Fatal("lines did not change during streaming growth")
	}
	if !strings.Contains(after, "hello world") {
		t.Fatalf("expected 'hello world' in rendered output, got:\n%s", after)
	}
}

// TestApplyBatchPreservesScrollWhenNotFollowing verifies that a user browsing
// older output stays put on new batches.
func TestApplyBatchPreservesScrollWhenNotFollowing(t *testing.T) {
	f := NewFeed()
	f.width = 80
	f.height = 24

	events := make([]agent.Event, 0, 100)
	for i := 0; i < 50; i++ {
		events = append(events, agent.Event{Seq: 2*i + 1, Type: agent.UserMsg, Text: "user message"})
		events = append(events, agent.Event{Seq: 2*i + 2, Type: agent.TextDelta, Text: "assistant reply that is long enough to wrap and take up multiple lines in the viewport for scrolling purposes"})
	}
	f.applyBatch(events)

	f.follow = false
	f.scrollTop = 0
	scrollTopBefore := f.scrollTop

	f.applyBatch([]agent.Event{
		{Seq: 200, Type: agent.TextDelta, Text: "more streaming text"},
	})

	if f.scrollTop != scrollTopBefore {
		t.Fatalf("scrollTop changed while browsing: %d -> %d", scrollTopBefore, f.scrollTop)
	}
	if f.follow {
		t.Fatal("follow became true while browsing")
	}
	if f.unseen == 0 {
		t.Fatal("unseen not incremented while browsing")
	}
}

// TestApplyBatchSticksToBottomWhenFollowing verifies that when follow=true,
// new batches auto-scroll to the bottom.
func TestApplyBatchSticksToBottomWhenFollowing(t *testing.T) {
	f := NewFeed()
	f.width = 80
	f.height = 24

	f.applyBatch([]agent.Event{
		{Seq: 1, Type: agent.UserMsg, Text: "hello"},
		{Seq: 2, Type: agent.TextDelta, Text: "world"},
	})

	f.follow = true
	f.scrollToEnd()
	bottomBefore := f.scrollTop

	f.applyBatch([]agent.Event{
		{Seq: 3, Type: agent.TextDelta, Text: " more text that grows the feed"},
	})

	if !f.follow {
		t.Fatal("follow became false while at bottom")
	}
	if f.scrollTop < bottomBefore {
		t.Fatalf("scrollTop moved up while following: %d < %d", f.scrollTop, bottomBefore)
	}
}

// TestApplyBatchUnseenDuringModal verifies that unseen increments without
// display changes when a modal is visible.
func TestApplyBatchUnseenDuringModal(t *testing.T) {
	f := NewFeed()
	f.width = 80
	f.height = 24

	f.applyBatch([]agent.Event{
		{Seq: 1, Type: agent.UserMsg, Text: "hello"},
	})

	f.modalVisible = true
	unseenBefore := f.unseen

	f.applyBatch([]agent.Event{
		{Seq: 2, Type: agent.TextDelta, Text: "streaming behind modal"},
	})

	if f.unseen <= unseenBefore {
		t.Fatalf("unseen did not increment during modal: %d <= %d", f.unseen, unseenBefore)
	}
}

// TestApplyBatchEventOrdering verifies that events within a batch are applied
// in order, so later events see the state from earlier ones.
func TestApplyBatchEventOrdering(t *testing.T) {
	f := NewFeed()
	f.width = 80
	f.height = 24

	callID := "tool-1"
	f.applyBatch([]agent.Event{
		{Seq: 1, Type: agent.ToolStart, Call: &agent.ToolCall{ID: callID, Name: "bash"}},
		{Seq: 2, Type: agent.ToolEnd, Call: &agent.ToolCall{ID: callID, Name: "bash", Output: "done", OK: true}},
	})

	items := f.proj.Items()
	found := false
	for _, it := range items {
		if it.Tool != nil && it.Tool.Status == "done" {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("ToolEnd did not transition tool to done state")
	}
}

// TestApplyBatchEmptyBatch verifies that an empty batch is a no-op.
func TestApplyBatchEmptyBatch(t *testing.T) {
	f := NewFeed()
	f.width = 80
	f.height = 24

	f.applyBatch([]agent.Event{
		{Seq: 1, Type: agent.UserMsg, Text: "hello"},
	})

	before := strings.Join(f.lines, "\n")
	scrollTopBefore := f.scrollTop
	unseenBefore := f.unseen

	f.applyBatch([]agent.Event{})

	after := strings.Join(f.lines, "\n")
	if before != after {
		t.Fatal("empty batch changed lines")
	}
	if f.scrollTop != scrollTopBefore {
		t.Fatal("empty batch changed scrollTop")
	}
	if f.unseen != unseenBefore {
		t.Fatal("empty batch changed unseen")
	}
}
