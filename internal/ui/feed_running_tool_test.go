package ui

import (
	"nabd/internal/event"
	"testing"
)

func TestRunningToolClearedDuringPermAsk(t *testing.T) {
	f := NewFeed()

	// ToolStart starts the tool
	f.Update(agentEventBatchMsg{Events: []event.Event{{Type: event.ToolStart, Call: &event.ToolCall{ID: "1", Name: "bash"}}}})
	if f.runningTool != "bash" {
		t.Errorf("expected runningTool=bash after ToolStart, got %q", f.runningTool)
	}

	// PermAsk should hide it while the modal is up
	f.Update(agentEventBatchMsg{Events: []event.Event{{Type: event.PermAsk, Call: &event.ToolCall{ID: "1", Name: "bash"}}}})
	if f.runningTool != "" {
		t.Errorf("expected runningTool to be cleared during PermAsk, got %q", f.runningTool)
	}

	// PermReply (Deny) should leave it cleared
	f.Update(agentEventBatchMsg{Events: []event.Event{{Type: event.PermReply, Call: &event.ToolCall{ID: "1", Name: "bash"}, Decision: event.Deny}}})
	if f.runningTool != "" {
		t.Errorf("expected runningTool to remain empty after Deny, got %q", f.runningTool)
	}

	// Let's try Allow
	f.Update(agentEventBatchMsg{Events: []event.Event{{Type: event.ToolStart, Call: &event.ToolCall{ID: "2", Name: "write_file"}}}})
	f.Update(agentEventBatchMsg{Events: []event.Event{{Type: event.PermAsk, Call: &event.ToolCall{ID: "2", Name: "write_file"}}}})
	f.Update(agentEventBatchMsg{Events: []event.Event{{Type: event.PermReply, Call: &event.ToolCall{ID: "2", Name: "write_file"}, Decision: event.AllowOnce, RawDecision: event.AllowOnce}}})
	if f.runningTool != "write_file" {
		t.Errorf("expected runningTool to be restored after Allow, got %q", f.runningTool)
	}
}
