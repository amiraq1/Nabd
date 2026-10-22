package ui

import (
	"strings"
	"testing"

	"nabd/internal/event"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

// TestFlushJoinColorsAgentText verifies the agent response text block
// is rendered with a distinct color style (not plain).
func TestFlushJoinColorsAgentText(t *testing.T) {
	// Force color output even without a TTY.
	lipgloss.SetColorProfile(termenv.ANSI)
	defer lipgloss.SetColorProfile(termenv.Ascii)

	buf := "hello world"
	e := event.Event{Type: event.TurnEnd} // renders as ""
	got := flushJoin(&buf, e, 80)
	// The agent text must contain ANSI color codes (not plain text).
	if !strings.Contains(got, "\x1b[") {
		t.Fatalf("agent text has no color styling, got %q", got)
	}
}
