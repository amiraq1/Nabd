package ui

import (
	"strings"
	"testing"

	"nabd/internal/agent"
)

func TestFeedStatusProjectorDrivesPermissionStatus(t *testing.T) {
	f := NewFeed()
	f.width = 60
	f.height = 12
	_, _ = f.Update(agentEventBatchMsg{Events: []agent.Event{
		{Seq: 1, Type: agent.TurnStart},
		{Seq: 2, Type: agent.ToolStart, Call: &agent.ToolCall{ID: "c1", Name: "bash"}},
		{Seq: 3, Type: agent.PermAsk, Call: &agent.ToolCall{ID: "c1", Name: "bash"}},
	}})
	if !strings.Contains(f.View(), "Permission Required") {
		t.Fatalf("view does not contain unified permission status: %q", f.View())
	}
}
