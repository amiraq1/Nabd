package ui

import (
	"nabd/internal/agent"
	"testing"
)

func TestRunningToolClearedDuringPermAsk(t *testing.T) {
	f := NewFeed()

	// ToolStart starts the tool
	f.Update(agentEventBatchMsg{Events: []agent.Event{{Type: agent.ToolStart, Call: &agent.ToolCall{ID: "1", Name: "bash"}}}})
	if f.runningTool != "bash" {
		t.Errorf("expected runningTool=bash after ToolStart, got %q", f.runningTool)
	}

	// PermAsk should hide it while the modal is up
	f.Update(agentEventBatchMsg{Events: []agent.Event{{Type: agent.PermAsk, Call: &agent.ToolCall{ID: "1", Name: "bash"}}}})
	if f.runningTool != "" {
		t.Errorf("expected runningTool to be cleared during PermAsk, got %q", f.runningTool)
	}

	// PermReply (Deny) should leave it cleared
	f.Update(agentEventBatchMsg{Events: []agent.Event{{Type: agent.PermReply, Call: &agent.ToolCall{ID: "1", Name: "bash"}, Decision: agent.Deny}}})
	if f.runningTool != "" {
		t.Errorf("expected runningTool to remain empty after Deny, got %q", f.runningTool)
	}

	// Let's try Allow
	f.Update(agentEventBatchMsg{Events: []agent.Event{{Type: agent.ToolStart, Call: &agent.ToolCall{ID: "2", Name: "write_file"}}}})
	f.Update(agentEventBatchMsg{Events: []agent.Event{{Type: agent.PermAsk, Call: &agent.ToolCall{ID: "2", Name: "write_file"}}}})
	f.Update(agentEventBatchMsg{Events: []agent.Event{{Type: agent.PermReply, Call: &agent.ToolCall{ID: "2", Name: "write_file"}, Decision: agent.AllowOnce, RawDecision: agent.AllowOnce}}})
	if f.runningTool != "write_file" {
		t.Errorf("expected runningTool to be restored after Allow, got %q", f.runningTool)
	}
}
