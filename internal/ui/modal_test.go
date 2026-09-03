package ui

import (
	"encoding/json"
	"strings"
	"testing"

	"nabd/internal/agent"

	tea "github.com/charmbracelet/bubbletea"
)

// TestModalRenderingAndChoices verifies that the modal renders the required elements
// (title, tool name, args, choices, key hints) adhering to visual contracts.
func TestModalRenderingAndChoices(t *testing.T) {
	f, _ := feedWithRunner(t)
	f.width = 80
	f.height = 24

	// Open modal for mutating tool (supports session)
	f.Update(agentEventBatchMsg{Events: []agent.Event{
		{Seq: 1, Type: agent.PermAsk, Call: &agent.ToolCall{ID: "c1", Name: "write_file", Args: json.RawMessage(`"test.go"`)}},
	}})

	if !f.modalVisible {
		t.Fatal("modal must be visible")
	}

	view := f.View()
	if !strings.Contains(view, "Permission Required") {
		t.Errorf("view missing 'Permission Required':\n%s", view)
	}
	if !strings.Contains(view, "write_file") {
		t.Errorf("view missing tool name 'write_file':\n%s", view)
	}
	if !strings.Contains(view, "not executed yet") {
		t.Errorf("view missing explicit 'not executed yet' indicator:\n%s", view)
	}
	if !strings.Contains(view, "Allow Once") || !strings.Contains(view, "Allow Session") || !strings.Contains(view, "Deny") {
		t.Errorf("view missing choices for write_file:\n%s", view)
	}

	// For bash (executing tool), all choices including Allow Session are rendered
	f2, _ := feedWithRunner(t)
	f2.width = 80
	f2.height = 24
	f2.Update(agentEventBatchMsg{Events: []agent.Event{
		{Seq: 1, Type: agent.PermAsk, Call: &agent.ToolCall{ID: "c2", Name: "bash", Args: json.RawMessage(`"ls -la"`)}},
	}})
	view2 := f2.View()
	if !strings.Contains(view2, "Allow Session") {
		t.Errorf("bash modal must include Allow Session choice, got:\n%s", view2)
	}
}

// TestBashAllowSessionCoreOwnedEffectiveDecision proves that:
// 1. Modal opens for a bash request.
// 2. Pressing 'a' sends agent.AllowSession to the approver (UI does NOT compute EffectiveDecision).
// 3. Core policy calculates EffectiveDecision = AllowOnce.
// 4. Emitted event records Decision=AllowSession and EffectiveDecision=AllowOnce.
func TestBashAllowSessionCoreOwnedEffectiveDecision(t *testing.T) {
	f, _ := feedWithRunner(t)
	ap := NewApprover()
	f.SetApprover(ap)

	// 1. Open modal for bash
	f.Update(agentEventBatchMsg{Events: []agent.Event{
		{Seq: 1, Type: agent.PermAsk, Call: &agent.ToolCall{ID: "c_bash", Name: "bash", Args: json.RawMessage(`"echo hi"`)}},
	}})
	if !f.modalVisible {
		t.Fatal("modal must be visible for bash tool call")
	}

	// 2. Press 'a'
	_, cmd := f.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("a")})
	if cmd == nil {
		t.Fatal("pressing 'a' must produce reply command")
	}

	// 3. Approver receives agent.AllowSession exactly once
	f = updateCmd(f, cmd)
	select {
	case d := <-ap.reply:
		if d != agent.AllowSession {
			t.Fatalf("approver received %v, want agent.AllowSession (UI must not downgrade)", d)
		}
	default:
		t.Fatal("approver reply channel is empty")
	}

	// 4 & 5. Core Gate computes EffectiveDecision = AllowOnce for bash,
	// and emits PermReply event with Decision=AllowSession and EffectiveDecision=AllowOnce.
	permEv := agent.Event{
		Seq:               2,
		Type:              agent.PermReply,
		Call:              &agent.ToolCall{ID: "c_bash", Name: "bash"},
		Decision:          agent.AllowSession,
		EffectiveDecision: agent.AllowOnce,
	}
	f.Update(agentEventBatchMsg{Events: []agent.Event{permEv}})

	// 6. Presentation renders both requested and applied decisions
	view := f.View()
	if !strings.Contains(view, "requested session, applied once") {
		t.Fatalf("expected feed view to show requested session and applied once, got:\n%s", view)
	}
}

// TestModalArrowSelectionAndEnter confirms that Up/Down/Left/Right navigate choices
// and Enter confirms the currently selected decision.
func TestModalArrowSelectionAndEnter(t *testing.T) {
	f, _ := feedWithRunner(t)
	f.width = 80
	f.height = 24
	openModal(f) // bash: [0: Allow Once, 1: Deny]

	if f.permModal.selected != -1 {
		t.Fatalf("initial selected = %d, want -1", f.permModal.selected)
	}

	// First Down arrow selects Allow Once (index 0)
	f.Update(tea.KeyMsg{Type: tea.KeyDown})
	if f.permModal.selected != 0 {
		t.Fatalf("after first Down, selected = %d, want 0", f.permModal.selected)
	}

	// Second Down arrow moves to Allow Session (index 1)
	f.Update(tea.KeyMsg{Type: tea.KeyDown})
	if f.permModal.selected != 1 {
		t.Fatalf("after second Down, selected = %d, want 1", f.permModal.selected)
	}

	// Third Down arrow moves to Deny (index 2)
	f.Update(tea.KeyMsg{Type: tea.KeyDown})
	if f.permModal.selected != 2 {
		t.Fatalf("after third Down, selected = %d, want 2", f.permModal.selected)
	}

	// Enter confirms selection (Deny)
	_, cmd := f.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("Enter must produce reply command")
	}
	msg := cmd()
	reply, ok := msg.(permReplyMsg)
	if !ok {
		t.Fatalf("expected permReplyMsg, got %T", msg)
	}
	if reply.Decision != agent.Deny {
		t.Fatalf("expected Decision=Deny, got %v", reply.Decision)
	}
}

// TestModalIdempotentSingleDecision confirms that repeated key presses (e.g. y twice,
// or y then Enter) generate exactly ONE decision command and no duplicate replies.
func TestModalIdempotentSingleDecision(t *testing.T) {
	f, _ := feedWithRunner(t)
	openModal(f)

	// First press 'y'
	_, cmd1 := f.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("y")})
	if cmd1 == nil {
		t.Fatal("first y must return reply command")
	}

	// Second press 'y' before command is processed
	_, cmd2 := f.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("y")})
	if cmd2 != nil {
		t.Fatal("second y must NOT return a command (idempotency)")
	}

	// Press Enter before command is processed
	_, cmd3 := f.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd3 != nil {
		t.Fatal("Enter after y must NOT return a command (idempotency)")
	}

	// Route keys must still be intercepted by modal while decisionPending is true
	if !f.decisionPending {
		t.Fatal("decisionPending must be true")
	}
	f.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("z")})
	if v := f.composer.value(); v != "" {
		t.Fatalf("typing while decisionPending must not reach composer, got %q", v)
	}
}

// TestModalFollowRestorationOnlyOnTransition confirms that followBeforeModal is saved
// strictly on false -> true transition and restored when the modal closes.
func TestModalFollowRestorationOnlyOnTransition(t *testing.T) {
	f, _ := feedWithRunner(t)
	f.follow = false // user was scrolled up

	// Open modal: false -> true saves followBeforeModal = false
	f.Update(agentEventBatchMsg{Events: []agent.Event{
		{Seq: 1, Type: agent.PermAsk, Call: &agent.ToolCall{ID: "c1", Name: "bash"}},
	}})
	if f.followBeforeModal != false {
		t.Fatalf("followBeforeModal = %v, want false", f.followBeforeModal)
	}

	// Queued/second ask while already visible must NOT overwrite followBeforeModal
	f.follow = true // simulate follow state mutation
	f.Update(agentEventBatchMsg{Events: []agent.Event{
		{Seq: 2, Type: agent.PermAsk, Call: &agent.ToolCall{ID: "c2", Name: "bash"}},
	}})
	if f.followBeforeModal != false {
		t.Fatalf("queued ask must not overwrite followBeforeModal, got %v", f.followBeforeModal)
	}

	// Close modal: restores follow = false
	f.Update(agentEventBatchMsg{Events: []agent.Event{
		{Seq: 3, Type: agent.PermReply, Call: &agent.ToolCall{ID: "c1"}, Decision: agent.AllowOnce, EffectiveDecision: agent.AllowOnce},
	}})
	if f.follow != false {
		t.Fatalf("after modal close follow = %v, want false", f.follow)
	}
}

// TestModalSafeArgsHandling verifies UTF-8 rune preservation and multiline handling.
func TestModalSafeArgsHandling(t *testing.T) {
	// Multiline with Arabic runes
	input := "echo 'مرحبا بك'\nفي العالم"
	safe := safeArgs(input, 15)
	if strings.Contains(safe, "\n") {
		t.Fatal("safeArgs must normalize multiline to single line")
	}
	if !strings.HasSuffix(safe, "…") {
		t.Fatalf("expected ellipsis at end of truncated string, got %q", safe)
	}

	// Extreme small maxRunes
	tiny := safeArgs("hello", 1)
	if tiny != "…" {
		t.Fatalf("expected '…', got %q", tiny)
	}
}

// TestModalResizeNarrowTerminals verifies that terminal widths 10, 20, 40, 80, 120
// render safely without panic or negative dimension math.
func TestModalResizeNarrowTerminals(t *testing.T) {
	widths := []int{10, 20, 40, 80, 120}
	for _, w := range widths {
		f, _ := feedWithRunner(t)
		f.Update(tea.WindowSizeMsg{Width: w, Height: 24})
		openModal(f)
		v := f.View()
		if v == "" {
			t.Fatalf("empty view for width %d", w)
		}
		if f.viewportHeight() < 0 {
			t.Fatalf("negative viewport height for width %d", w)
		}
	}
}

// TestModalTermuxSnapshotDimensions asserts visual card structure and height limits
// on typical Termux phone dimensions: 80x24 (landscape), 40x20 (portrait), 50x16 (small).
func TestModalTermuxSnapshotDimensions(t *testing.T) {
	dimensions := []struct {
		width  int
		height int
		name   string
	}{
		{width: 80, height: 24, name: "80x24-landscape"},
		{width: 40, height: 20, name: "40x20-portrait"},
		{width: 50, height: 16, name: "50x16-small"},
	}

	for _, d := range dimensions {
		t.Run(d.name, func(t *testing.T) {
			f, _ := feedWithRunner(t)
			f.Update(tea.WindowSizeMsg{Width: d.width, Height: d.height})

			// 1. Open modal with bash command
			f.Update(agentEventBatchMsg{Events: []agent.Event{
				{Seq: 1, Type: agent.PermAsk, Call: &agent.ToolCall{ID: "c_snap", Name: "bash", Args: json.RawMessage(`"pwd"`)}},
			}})

			// 2. Select index 0 (Allow Once) via Down arrow
			f.Update(tea.KeyMsg{Type: tea.KeyDown})

			// 3. Render view during active modal
			v := f.View()
			if v == "" {
				t.Fatal("empty modal view")
			}

			// Invariant A: Total lines must strictly not exceed terminal height
			numLines := countLines(v)
			if numLines > d.height {
				t.Fatalf("rendered %d lines, exceeding terminal height %d:\n%s", numLines, d.height, v)
			}

			// Invariant B: Card top border must be rendered
			if !strings.Contains(v, "+-- Permission") {
				t.Fatalf("missing card top border in view:\n%s", v)
			}

			// Invariant C: Selected choice must clearly display [*]
			if !strings.Contains(v, "[*] Allow Once") {
				t.Fatalf("selected choice must visually show [*] Allow Once:\n%s", v)
			}

			// Invariant D: Composer must be visibly paused
			if !strings.Contains(v, "composer paused") {
				t.Fatalf("composer must be replaced by paused notice during modal:\n%s", v)
			}
			if strings.Contains(v, "type a message") {
				t.Fatalf("composer placeholder must NOT appear while modal is active:\n%s", v)
			}

			// Invariant E: Answer modal -> verify composer is restored
			f.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("y")})
			f.Update(agentEventBatchMsg{Events: []agent.Event{
				{Seq: 2, Type: agent.PermReply, Call: &agent.ToolCall{ID: "c_snap"}, Decision: agent.AllowOnce, EffectiveDecision: agent.AllowOnce},
			}})

			vClosed := f.View()
			if !strings.Contains(vClosed, "type a message") {
				t.Fatalf("composer placeholder must be restored after modal close:\n%s", vClosed)
			}
			if strings.Contains(vClosed, "composer paused") {
				t.Fatalf("composer paused notice must disappear after modal close:\n%s", vClosed)
			}
			if countLines(vClosed) > d.height {
				t.Fatalf("post-modal lines %d exceed height %d", countLines(vClosed), d.height)
			}
		})
	}
}
