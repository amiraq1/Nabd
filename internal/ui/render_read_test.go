package ui

import (
	"strings"
	"testing"

	"nabd/internal/agent"

	"github.com/charmbracelet/x/ansi"
)

func TestReadEventRendersNothingWhenNotTruncated(t *testing.T) {
	e := agent.Event{
		Type: agent.EventRead,
		Read: &agent.ReadRecord{Path: "a.go"},
	}
	if got := RenderEvent(e, DefaultWidth); got != "" {
		t.Fatalf(
			"RenderEvent = %q, want empty; a complete read is not a screen event",
			got,
		)
	}
}

func TestReadEventRendersWarningWhenTruncated(t *testing.T) {
	e := agent.Event{
		Type: agent.EventRead,
		Read: &agent.ReadRecord{Path: "a.go", Truncated: true},
	}
	got := RenderEvent(e, DefaultWidth)
	if !strings.Contains(got, "partially read") || !strings.Contains(got, "✂") {
		t.Fatalf("RenderEvent = %q, want warning with ✂ and 'partially read'", got)
	}
}

func TestReadEventRendersNothingWhenNilRead(t *testing.T) {
	e := agent.Event{
		Type: agent.EventRead,
		Read: nil,
	}
	if got := RenderEvent(e, DefaultWidth); got != "" {
		t.Fatalf("RenderEvent = %q, want empty for nil ReadRecord", got)
	}
}

func TestTrulyUnknownEventTypeReachesFallback(t *testing.T) {
	e := agent.Event{Type: "future_unknown_event"}
	got := ansi.Strip(RenderEvent(e, DefaultWidth))
	if !strings.HasPrefix(got, "· future_unknown_event") {
		t.Fatalf("RenderEvent = %q, want it to reach unknown fallback with prefix '· '", got)
	}
}
