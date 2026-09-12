package ui

import (
	"testing"

	"nabd/internal/agent"
)

// TestRunErrorRetiresProgressStatus pins the E8 regression: a journaled
// RunError must retire the progress row immediately, without waiting for
// doneMsg (which only lands when the runner goroutine returns).
func TestRunErrorRetiresProgressStatus(t *testing.T) {
	m := NewFeed()
	m.running = true
	m.busy = true

	if got := m.runtimeStatusText(); got != "Generating…" {
		t.Fatalf("precondition: want Generating…, got %q", got)
	}

	m.trackState(agent.Event{Type: agent.RunError, Seq: 1})

	got := m.runtimeStatusText()
	if got == "Generating…" || got == "Working…" {
		t.Fatalf("status still claims progress after RunError: %q", got)
	}
	if got != runFailedStatus {
		t.Fatalf("want %q, got %q", runFailedStatus, got)
	}
	if m.running {
		t.Fatal("running not cleared by RunError")
	}
	if m.runningTool != "" {
		t.Fatalf("runningTool not cleared by RunError: %q", m.runningTool)
	}
}

// TestRunErrorKeepsSendGate documents the deliberate limit of the fix: busy
// stays set so a second send cannot start before doneMsg proves the runner
// has actually returned.
func TestRunErrorKeepsSendGate(t *testing.T) {
	m := NewFeed()
	m.running = true
	m.busy = true

	m.trackState(agent.Event{Type: agent.RunError, Seq: 1})

	if !m.busy {
		t.Fatal("busy cleared by RunError: the send gate would open before doneMsg")
	}
	if !m.errorSeenSinceSend {
		t.Fatal("errorSeenSinceSend not set: doneMsg would add a duplicate notice")
	}
}

// TestInterruptedRetiresProgressStatus covers the cancel path: Interrupted
// is terminal too, and a running tool row must not survive it.
func TestInterruptedRetiresProgressStatus(t *testing.T) {
	m := NewFeed()
	m.running = true
	m.busy = true
	m.runningTool = "Bash"

	m.trackState(agent.Event{Type: agent.Interrupted, Seq: 2})

	got := m.runtimeStatusText()
	if got == "Generating…" || got == "Working…" || got == "Running Bash…" {
		t.Fatalf("status still claims progress after Interrupted: %q", got)
	}
	if got != runFailedStatus {
		t.Fatalf("want %q, got %q", runFailedStatus, got)
	}
}

// TestDoneMsgClearsRunFailedStatus keeps the transient row transient: once
// the runner returns, the permanent feed notice carries the failure and the
// status row goes back to empty.
func TestDoneMsgClearsRunFailedStatus(t *testing.T) {
	m := NewFeed()
	m.running = true
	m.busy = true
	m.trackState(agent.Event{Type: agent.RunError, Seq: 1})

	if _, _ = m.Update(doneMsg{}); m.status != "" {
		t.Fatalf("status not cleared by doneMsg: %q", m.status)
	}
	if m.busy {
		t.Fatal("busy not cleared by doneMsg")
	}
	if got := m.runtimeStatusText(); got == runFailedStatus {
		t.Fatalf("transient failure row survived doneMsg: %q", got)
	}
}
