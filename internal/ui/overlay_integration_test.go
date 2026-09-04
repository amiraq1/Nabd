package ui

import (
	"encoding/json"
	"strings"
	"testing"

	"nabd/internal/agent"

	tea "github.com/charmbracelet/bubbletea"
)

// TestOverlayFullEndToEndIntegration executes the comprehensive Phase 3B integration test:
// Composer focused → type '/' → menu appears → type query → Down → completion →
// verify NO execution → Enter → command dispatch → permission request → Modal appears →
// event arrives → Feed updates → Composer ignores keypress → select decision → confirm →
// exactly one decision → Modal closes → focus restored → follow state restored →
// resize (10, 40, 80, 120) → verify no panic → verify no duplicate events.
func TestOverlayFullEndToEndIntegration(t *testing.T) {
	f, r := feedWithRunner(t)
	f.width = 80
	f.height = 24
	ap := NewApprover()
	f.SetApprover(ap)

	var undoCalls int
	f.SetCallbacks(&FeedCallbacks{
		OnUndo: func(n int) string {
			undoCalls++
			return "undone"
		},
	})

	// 1. Initial state: Composer focused, overlays closed
	if !f.composer.focused() {
		t.Fatal("step 1: composer must be focused initially")
	}
	if f.modalVisible || f.menu.visible {
		t.Fatal("step 1: overlays must be closed initially")
	}

	// 2. Type '/': Menu appears
	typeIntoFeed(t, f, "/")
	if !f.menu.visible {
		t.Fatal("step 2: slash menu must appear on '/'")
	}
	if len(f.menu.items) != 6 {
		t.Fatalf("step 2: expected 6 commands, got %d", len(f.menu.items))
	}

	// 3. Type query 'un': filtered to /undo
	typeIntoFeed(t, f, "un")
	if !f.menu.visible {
		t.Fatal("step 3: menu must remain visible while filtering")
	}
	if len(f.menu.items) != 1 || f.menu.items[0].Name != "/undo" {
		t.Fatalf("step 3: expected only /undo, got %+v", f.menu.items)
	}

	// 4. Down arrow: navigate inside menu
	f.Update(tea.KeyMsg{Type: tea.KeyDown})
	if f.HistoryBrowsing() {
		t.Fatal("step 4: navigation inside menu must NOT open history browsing")
	}

	// 5. First Enter: completion only. MUST NOT EXECUTE!
	_, cmdCompletion := f.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmdCompletion != nil {
		t.Fatal("step 5: completion must NOT produce an execution command")
	}
	if undoCalls != 0 {
		t.Fatal("step 5: completion must NOT call OnUndo")
	}
	if r.textsLen() != 0 {
		t.Fatal("step 5: completion must NOT invoke runner")
	}
	if f.menu.visible {
		t.Fatal("step 5: menu must close after completion")
	}
	if got := f.composer.value(); got != "/undo " {
		t.Fatalf("step 5: composer text = %q, want '/undo '", got)
	}

	// 6. Type '2' and press Enter: command dispatch
	typeIntoFeed(t, f, "2")
	_, cmdDispatch := f.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmdDispatch != nil {
		t.Fatal("step 6: slash command is handled synchronously, no async cmd")
	}
	if undoCalls != 1 {
		t.Fatalf("step 6: expected OnUndo to be called once, got %d", undoCalls)
	}
	if f.composer.value() != "" {
		t.Fatalf("step 6: composer must be cleared on successful command, got %q", f.composer.value())
	}
	if f.status != "undone" {
		t.Fatalf("step 6: status = %q, want 'undone'", f.status)
	}

	// 7. Permission request arrives: Modal appears
	initialFollow := f.follow
	f.Update(agentEventBatchMsg{Events: []agent.Event{
		{Seq: 1, Type: agent.PermAsk, Call: &agent.ToolCall{ID: "call_1", Name: "write_file", Args: json.RawMessage(`"main.go"`)}},
	}})
	if !f.modalVisible {
		t.Fatal("step 7: modal must be visible after PermAsk")
	}
	if f.composer.focused() {
		t.Fatal("step 7: composer must lose focus when modal opens")
	}

	// 8. Event arrives behind the modal: feed projects, auto-scroll pauses
	f.Update(agentEventBatchMsg{Events: []agent.Event{
		{Seq: 2, Type: agent.TextDelta, Text: "background stream"},
	}})
	if f.unseen == 0 {
		t.Fatal("step 8: unseen count must increment behind modal")
	}

	// 9. Composer ignores keypress while modal is visible
	beforeComposer := f.composer.value()
	f.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("ignored text")})
	f.Update(tea.KeyMsg{Type: tea.KeySpace})
	if f.composer.value() != beforeComposer {
		t.Fatalf("step 9: composer text changed during modal: %q → %q", beforeComposer, f.composer.value())
	}

	// 10. Select decision and confirm: arrow navigation and Enter
	// Modal has choices: [0: Allow Once, 1: Allow Session, 2: Deny]
	f.Update(tea.KeyMsg{Type: tea.KeyDown}) // select 0 (Allow Once)
	f.Update(tea.KeyMsg{Type: tea.KeyDown}) // select 1 (Allow Session)
	if f.permModal.selected != 1 {
		t.Fatalf("step 10: expected selected=1 (Allow Session), got %d", f.permModal.selected)
	}

	// 11. Confirm selection with Enter: exactly ONE decision
	_, cmdReply1 := f.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmdReply1 == nil {
		t.Fatal("step 11: Enter must return perm-reply command")
	}

	// Duplicate Enter press before command processed: must be swallowed
	_, cmdReply2 := f.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmdReply2 != nil {
		t.Fatal("step 11: duplicate Enter must NOT produce second command (idempotency)")
	}

	// Duplicate 'y' press before command processed: must be swallowed
	_, cmdReply3 := f.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("y")})
	if cmdReply3 != nil {
		t.Fatal("step 11: duplicate 'y' must NOT produce command while pending")
	}

	// Process the single reply command
	f = updateCmd(f, cmdReply1)

	// Verify reply reached Approver channel
	select {
	case d := <-ap.reply:
		if d != agent.AllowSession {
			t.Fatalf("step 11: expected Approver received AllowSession, got %v", d)
		}
	default:
		t.Fatal("step 11: Approver reply channel is empty")
	}
	// Verify no second decision in channel
	select {
	case d2 := <-ap.reply:
		t.Fatalf("step 11: unexpected second reply in channel: %v", d2)
	default:
		// channel empty as expected
	}

	// 12. Core emits PermReply: Modal closes, focus restored, follow restored
	f.Update(agentEventBatchMsg{Events: []agent.Event{
		{Seq: 3, Type: agent.PermReply, Call: &agent.ToolCall{ID: "call_1"}, Decision: agent.AllowSession, RawDecision: agent.AllowSession},
	}})
	if f.modalVisible {
		t.Fatal("step 12: modal must close on PermReply")
	}
	if !f.composer.focused() {
		t.Fatal("step 12: composer focus must be restored after modal close")
	}
	if f.follow != initialFollow {
		t.Fatalf("step 12: follow state must be restored to %v, got %v", initialFollow, f.follow)
	}

	// 13. Terminal resize across 10, 40, 80, 120 columns: no panic, valid dimensions
	for _, width := range []int{10, 40, 80, 120} {
		f.Update(tea.WindowSizeMsg{Width: width, Height: 24})
		if f.viewportHeight() < 0 {
			t.Fatalf("step 13: negative viewport height at width %d", width)
		}
		v := f.View()
		if v == "" {
			t.Fatalf("step 13: empty view at width %d", width)
		}
	}

	// 14. Verify no duplicate events in presentation items
	items := f.proj.Items()
	var seenDelta int
	for _, it := range items {
		if strings.Contains(it.Text, "background stream") {
			seenDelta++
		}
	}
	if seenDelta != 1 {
		t.Fatalf("step 14: expected exactly 1 delta item, got %d", seenDelta)
	}
}
