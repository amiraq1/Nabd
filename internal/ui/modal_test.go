package ui

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"nabd/internal/agent"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
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

// TestBashAllowSessionCoreOwnedRawDecision proves that:
// 1. Modal opens for a bash request.
// 2. Pressing 'a' sends agent.AllowSession to the approver (UI does NOT compute RawDecision).
// 3. Core policy calculates RawDecision = AllowOnce.
// 4. Emitted event records Decision=AllowSession and RawDecision=AllowOnce.
func TestBashAllowSessionCoreOwnedRawDecision(t *testing.T) {
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
	f.permModal.armedAt = time.Time{} // armed
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

	// 4 & 5. Core Gate computes RawDecision = AllowOnce for bash,
	// and emits PermReply event with Decision=AllowSession and RawDecision=AllowOnce.
	permEv := agent.Event{
		Seq:         2,
		Type:        agent.PermReply,
		Call:        &agent.ToolCall{ID: "c_bash", Name: "bash"},
		Decision:    agent.AllowSession,
		RawDecision: agent.AllowOnce,
	}
	f.Update(agentEventBatchMsg{Events: []agent.Event{permEv}})

	// 6. Presentation renders both requested and applied decisions
	view := f.View()
	if !strings.Contains(view, "requested session, applied once") {
		t.Fatalf("expected feed view to show requested session and applied once, got:\n%s", view)
	}
}

// TestModalArrowSelectionAndEnter confirms that Up/Down/Left/Right and Enter
// do not produce any decision under contract 0.2 (decisions are literal keys only).
func TestModalArrowSelectionAndEnter(t *testing.T) {
	f, _ := feedWithRunner(t)
	f.width = 80
	f.height = 24
	openModal(f)
	f.permModal.armedAt = time.Time{} // armed

	// Arrow keys produce no command
	for _, k := range []tea.KeyMsg{
		{Type: tea.KeyDown},
		{Type: tea.KeyUp},
		{Type: tea.KeyLeft},
		{Type: tea.KeyRight},
	} {
		_, cmd := f.Update(k)
		if cmd != nil {
			t.Fatalf("arrow key %v produced unexpected command under contract 0.2", k)
		}
	}

	// Enter produces no decision command
	_, cmd := f.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd != nil {
		t.Fatal("Enter must not produce decision command under contract 0.2")
	}
}

// TestModalDefaultSelectionIsDeny verifies that a freshly opened modal starts on
// Deny, currentDecision reflects Deny, and selected points at the Deny choice.
func TestModalDefaultSelectionIsDeny(t *testing.T) {
	f, _ := feedWithRunner(t)
	openModal(f)

	if f.permModal.selected != denyIndex() {
		t.Fatalf("initial selected = %d, want denyIndex=%d", f.permModal.selected, denyIndex())
	}
	if f.permModal.currentDecision() != agent.Deny {
		t.Fatalf("currentDecision = %v, want Deny", f.permModal.currentDecision())
	}
	choices := f.permModal.choices()
	if choices[f.permModal.selected].Decision != agent.Deny {
		t.Fatalf("selected choice has Decision=%v, want Deny", choices[f.permModal.selected].Decision)
	}
}

// TestModalEnterWithoutNavigationProducesNoDecision verifies that pressing Enter
// produces no decision without invoking the runner or modifying the composer.
func TestModalEnterWithoutNavigationProducesNoDecision(t *testing.T) {
	f, r := feedWithRunner(t)
	typeIntoFeed(t, f, "pending message")
	openModal(f)
	f.permModal.armedAt = time.Time{}

	_, cmd := f.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd != nil {
		t.Fatal("Enter on modal must not produce reply command under contract 0.2")
	}

	// Runner must never be invoked.
	if r.textsLen() != 0 {
		t.Fatalf("runner invoked %d times, want 0", r.textsLen())
	}

	// Composer text must be preserved.
	if got := f.composer.value(); got != "pending message" {
		t.Fatalf("composer changed: %q", got)
	}
}

// TestModalVisibleSelectionMatchesDecision verifies, across all rendering
// levels, that Deny is clearly indicated and no [*] selection markers exist.
func TestModalVisibleSelectionMatchesDecision(t *testing.T) {
	// maxRows values chosen to force each degradation level:
	// full (>=8), compact (5-6), toolrow (4), minimum (3).
	for _, maxRows := range []int{100, 5, 4, 3} {
		t.Run(fmt.Sprintf("maxRows=%d", maxRows), func(t *testing.T) {
			f, _ := feedWithRunner(t)
			f.width = 80
			f.height = 24
			openModal(f)

			// Strip ANSI to inspect plain text.
			plain := ansi.Strip(f.permModal.view(80, maxRows))

			// The visible choice must contain Deny.
			if !strings.Contains(plain, "Deny (n / esc)") {
				t.Fatalf("maxRows=%d: expected Deny (n / esc) in view, got:\n%s", maxRows, plain)
			}
			if strings.Contains(plain, "[*]") {
				t.Fatalf("maxRows=%d: [*] appears in view under contract 0.2:\n%s", maxRows, plain)
			}

			// currentDecision must agree with the default Deny.
			if f.permModal.currentDecision() != agent.Deny {
				t.Fatalf("maxRows=%d: currentDecision=%v, want Deny", maxRows, f.permModal.currentDecision())
			}
		})
	}
}

// TestModalInvalidSelectionFallsBackToDeny verifies that injected invalid
// selected values resolve to Deny both in decision and display.
func TestModalInvalidSelectionFallsBackToDeny(t *testing.T) {
	for _, sel := range []int{-1, -5, 99} {
		t.Run(fmt.Sprintf("selected=%d", sel), func(t *testing.T) {
			f, _ := feedWithRunner(t)
			openModal(f)
			f.permModal.selected = sel

			if f.permModal.currentDecision() != agent.Deny {
				t.Fatalf("selected=%d: currentDecision=%v, want Deny", sel, f.permModal.currentDecision())
			}

			plain := ansi.Strip(f.permModal.view(80, 3))
			if !strings.Contains(plain, "Deny (n / esc)") {
				t.Fatalf("selected=%d: expected Deny (n / esc) in narrow view, got:\n%s", sel, plain)
			}
			if strings.Contains(plain, "[*]") {
				t.Fatalf("selected=%d: [*] on choice:\n%s", sel, plain)
			}
		})
	}
}

// TestModalReopenResetsToDeny verifies that closing and reopening the modal
// resets the selection to Deny regardless of the previous choice.
func TestModalReopenResetsToDeny(t *testing.T) {
	f, _ := feedWithRunner(t)
	openModal(f)
	f.permModal.armedAt = time.Time{} // armed

	// Move to Allow Once and answer.
	f.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("y")})
	if f.modalVisible {
		t.Fatal("modal must close after y")
	}

	// Reopen.
	openModal(f)
	if f.permModal.selected != denyIndex() {
		t.Fatalf("after reopen, selected=%d, want denyIndex=%d", f.permModal.selected, denyIndex())
	}
	if f.permModal.currentDecision() != agent.Deny {
		t.Fatalf("after reopen, currentDecision=%v, want Deny", f.permModal.currentDecision())
	}
}

// TestChoiceIndexReturnsMinusOneForMissingDecision verifies that choiceIndex
// returns -1 when the requested decision is absent from the choices list. This
// protects fail-closed behavior: if Deny were ever removed from choices,
// denyIndex() returns -1 and currentDecision() still yields Deny.
func TestChoiceIndexReturnsMinusOneForMissingDecision(t *testing.T) {
	got := choiceIndex([]PermissionChoice{
		{Decision: agent.AllowOnce},
		{Decision: agent.AllowSession},
	}, agent.Deny)
	if got != -1 {
		t.Fatalf("choiceIndex(Deny absent) = %d, want -1", got)
	}

	// Sanity: present decisions are found correctly.
	if idx := choiceIndex((&PermissionModal{}).choices(), agent.Deny); idx < 0 {
		t.Fatalf("choiceIndex(Deny present) = %d, want >= 0", idx)
	}
	if denyIndex() < 0 {
		t.Fatalf("denyIndex() = %d, want >= 0", denyIndex())
	}
}

// TestModalIdempotentSingleDecision confirms that repeated key presses (e.g. y twice,
// or y then Enter) generate exactly ONE decision command and no duplicate replies.
func TestModalIdempotentSingleDecision(t *testing.T) {
	f, _ := feedWithRunner(t)
	openModal(f)
	f.permModal.armedAt = time.Time{} // armed

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
		{Seq: 3, Type: agent.PermReply, Call: &agent.ToolCall{ID: "c1"}, Decision: agent.AllowOnce, RawDecision: agent.AllowOnce},
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

			// 2. Render view during active modal
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

			// Invariant C: Choice must visually show Allow Once (y)
			if !strings.Contains(v, "Allow Once (y)") {
				t.Fatalf("choice must visually show Allow Once (y):\n%s", v)
			}

			// Invariant D: Composer must be visibly paused
			if !strings.Contains(v, "composer paused") {
				t.Fatalf("composer must be replaced by paused notice during modal:\n%s", v)
			}
			if strings.Contains(v, "type a message") {
				t.Fatalf("composer placeholder must NOT appear while modal is active:\n%s", v)
			}

			// Invariant E: Answer modal -> verify composer is restored
			f.permModal.armedAt = time.Time{} // armed
			f.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("y")})
			f.Update(agentEventBatchMsg{Events: []agent.Event{
				{Seq: 2, Type: agent.PermReply, Call: &agent.ToolCall{ID: "c_snap"}, Decision: agent.AllowOnce, RawDecision: agent.AllowOnce},
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

// TestModalDroppedArgsDisclosed proves the three cases:
// 1. Presence of DroppedArgs produces the "dropped: ..." line at width 120.
// 2. At narrow width 24, the line is present and truncated (not deleted), retaining "dropped: ".
// 3. Absence of DroppedArgs produces no dropped line and no blank line.
func TestModalDroppedArgsDisclosed(t *testing.T) {
	t.Run("present at width 120", func(t *testing.T) {
		m := newPermissionModal()
		m.open(&agent.ToolCall{
			ID:          "c1",
			Name:        "bash",
			Args:        json.RawMessage(`{"cmd":"ls"}`),
			DroppedArgs: []string{"bogus", "other"},
		})
		view := m.view(120)
		if !strings.Contains(view, "dropped: bogus, other") {
			t.Fatalf("modal at width 120 missing dropped args line, got:\n%s", view)
		}
	})

	t.Run("present and truncated at width 24", func(t *testing.T) {
		m := newPermissionModal()
		m.open(&agent.ToolCall{
			ID:          "c2",
			Name:        "bash",
			Args:        json.RawMessage(`{"cmd":"ls"}`),
			DroppedArgs: []string{"bogus_long_argument_key", "another_extra_key"},
		})
		view := m.view(24)
		if !strings.Contains(view, "dropped: ") {
			t.Fatalf("modal at width 24 missing 'dropped: ' prefix, got:\n%s", view)
		}
		if !strings.Contains(view, "…") {
			t.Fatalf("modal at width 24 should truncate long keys with '…', got:\n%s", view)
		}
		found := false
		for _, l := range strings.Split(view, "\n") {
			if strings.Contains(l, "dropped:") {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("dropped row was deleted at width 24:\n%s", view)
		}
	})

	t.Run("absent produces no dropped row", func(t *testing.T) {
		m := newPermissionModal()
		m.open(&agent.ToolCall{
			ID:   "c3",
			Name: "bash",
			Args: json.RawMessage(`{"cmd":"ls"}`),
		})
		for _, w := range []int{24, 120} {
			view := m.view(w)
			if strings.Contains(view, "dropped") {
				t.Fatalf("modal at width %d must not produce dropped row when DroppedArgs is empty, got:\n%s", w, view)
			}
		}
	})
}
