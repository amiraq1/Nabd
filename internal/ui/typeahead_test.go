package ui

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"nabd/internal/agent"

	tea "github.com/charmbracelet/bubbletea"
)

func setupTestFeed(t *testing.T) (*Feed, *runnerRecorder) {
	t.Helper()
	f, r := feedWithRunner(t)
	r.decisionCh = make(chan agent.Decision, 10)
	f.SetApprover(&Approver{reply: r.decisionCh})
	return f, r
}

func press(f *Feed, k tea.KeyMsg) {
	_, cmd := f.Update(k)
	if cmd != nil {
		_ = updateCmd(f, cmd)
	}
}

func runnerDecision(r *runnerRecorder) (agent.Decision, bool) {
	if r == nil || r.decisionCh == nil {
		return agent.Deny, false
	}
	select {
	case d := <-r.decisionCh:
		return d, true
	default:
		return agent.Deny, false
	}
}

func openModalWithCall(f *Feed) {
	_, _ = f.Update(agentEventBatchMsg{Events: []agent.Event{
		{Seq: 1, Type: agent.PermAsk, Call: &agent.ToolCall{
			ID: "ta1", Name: "bash",
			SessionGrantKnown: true, SessionGrantAllowed: true,
		}},
	}})
}

// TestPermissionModalIgnoresTypeaheadDecisionKeys verifies that decision keys
// during arm delay are ignored, while Esc immediately denies.
func TestPermissionModalIgnoresTypeaheadDecisionKeys(t *testing.T) {
	now := time.Now()
	setModalClock(func() time.Time { return now })
	t.Cleanup(func() { setModalClock(nil) })

	// Each key is tested on a fresh modal during the arm delay window.
	for _, k := range []tea.KeyMsg{
		{Type: tea.KeyRunes, Runes: []rune{'y'}},
		{Type: tea.KeyRunes, Runes: []rune{'a'}},
		{Type: tea.KeyRunes, Runes: []rune{'n'}},
		{Type: tea.KeyEnter},
	} {
		f, r := setupTestFeed(t)
		openModalWithCall(f)
		press(f, k)
		if d, ok := runnerDecision(r); ok {
			t.Errorf("key %q produced decision %v during arm delay — typeahead vulnerability", k.String(), d)
		}
	}

	// Esc during arm delay must deny immediately on an active modal.
	f, r := setupTestFeed(t)
	openModalWithCall(f)
	press(f, tea.KeyMsg{Type: tea.KeyEsc})
	d, ok := runnerDecision(r)
	if !ok || d != agent.Deny {
		t.Errorf("Esc during arm delay must immediately deny; got decision=%v ok=%v", d, ok)
	}
}

// TestPermissionModalAcceptsDecisionAfterArmDelay verifies that after the arm delay
// elapses, decision keys are honored, and that an ignored key resets the delay.
func TestPermissionModalAcceptsDecisionAfterArmDelay(t *testing.T) {
	now := time.Now()
	setModalClock(func() time.Time { return now })
	t.Cleanup(func() { setModalClock(nil) })

	// 1. Before arm delay expires: y produces no decision.
	f1, r1 := setupTestFeed(t)
	openModalWithCall(f1)
	press(f1, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	if d, ok := runnerDecision(r1); ok {
		t.Errorf("y during arm delay produced decision %v; want none", d)
	}

	// 2. Advance clock past arm delay: y produces AllowOnce.
	f2, r2 := setupTestFeed(t)
	openModalWithCall(f2)
	now = now.Add(ModalArmDelay + time.Millisecond)
	press(f2, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	if d, ok := runnerDecision(r2); !ok || d != agent.AllowOnce {
		t.Errorf("y after arm delay: got decision=%v ok=%v, want AllowOnce", d, ok)
	}

	// 3. Advance clock past arm delay: a produces AllowSession.
	f3, r3 := setupTestFeed(t)
	openModalWithCall(f3)
	now = now.Add(ModalArmDelay + time.Millisecond)
	press(f3, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	if d, ok := runnerDecision(r3); !ok || d != agent.AllowSession {
		t.Errorf("a after arm delay: got decision=%v ok=%v, want AllowSession", d, ok)
	}

	// 4. Advance clock past arm delay: n produces Deny.
	f4, r4 := setupTestFeed(t)
	openModalWithCall(f4)
	now = now.Add(ModalArmDelay + time.Millisecond)
	press(f4, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})
	if d, ok := runnerDecision(r4); !ok || d != agent.Deny {
		t.Errorf("n after arm delay: got decision=%v ok=%v, want Deny", d, ok)
	}

	// 5. Ignored key resets arm delay (rearm):
	f5, r5 := setupTestFeed(t)
	openModalWithCall(f5)
	// Key during arm delay is ignored and resets arm delay:
	press(f5, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
	now = now.Add(200 * time.Millisecond)
	press(f5, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	if d, ok := runnerDecision(r5); ok {
		t.Errorf("y immediately after ignored key produced decision %v; arm delay should have reset", d)
	}
	// After arm delay expires from rearm:
	now = now.Add(ModalArmDelay + time.Millisecond)
	press(f5, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	if d, ok := runnerDecision(r5); !ok || d != agent.AllowOnce {
		t.Errorf("y after reset arm delay: got decision=%v ok=%v, want AllowOnce", d, ok)
	}
}

// TestPermissionModalArmDelayShowsHint verifies wait hint appears during arm delay and disappears after.
func TestPermissionModalArmDelayShowsHint(t *testing.T) {
	now := time.Now()
	setModalClock(func() time.Time { return now })
	t.Cleanup(func() { setModalClock(nil) })

	f, _ := setupTestFeed(t)
	openModalWithCall(f)

	viewDuring := f.View()
	if !strings.Contains(viewDuring, "wait") && !strings.Contains(viewDuring, "Wait") {
		t.Errorf("view during arm delay missing wait hint:\n%s", viewDuring)
	}

	now = now.Add(ModalArmDelay + time.Millisecond)
	viewAfter := f.View()
	if strings.Contains(viewAfter, "wait") || strings.Contains(viewAfter, "Wait") {
		t.Errorf("wait hint must disappear after arm delay:\n%s", viewAfter)
	}
}

// TestNavigationYNoLongerCopiesOrApproves verifies y in navigation mode does not copy or approve.
func TestNavigationYNoLongerCopiesOrApproves(t *testing.T) {
	f, r := setupTestFeed(t)
	f.width = 80
	f.height = 24

	f.BuildFromEvents([]agent.Event{
		{Seq: 1, Type: agent.ToolStart, Call: &agent.ToolCall{ID: "n1", Name: "bash"}},
		{Seq: 2, Type: agent.ToolEnd, Call: &agent.ToolCall{
			ID: "n1", Name: "bash", OK: true, Output: "hello world",
		}},
	})
	f.refresh()
	f.selectedItem = 0

	f.enterNavigation()
	if !f.navigationMode {
		t.Fatal("must be in navigation mode")
	}

	var buf bytes.Buffer
	f.SetClipboardWriter(&buf)
	press(f, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	if d, ok := runnerDecision(r); ok {
		t.Errorf("y in navigation mode produced permission decision %v", d)
	}
	if buf.Len() > 0 {
		t.Errorf("y in navigation mode wrote to clipboard: %q", buf.String())
	}

	buf.Reset()
	press(f, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'c'}})
	if buf.Len() == 0 {
		t.Error("c in navigation mode should write to clipboard")
	}

	buf.Reset()
	press(f, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'Y'}})
	if buf.Len() == 0 {
		t.Error("Y in navigation mode should write to clipboard")
	}
}

// TestPermissionModalEnterNeverAllows verifies Enter produces no decision (with or without arrows).
func TestPermissionModalEnterNeverAllows(t *testing.T) {
	now := time.Now()
	setModalClock(func() time.Time { return now })
	t.Cleanup(func() { setModalClock(nil) })

	// Case 1: Without arrows, after arm delay
	f1, r1 := setupTestFeed(t)
	openModalWithCall(f1)
	now = now.Add(ModalArmDelay + time.Millisecond)

	press(f1, tea.KeyMsg{Type: tea.KeyEnter})
	if d, ok := runnerDecision(r1); ok {
		t.Errorf("Enter without arrows after arm delay produced decision %v; contract 0.2 requires no decision", d)
	}

	// Case 2: With arrows (KeyUp/KeyDown), after arm delay
	f2, r2 := setupTestFeed(t)
	openModalWithCall(f2)
	now = now.Add(ModalArmDelay + time.Millisecond)

	press(f2, tea.KeyMsg{Type: tea.KeyDown})
	press(f2, tea.KeyMsg{Type: tea.KeyDown})
	press(f2, tea.KeyMsg{Type: tea.KeyEnter})
	if d, ok := runnerDecision(r2); ok {
		t.Errorf("Enter with arrows after arm delay produced decision %v; contract 0.2 requires no decision", d)
	}
}
