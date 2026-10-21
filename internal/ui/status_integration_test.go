package ui

import (
	"strings"
	"testing"

	"nabd/internal/event"
)

func TestFeedStatusProjectorDrivesPermissionStatus(t *testing.T) {
	f := NewFeed()
	f.width = 60
	f.height = 12
	_, _ = f.Update(agentEventBatchMsg{Events: []event.Event{
		{Seq: 1, Type: event.TurnStart},
		{Seq: 2, Type: event.ToolStart, Call: &event.ToolCall{ID: "c1", Name: "bash"}},
		{Seq: 3, Type: event.PermAsk, Call: &event.ToolCall{ID: "c1", Name: "bash"}},
	}})
	if !strings.Contains(f.View(), "Permission Required") {
		t.Fatalf("view does not contain unified permission status: %q", f.View())
	}
}
