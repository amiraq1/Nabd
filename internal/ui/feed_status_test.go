package ui

import (
	"testing"
	"time"

	"nabd/internal/event"
	"nabd/internal/presentation"
)

// Note: Permission, Error, and Canceling phases are handled by modal.go and
// error_render.go respectively, and are intentionally outside the phaseText() contract.
func TestPhaseTextStreamingStatus(t *testing.T) {
	f := NewFeed()

	// 1. Idle state: returns empty string
	f.running = false
	f.busy = false
	if got := f.phaseText(); got != "" {
		t.Fatalf("phaseText when idle = %q, want empty", got)
	}

	// 2. Generating before first delta arrives
	f.running = true
	f.streamFirstDeltaAt = time.Time{}
	if got := f.phaseText(); got != "Generating…" {
		t.Fatalf("phaseText before delta = %q, want %q", got, "Generating…")
	}

	// 3. Streaming after first delta arrives (streamFirstDeltaAt is non-zero)
	f.streamFirstDeltaAt = time.Now()

	// Default Unicode mode: Streaming…
	t.Setenv("NABD_ASCII_ONLY", "")
	if got := f.phaseText(); got != "Streaming…" {
		t.Fatalf("phaseText during stream (Unicode) = %q, want %q", got, "Streaming…")
	}

	// ASCII-only mode: Streaming...
	t.Setenv("NABD_ASCII_ONLY", "1")
	if got := f.phaseText(); got != "Streaming..." {
		t.Fatalf("phaseText during stream (ASCII) = %q, want %q", got, "Streaming...")
	}

	// 4. End of run: even if streamFirstDeltaAt remains non-zero from the completed turn,
	// phaseText must return empty when run ends and feed is not busy.
	f.running = false
	f.busy = false
	if got := f.phaseText(); got != "" {
		t.Fatalf("phaseText after run end with lingering stream delta = %q, want empty", got)
	}
}

func TestPhaseTextCompactingAndToolPriority(t *testing.T) {
	f := NewFeed()
	f.running = true
	f.streamFirstDeltaAt = time.Now()

	// Tool takes priority over streaming
	f.statusProj = presentation.NewStatusProjector()
	f.statusProj.Apply(event.Event{
		Type: event.ToolStart,
		Call: &event.ToolCall{ID: "1", Name: "bash"},
	})
	if got := f.phaseText(); got != "Running bash…" {
		t.Fatalf("phaseText with active tool = %q, want %q", got, "Running bash…")
	}

	// Compacting phase takes highest priority over tools and streaming
	f.statusProj.Apply(event.Event{
		Type: event.Compact,
	})
	if got := f.phaseText(); got != "Compacting context…" {
		t.Fatalf("phaseText during compaction = %q, want %q", got, "Compacting context…")
	}
}
