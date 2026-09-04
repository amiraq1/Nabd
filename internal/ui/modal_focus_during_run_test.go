package ui

import (
	"testing"

	"nabd/internal/agent"

	tea "github.com/charmbracelet/bubbletea"
)

// TestFocusRestoredAfterModalDuringRun: the composer is re-focused as soon
// as a permission modal closes, even when the agent run is still in flight
// (typing the next draft is allowed during a run).
func TestFocusRestoredAfterModalDuringRun(t *testing.T) {
	f, r := feedWithBlockingRunner(t)
	startBlockingRun(t, f, r, "tool request")
	openModal(f)
	if f.composer.focused() {
		t.Fatal("composer must blur while the modal is visible")
	}
	// Answer deny: focus returns although the run is still busy.
	_, cmd := f.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("n")})
	f = updateCmd(f, cmd)
	if f.modalVisible {
		t.Fatal("modal must close after n")
	}
	if !f.composer.focused() {
		t.Fatal("composer focus must return after the modal closes, even mid-run")
	}
	// Typing a next draft works while the run continues.
	typeIntoFeed(t, f, "next draft")
	if v := f.composer.value(); v != "next draft" {
		t.Fatalf("draft while run continues = %q", v)
	}
	close(r.release)
	r.waitReturned(t)
	// Also via the event path (loop-side denial): focus returns.
	f2, r2 := feedWithBlockingRunner(t)
	startBlockingRun(t, f2, r2, "another run")
	openModal(f2)
	_, _ = f2.Update(agentEventBatchMsg{Events: []agent.Event{
		{Seq: 9, Type: agent.PermReply, Call: &agent.ToolCall{ID: "c1"}, Decision: agent.Deny, RawDecision: agent.Deny},
	}})
	if !f2.composer.focused() {
		t.Fatal("focus must return via the PermReply event path while busy")
	}
	close(r2.release)
	r2.waitReturned(t)
}
