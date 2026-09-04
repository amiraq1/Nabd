package ui

import (
	"testing"

	"nabd/internal/agent"
)

// TestFollowResumesAfterModalClose: while the modal is visible, visible
// auto-scroll pauses and unseen grows; after the modal closes (PermReply
// event), follow mode resumes and scroll returns to the bottom.
func TestFollowResumesAfterModalClose(t *testing.T) {
	f := NewFeed()
	f.width = 80
	f.height = 24
	f.follow = true
	_, _ = f.Update(agentEventBatchMsg{Events: []agent.Event{
		{Seq: 1, Type: agent.RunStart, Text: "start"},
	}})

	// Modal opens.
	_, _ = f.Update(agentEventBatchMsg{Events: []agent.Event{
		{Seq: 2, Type: agent.PermAsk, Call: &agent.ToolCall{ID: "c1", Name: "bash"}},
	}})
	if !f.modalVisible {
		t.Fatal("modal must be visible")
	}
	// Events behind the modal: unseen grows, follow stays true but scroll
	// is paused.
	_, _ = f.Update(agentEventBatchMsg{Events: []agent.Event{
		{Seq: 3, Type: agent.TextDelta, Text: "behind the modal"},
	}})
	if f.unseen == 0 {
		t.Fatal("unseen must grow while the modal pauses auto-scroll")
	}

	// The loop answers: PermReply event closes the modal.
	_, _ = f.Update(agentEventBatchMsg{Events: []agent.Event{
		{Seq: 4, Type: agent.PermReply, Call: &agent.ToolCall{ID: "c1"}, Decision: agent.AllowOnce, RawDecision: agent.AllowOnce},
	}})
	if f.modalVisible {
		t.Fatal("modal must close on PermReply")
	}
	// Follow mode resumes: unseen cleared, viewport at bottom.
	if f.follow != true {
		t.Fatal("follow must remain/return after the modal closes")
	}
	if f.unseen != 0 {
		t.Fatalf("unseen after modal close = %d, want 0", f.unseen)
	}
}

// TestFocusRestoredFromEventPath: when the modal closes through the event
// stream (not through answerModal), composer focus is restored when no run
// is in flight.
func TestFocusRestoredFromEventPath(t *testing.T) {
	f := NewFeed()
	f.width = 80
	f.height = 24
	// Open the modal (run not busy: a simulated standalone ask).
	_, _ = f.Update(agentEventBatchMsg{Events: []agent.Event{
		{Seq: 1, Type: agent.PermAsk, Call: &agent.ToolCall{ID: "c1", Name: "read_file"}},
	}})
	if f.composer.focused() {
		t.Fatal("composer must blur while the modal is visible")
	}
	// Deny via the event stream (loop-side decision).
	_, _ = f.Update(agentEventBatchMsg{Events: []agent.Event{
		{Seq: 2, Type: agent.PermReply, Call: &agent.ToolCall{ID: "c1"}, Decision: agent.Deny, RawDecision: agent.Deny},
	}})
	if f.modalVisible {
		t.Fatal("modal must close")
	}
	if !f.composer.focused() {
		t.Fatal("composer focus must be restored via the event path")
	}
}
