package ui

import (
	"encoding/json"
	"nabd/internal/event"
	"strings"
	"testing"
)

func TestHostilePayload(t *testing.T) {
	hostile := "alert\x1b[31mred\x1b[0m\x1b]8;;http://x\x1b\\link\x1b[0m\x07beep"
	f := NewFeed()
	f.width = 80
	f.height = 24
	args := json.RawMessage(`{"test":"` + hostile + `"}`)
	events := []event.Event{
		{Seq: 1, Type: event.RunStart, Text: hostile},
		{Seq: 2, Type: event.UserMsg, Text: hostile},
		{Seq: 3, Type: event.TextDelta, Text: hostile},
		{Seq: 4, Type: event.TurnEnd},
		{Seq: 5, Type: event.ToolStart, Call: &event.ToolCall{ID: "call_1", Name: hostile, Args: args}},
		{Seq: 6, Type: event.ToolEnd, Call: &event.ToolCall{ID: "call_1", Name: hostile, Args: args, Output: hostile, Signal: hostile, MS: 10}},
		{Seq: 7, Type: event.PermAsk, Call: &event.ToolCall{ID: "call_2", Name: hostile, Args: args}},
		{Seq: 8, Type: event.Notice, Text: hostile},
		{Seq: 9, Type: event.RunError, Err: hostile},
	}
	f.BuildFromEvents(events)
	f.addDiagnostic(hostile)
	f.status = hostile

	// Composer content could be hostile, we test if textarea escapes it?
	// The standard textarea does NOT escape ANSI, but our View shouldn't leak it in our custom UI elements.
	// Actually, wait, if the composer itself leaks ANSI, then we should fail?
	// The textarea is typed in by the user. If the user pastes ANSI, it enters the textarea.
	// The user prompt says "including constructor echos, status, diagnostics, command list".
	f.composer.setValue("/" + hostile)
	f.menu.open(supportedSlashCommands)

	view := f.View()

	leaks := []string{
		"\x1b]8;;",   // hyperlink start
		"link\x1b\\", // hyperlink end with text
		"\x07beep",   // bell
	}

	for _, leak := range leaks {
		if strings.Contains(view, leak) {
			t.Errorf("View() leaked hostile payload sequence %q", leak)
		}
	}

	// Check literal string
	if strings.Contains(view, "\x1b[31mred\x1b[0m") {
		t.Errorf("View() leaked ANSI color from payload")
	}
}
