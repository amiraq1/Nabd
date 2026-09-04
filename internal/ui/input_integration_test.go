package ui

import (
	"strings"
	"testing"

	"nabd/internal/agent"
	"nabd/internal/presentation"

	tea "github.com/charmbracelet/bubbletea"
)

// TestIntegrationPhase3AFullSequence drives real tea.KeyMsg messages through
// Model.Update — the composer, input router, feed, modal and runner — in one
// deterministic script. No sleeps, no provider, no network.
func TestIntegrationPhase3AFullSequence(t *testing.T) {
	// Wire a recording runner.
	f := NewFeed()
	f.width = 80
	f.height = 24
	var sent []string
	f.SetRunner(runnerFunc(func(text string) error {
		sent = append(sent, text)
		return nil
	}))
	// Wire an approver that records decisions.
	ap := NewApprover()
	f.SetApprover(ap)

	// 1. Composer starts focused.
	if !f.composer.focused() {
		t.Fatal("composer must start focused")
	}

	// 2. Type ASCII.
	typeIntoFeed(t, f, "task: ")

	// 3. Newline via Ctrl+J.
	_, _ = f.Update(tea.KeyMsg{Type: tea.KeyCtrlJ})

	// 4. Type an Arabic line.
	typeIntoFeed(t, f, "افحص الملف")

	// 5. Up inside the text (not on the first line): plain cursor move, no
	// history (there is no history yet anyway).
	_, _ = f.Update(tea.KeyMsg{Type: tea.KeyUp})
	if got := f.composer.value(); got != "task: \nافحص الملف" {
		t.Fatalf("after typing + newline, value = %q", got)
	}

	// 6. Move back to the end and send with Enter.
	_, _ = f.Update(tea.KeyMsg{Type: tea.KeyEnd}) // composer cursor to end of line
	_, _ = f.Update(tea.KeyMsg{Type: tea.KeyDown})
	_, _ = f.Update(tea.KeyMsg{Type: tea.KeyEnd})
	mdl, cmd := f.Update(tea.KeyMsg{Type: tea.KeyEnter})
	f = mdl.(*Feed)
	if cmd == nil {
		t.Fatal("Enter must produce a run command")
	}

	// 7. The send callback fired exactly once with the full multiline text.
	msg := cmd()
	if _, ok := msg.(doneMsg); !ok {
		t.Fatalf("run cmd produced %T, want doneMsg", msg)
	}
	if len(sent) != 1 {
		t.Fatalf("send callback called %d times, want 1", len(sent))
	}
	if sent[0] != "task: \nافحص الملف" {
		t.Fatalf("sent text = %q, want full multiline text", sent[0])
	}

	// 8. Composer cleared.
	if v := f.composer.value(); v != "" {
		t.Fatalf("composer after send = %q, want empty", v)
	}
	if f.HistoryLen() != 1 {
		t.Fatalf("history len = %d, want 1", f.HistoryLen())
	}

	// Run finished.
	_, _ = f.Update(doneMsg{err: nil})
	if f.busy {
		t.Fatal("feed must be idle after run end")
	}

	// 9. Up recalls the message.
	_, _ = f.Update(tea.KeyMsg{Type: tea.KeyUp})
	if got := f.composer.value(); got != "task: \nافحص الملف" {
		t.Fatalf("Up recall = %q", got)
	}

	// 10. Modify the recalled message.
	typeIntoFeed(t, f, "!")
	if got := f.composer.value(); got != "task: \nافحص الملف!" {
		t.Fatalf("edited recall = %q", got)
	}
	if f.HistoryBrowsing() {
		t.Fatal("editing a recalled message must exit browsing")
	}

	// 11-13. Create a fresh draft, browse history, restore the draft.
	// Clear via ctrl+c (composer has text → clears).
	_, _ = f.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	if v := f.composer.value(); v != "" {
		t.Fatalf("composer after ctrl+c = %q, want empty", v)
	}
	// Type a new draft.
	typeIntoFeed(t, f, "draft in progress")
	// Up enters history (empty→first line) and recalls the ORIGINAL stored
	// message (the edited text was never sent, so it never entered history).
	_, _ = f.Update(tea.KeyMsg{Type: tea.KeyUp})
	if got := f.composer.value(); got != "task: \nافحص الملف" {
		t.Fatalf("browsing recall = %q, want the original stored message", got)
	}
	// Down restores the draft.
	_, _ = f.Update(tea.KeyMsg{Type: tea.KeyDown})
	if got := f.composer.value(); got != "draft in progress" {
		t.Fatalf("draft restore = %q, want 'draft in progress'", got)
	}

	// 14. Open the permission modal via a PermAsk event.
	openModal(f)
	if !f.modalVisible {
		t.Fatal("modal must be visible")
	}
	if f.composer.focused() {
		t.Fatal("composer must be blurred while the modal is visible")
	}

	// 15. Keys while the modal is open: composer must not change. The typed
	// text must avoid y/a/n (modal answer keys: allow/session/deny).
	before := f.composer.value()
	typeIntoFeed(t, f, "1234!@")
	_, _ = f.Update(tea.KeyMsg{Type: tea.KeyEnter})
	_, _ = f.Update(tea.KeyMsg{Type: tea.KeyUp})
	if got := f.composer.value(); got != before {
		t.Fatalf("composer changed during modal: %q → %q (modalVisible=%v, focused=%v)",
			before, got, f.modalVisible, f.composer.focused())
	}
	if !f.modalVisible {
		t.Fatal("modal must still be visible after ordinary keys")
	}

	// 16. Agent events still reach the feed behind the modal.
	_, _ = f.Update(agentEventBatchMsg{Events: []agent.Event{
		{Seq: 2, Type: agent.TextDelta, Text: "assistant behind modal"},
		{Seq: 3, Type: agent.TurnEnd},
	}})
	items := f.proj.Items()
	var saw bool
	for _, it := range items {
		if it.Type == presentation.ItemAssistant && strings.Contains(it.Text, "assistant behind modal") {
			saw = true
		}
	}
	if !saw {
		t.Fatal("feed must keep projecting events while the modal is visible")
	}

	// 17. Answer the modal with y.
	_, cmd = f.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("y")})
	if cmd == nil {
		t.Fatal("y during modal must produce a reply command")
	}
	f = updateCmd(f, cmd)
	if f.modalVisible {
		t.Fatal("modal must close after the answer")
	}

	// 18. Focus restored.
	if !f.composer.focused() {
		t.Fatal("composer focus must be restored after the modal closes")
	}

	// 19. Resize: text and focus preserved.
	typeIntoFeed(t, f, "post-modal text")
	_, _ = f.Update(tea.WindowSizeMsg{Width: 50, Height: 12})
	if got := f.composer.value(); got != "draft in progress"+"post-modal text" {
		t.Fatalf("resize lost text: %q", got)
	}
	if !f.composer.focused() {
		t.Fatal("resize lost focus")
	}

	// 20. Oversized paste is rejected and previous text survives.
	_, _ = f.Update(tea.KeyMsg{Type: tea.KeyCtrlC}) // clear
	typeIntoFeed(t, f, "pre-paste")
	pasteInto(f, strings.Repeat("x\n", 250))
	if got := f.composer.value(); got != "pre-paste" {
		t.Fatalf("oversized paste must be rejected; got %q", got)
	}
	if f.status == "" {
		t.Fatal("oversized paste must set a notice")
	}

	// 21. Ctrl+C during a run cancels (direct context cancel, no Quit).
	f2, r := feedWithBlockingRunner(t)
	startBlockingRun(t, f2, r, "long running task")
	_, cmd = f2.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	if cmd != nil {
		if _, ok := cmd().(tea.QuitMsg); ok {
			t.Fatal("Ctrl+C during a run must not quit")
		}
	}
	r.waitReturned(t)

	// 22. Ctrl+D with text deletes a rune, does not quit.
	typeIntoFeed(t, f2, "abc")
	_, cmd = f2.Update(tea.KeyMsg{Type: tea.KeyCtrlD})
	if cmd != nil {
		t.Fatal("Ctrl+D with text must not produce a quit command")
	}

	// 23. Ctrl+D in the safe state quits.
	f3 := NewFeed()
	_, cmd = f3.Update(tea.KeyMsg{Type: tea.KeyCtrlD})
	if cmd == nil {
		t.Fatal("Ctrl+D in the safe state must quit")
	}
	msg2 := cmd()
	if _, ok := msg2.(tea.QuitMsg); !ok {
		t.Fatalf("safe Ctrl+D produced %T, want QuitMsg", msg2)
	}
}
