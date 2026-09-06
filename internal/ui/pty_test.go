package ui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"nabd/internal/agent"
)

// 1. TestPTYLongOutputKeepsComposerVisible: verifies that streaming a large amount
// of feed output preserves the composer at the bottom of the screen without feed overflow.
func TestPTYLongOutputKeepsComposerVisible(t *testing.T) {
	sess := StartPTYSession(t, 80, 24)

	var sb strings.Builder
	for i := 1; i <= 60; i++ {
		sb.WriteString(fmt.Sprintf("Feed line %02d: testing long output scrolling\n", i))
	}

	sess.InjectBatch([]agent.Event{
		{Seq: 1, Type: agent.RunStart},
		{Seq: 2, Type: agent.ToolStart, Call: &agent.ToolCall{ID: "t1", Name: "bash", Args: []byte(`"echo long"`)}},
		{Seq: 3, Type: agent.ToolEnd, Call: &agent.ToolCall{ID: "t1", Name: "bash", Output: sb.String(), OK: true}},
	})

	// Follow mode anchors to the newest lines (bottom), so the latest output lines
	// are visible on screen, while line 10 is scrolled above the viewport.
	err := sess.WaitForText("Feed line 60", 3*time.Second)
	if err != nil {
		t.Fatalf("long output failed to appear: %v", err)
	}

	snap := sess.Snapshot()
	assertScreenBounds(t, snap)
	assertComposerNearBottom(t, snap, 3)
}

// 2. TestPTYTypingAfterFeedFillAppearsInComposer: verifies that after feed fills the screen,
// typing keystrokes via PTY appears in the persistent composer panel.
func TestPTYTypingAfterFeedFillAppearsInComposer(t *testing.T) {
	sess := StartPTYSession(t, 80, 24)

	var sb strings.Builder
	for i := 1; i <= 40; i++ {
		sb.WriteString(fmt.Sprintf("Feed line %02d: filling the viewport\n", i))
	}
	sess.InjectBatch([]agent.Event{
		{Seq: 1, Type: agent.RunStart},
		{Seq: 2, Type: agent.ToolStart, Call: &agent.ToolCall{ID: "t1", Name: "bash"}},
		{Seq: 3, Type: agent.ToolEnd, Call: &agent.ToolCall{ID: "t1", Name: "bash", Output: sb.String(), OK: true}},
	})

	// Follow mode anchors to the newest lines (bottom), so line 40 is visible.
	err := sess.WaitForText("Feed line 40", 3*time.Second)
	if err != nil {
		t.Fatalf("feed filling text did not appear: %v", err)
	}

	sess.WriteString("typing after feed fill")
	err = sess.WaitForText("typing after feed fill", 2*time.Second)
	if err != nil {
		t.Fatalf("typed text did not appear in screen: %v", err)
	}

	snap := sess.Snapshot()
	assertScreenBounds(t, snap)
	assertComposerNearBottom(t, snap, 3)

	compRow, ok := snap.ComposerRow()
	if !ok {
		t.Fatal("composer row not found in screen")
	}
	plainRows := snap.PlainRows()
	if !strings.Contains(plainRows[compRow], "typing after feed fill") {
		t.Fatalf("typed text not located in composer row %d: %q", compRow, plainRows[compRow])
	}
}

// 3. TestPTYVisualRowsStayWithinTerminal: verifies strict geometric bounds for all screen rows
// including wide UTF-8 and ANSI sequences.
func TestPTYVisualRowsStayWithinTerminal(t *testing.T) {
	sess := StartPTYSession(t, 80, 24)

	wideLine := strings.Repeat("عربى English 123 ", 8) // ~160 visual width
	sess.InjectBatch([]agent.Event{
		{Seq: 1, Type: agent.RunStart},
		{Seq: 2, Type: agent.TextDelta, Text: wideLine},
	})

	err := sess.WaitForCondition("wide text rendered", 3*time.Second, func(s ScreenSnapshot) bool {
		return s.Contains("عربى")
	})
	if err != nil {
		t.Fatalf("wide text failed to render: %v", err)
	}

	snap := sess.Snapshot()
	assertScreenBounds(t, snap)
	if _, ok := snap.ComposerRow(); !ok {
		t.Fatal("composer row missing from screen")
	}
}

// 4. TestPTYPermissionModalAppears: verifies that receiving a PermAsk event renders
// the permission modal card with required title, tool name, and choices.
func TestPTYPermissionModalAppears(t *testing.T) {
	sess := StartPTYSession(t, 80, 24)

	sess.InjectBatch([]agent.Event{
		{
			Seq:  1,
			Type: agent.PermAsk,
			Call: &agent.ToolCall{ID: "c1", Name: "write_file", Args: []byte(`{"path":"secret.txt"}`)},
		},
	})

	snap, err := waitForPermissionModal(sess, 3*time.Second)
	if err != nil {
		t.Fatalf("permission modal did not appear: %v", err)
	}

	assertScreenBounds(t, snap)
	if !snap.Contains("Permission Required") {
		t.Fatalf("modal missing title 'Permission Required':\n%s", snap.PlainText())
	}
	if !snap.Contains("write_file") {
		t.Fatalf("modal missing tool name 'write_file':\n%s", snap.PlainText())
	}
	if !snap.Contains("Allow Once") || !snap.Contains("Deny") {
		t.Fatalf("modal missing choices:\n%s", snap.PlainText())
	}
}

// 5. TestPTYPermissionAllowOnceRestoresComposer: verifies that selecting 'Allow Once' (via 'y')
// notifies approver and restores composer interactivity and position.
func TestPTYPermissionAllowOnceRestoresComposer(t *testing.T) {
	sess := StartPTYSession(t, 80, 24)

	sess.InjectBatch([]agent.Event{
		{
			Seq:  1,
			Type: agent.PermAsk,
			Call: &agent.ToolCall{ID: "c1", Name: "write_file", Args: []byte(`"test.txt"`)},
		},
	})

	_, err := waitForPermissionModal(sess, 3*time.Second)
	if err != nil {
		t.Fatalf("modal did not appear: %v", err)
	}

	sess.SendKey([]byte("y"))

	err = sess.Approver.WaitCount(1, 2*time.Second)
	if err != nil {
		t.Fatalf("approver did not receive decision: %v", err)
	}
	d, _ := sess.Approver.LastDecision()
	if d != agent.AllowOnce {
		t.Fatalf("approver received %v, want AllowOnce", d)
	}

	// Deliver PermReply to simulate core processing
	sess.InjectBatch([]agent.Event{
		{
			Seq:         2,
			Type:        agent.PermReply,
			Call:        &agent.ToolCall{ID: "c1", Name: "write_file"},
			Decision:    agent.AllowOnce,
			RawDecision: agent.AllowOnce,
		},
	})

	err = sess.WaitForCondition("modal closed", 2*time.Second, func(s ScreenSnapshot) bool {
		return !s.ModalVisible()
	})
	if err != nil {
		t.Fatalf("modal failed to close: %v", err)
	}

	snap := sess.Snapshot()
	assertScreenBounds(t, snap)

	// Verify typing works in restored composer
	sess.WriteString("typing after allow once")
	err = sess.WaitForText("typing after allow once", 2*time.Second)
	if err != nil {
		t.Fatalf("typing in composer failed after modal: %v", err)
	}
}

// 6. TestPTYPermissionDenyDoesNotExecuteTool: verifies that denying permission (via 'n' or 'esc')
// records agent.Deny and does not execute the tool runner.
func TestPTYPermissionDenyDoesNotExecuteTool(t *testing.T) {
	sess := StartPTYSession(t, 80, 24)

	sess.InjectBatch([]agent.Event{
		{
			Seq:  1,
			Type: agent.PermAsk,
			Call: &agent.ToolCall{ID: "c_deny", Name: "delete_database", Args: []byte(`"all"`)},
		},
	})

	_, err := waitForPermissionModal(sess, 3*time.Second)
	if err != nil {
		t.Fatalf("modal did not appear: %v", err)
	}

	sess.SendKey([]byte("n"))

	err = sess.Approver.WaitCount(1, 2*time.Second)
	if err != nil {
		t.Fatalf("approver did not receive decision: %v", err)
	}
	d, _ := sess.Approver.LastDecision()
	if d != agent.Deny {
		t.Fatalf("approver received %v, want Deny", d)
	}

	sess.InjectBatch([]agent.Event{
		{
			Seq:         2,
			Type:        agent.PermReply,
			Call:        &agent.ToolCall{ID: "c_deny", Name: "delete_database"},
			Decision:    agent.Deny,
			RawDecision: agent.Deny,
		},
		{
			Seq:  3,
			Type: agent.ToolStart,
			Call: &agent.ToolCall{ID: "c_deny", Name: "delete_database"},
		},
		{
			Seq:  4,
			Type: agent.ToolEnd,
			Call: &agent.ToolCall{ID: "c_deny", Name: "delete_database", OK: false, Output: "refused to run delete_database"},
		},
	})

	err = sess.WaitForCondition("denial processed", 2*time.Second, func(s ScreenSnapshot) bool {
		return !s.ModalVisible() && (s.Contains("refused") || s.Contains("delete_database"))
	})
	if err != nil {
		t.Fatalf("denial not reflected: %v", err)
	}

	if sess.Runner.Count() != 0 {
		t.Fatalf("runner executed count = %d, want 0", sess.Runner.Count())
	}
	assertScreenBounds(t, sess.Snapshot())
}

// 7. TestPTYBashAllowSessionIsCoreDowngraded: verifies that when user presses 'a' for bash,
// UI forwards raw AllowSession to approver, and presentation renders the core downgrade indicator.
func TestPTYBashAllowSessionIsCoreDowngraded(t *testing.T) {
	sess := StartPTYSession(t, 80, 24)

	sess.InjectBatch([]agent.Event{
		{
			Seq:  1,
			Type: agent.PermAsk,
			Call: &agent.ToolCall{ID: "c_bash", Name: "bash", Args: []byte(`"echo dangerous"`)},
		},
	})

	_, err := waitForPermissionModal(sess, 3*time.Second)
	if err != nil {
		t.Fatalf("modal did not appear: %v", err)
	}

	sess.SendKey([]byte("a"))

	err = sess.Approver.WaitCount(1, 2*time.Second)
	if err != nil {
		t.Fatalf("approver did not receive decision: %v", err)
	}
	d, _ := sess.Approver.LastDecision()
	if d != agent.AllowSession {
		t.Fatalf("approver received %v, want AllowSession (UI must not downgrade)", d)
	}

	// Core policy calculation: AllowSession for bash -> AllowOnce
	sess.InjectBatch([]agent.Event{
		{
			Seq:         2,
			Type:        agent.PermReply,
			Call:        &agent.ToolCall{ID: "c_bash", Name: "bash"},
			Decision:    agent.AllowSession,
			RawDecision: agent.AllowOnce,
		},
	})

	err = sess.WaitForText("requested session, applied once", 3*time.Second)
	if err != nil {
		t.Fatalf("downgrade indicator not rendered on terminal grid: %v", err)
	}

	snap := sess.Snapshot()
	assertScreenBounds(t, snap)
}

// 8. TestPTYRepeatedDecisionIsIdempotent: verifies that rapid repeated decision inputs
// emit exactly one decision to the approver.
func TestPTYRepeatedDecisionIsIdempotent(t *testing.T) {
	sess := StartPTYSession(t, 80, 24)

	sess.InjectBatch([]agent.Event{
		{
			Seq:  1,
			Type: agent.PermAsk,
			Call: &agent.ToolCall{ID: "c_dup", Name: "write_file"},
		},
	})

	_, err := waitForPermissionModal(sess, 3*time.Second)
	if err != nil {
		t.Fatalf("modal did not appear: %v", err)
	}

	sess.SendKey([]byte("y"))
	time.Sleep(30 * time.Millisecond)
	sess.SendKey([]byte("y"))
	time.Sleep(30 * time.Millisecond)
	sess.SendKey([]byte("y"))

	// Wait for the decision to be recorded
	err = sess.Approver.WaitCount(1, 2*time.Second)
	if err != nil {
		t.Fatalf("approver did not receive decision: %v", err)
	}

	// Brief pause to ensure no duplicate decision is queued
	time.Sleep(150 * time.Millisecond)

	if cnt := sess.Approver.Count(); cnt != 1 {
		t.Fatalf("approver received %d decisions, want exactly 1", cnt)
	}

	sess.InjectBatch([]agent.Event{
		{
			Seq:         2,
			Type:        agent.PermReply,
			Call:        &agent.ToolCall{ID: "c_dup", Name: "write_file"},
			Decision:    agent.AllowOnce,
			RawDecision: agent.AllowOnce,
		},
	})

	err = sess.WaitForCondition("modal closed cleanly", 2*time.Second, func(s ScreenSnapshot) bool {
		return !s.ModalVisible()
	})
	if err != nil {
		t.Fatalf("modal did not close cleanly: %v", err)
	}
	assertScreenBounds(t, sess.Snapshot())
}

// 9. TestPTYModalBlocksComposerInput: verifies that keystrokes typed while the modal
// is displayed are swallowed and do not enter the composer textarea.
func TestPTYModalBlocksComposerInput(t *testing.T) {
	sess := StartPTYSession(t, 80, 24)

	sess.InjectBatch([]agent.Event{
		{
			Seq:  1,
			Type: agent.PermAsk,
			Call: &agent.ToolCall{ID: "c_block", Name: "format_disk"},
		},
	})

	_, err := waitForPermissionModal(sess, 3*time.Second)
	if err != nil {
		t.Fatalf("modal did not appear: %v", err)
	}

	sess.WriteString("leak_text_should_be_blocked")
	time.Sleep(150 * time.Millisecond)

	snap := sess.Snapshot()
	if snap.Contains("leak_text_should_be_blocked") {
		t.Fatalf("composer accepted input while modal was visible:\n%s", snap.PlainText())
	}

	sess.SendKey([]byte("n"))
	err = sess.Approver.WaitCount(1, 2*time.Second)
	if err != nil {
		t.Fatalf("approver did not receive decision: %v", err)
	}
	d, _ := sess.Approver.LastDecision()
	if d != agent.Deny {
		t.Fatalf("expected Deny, got %v", d)
	}
	assertScreenBounds(t, sess.Snapshot())
}

// 10. TestPTYModalRestoresDraftAndCursor: verifies that typing a draft before a modal
// opens preserves the draft and restores editing functionality after modal closes.
func TestPTYModalRestoresDraftAndCursor(t *testing.T) {
	sess := StartPTYSession(t, 80, 24)

	sess.WriteString("existing draft message")
	err := sess.WaitForText("existing draft message", 2*time.Second)
	if err != nil {
		t.Fatalf("initial draft failed to appear: %v", err)
	}

	sess.InjectBatch([]agent.Event{
		{
			Seq:  1,
			Type: agent.PermAsk,
			Call: &agent.ToolCall{ID: "c_draft", Name: "read_file"},
		},
	})

	_, err = waitForPermissionModal(sess, 3*time.Second)
	if err != nil {
		t.Fatalf("modal did not appear: %v", err)
	}

	sess.SendKey([]byte("y"))
	err = sess.Approver.WaitCount(1, 2*time.Second)
	if err != nil {
		t.Fatalf("approver did not receive decision: %v", err)
	}

	sess.InjectBatch([]agent.Event{
		{
			Seq:         2,
			Type:        agent.PermReply,
			Call:        &agent.ToolCall{ID: "c_draft", Name: "read_file"},
			Decision:    agent.AllowOnce,
			RawDecision: agent.AllowOnce,
		},
	})

	err = sess.WaitForCondition("modal closed and composer unpaused", 2*time.Second, func(s ScreenSnapshot) bool {
		return !s.ModalVisible() && !s.Contains("composer paused")
	})
	if err != nil {
		t.Fatalf("modal did not close or composer remained paused: %v", err)
	}

	snap := sess.Snapshot()
	if !snap.Contains("existing draft message") {
		t.Fatalf("draft text lost after modal close:\n%s", snap.PlainText())
	}

	sess.WriteString(" continued")
	err = sess.WaitForText("existing draft message continued", 2*time.Second)
	if err != nil {
		t.Fatalf("typing continuation failed: %v", err)
	}

	snap = sess.Snapshot()
	assertScreenBounds(t, snap)
}

// 11. TestPTYResizeFilledFeedKeepsBottomChrome: verifies that resizing a filled terminal
// from 80x24 to 40x20 keeps the bottom separators, composer, and footer intact.
func TestPTYResizeFilledFeedKeepsBottomChrome(t *testing.T) {
	sess := StartPTYSession(t, 80, 24)

	var sb strings.Builder
	for i := 1; i <= 50; i++ {
		sb.WriteString(fmt.Sprintf("Feed line %02d: resize testing\n", i))
	}
	sess.InjectBatch([]agent.Event{
		{Seq: 1, Type: agent.RunStart},
		{Seq: 2, Type: agent.ToolStart, Call: &agent.ToolCall{ID: "t1", Name: "bash"}},
		{Seq: 3, Type: agent.ToolEnd, Call: &agent.ToolCall{ID: "t1", Name: "bash", Output: sb.String(), OK: true}},
	})

	// Follow mode anchors to the newest lines (bottom), so line 50 is visible.
	err := sess.WaitForText("Feed line 50", 3*time.Second)
	if err != nil {
		t.Fatalf("feed output did not appear: %v", err)
	}

	snap := sess.Snapshot()
	assertScreenBounds(t, snap)
	assertComposerNearBottom(t, snap, 3)

	sess.Resize(40, 20)
	err = sess.WaitForCondition("resized to 40x20", 3*time.Second, func(s ScreenSnapshot) bool {
		return s.Width == 40 && s.Height == 20
	})
	if err != nil {
		t.Fatalf("resize condition failed: %v", err)
	}

	snap = sess.Snapshot()
	assertScreenBounds(t, snap)
	assertComposerNearBottom(t, snap, 3)

	_, hasFooter := snap.FooterRow()
	if !hasFooter {
		t.Fatalf("footer row missing after resize:\n%s", snap.PlainText())
	}
}

// 12. TestPTYTwentyByTwelveDoesNotOverflow: verifies that the minimum terminal dimension (20x12)
// renders without panic, out-of-bounds rows, or layout overflow.
func TestPTYTwentyByTwelveDoesNotOverflow(t *testing.T) {
	sess := StartPTYSession(t, 20, 12)

	sess.InjectBatch([]agent.Event{
		{Seq: 1, Type: agent.RunStart},
		{Seq: 2, Type: agent.ToolStart, Call: &agent.ToolCall{ID: "c1", Name: "ls"}},
		{Seq: 3, Type: agent.ToolEnd, Call: &agent.ToolCall{ID: "c1", Name: "ls", Output: "compact output", OK: true}},
	})

	err := sess.WaitForCondition("compact event rendered", 3*time.Second, func(s ScreenSnapshot) bool {
		return s.Contains("compact") || s.Contains("ls")
	})
	if err != nil {
		t.Fatalf("compact output failed to render: %v", err)
	}

	snap := sess.Snapshot()
	assertScreenBounds(t, snap)

	sess.WriteString("hi")
	err = sess.WaitForCondition("typing accepted", 2*time.Second, func(s ScreenSnapshot) bool {
		return s.Contains("hi")
	})
	if err != nil {
		t.Fatalf("typing failed in 20x12: %v", err)
	}

	snap = sess.Snapshot()
	assertScreenBounds(t, snap)
}

// 13. TestPTYToolOutputControlPayloadDoesNotCorruptTerminal: injects raw escape codes
// (screen wipe, cursor moves, OSC title changes) via tool output and verifies the real PTY
// grid and composer remain intact without corruption.
func TestPTYToolOutputControlPayloadDoesNotCorruptTerminal(t *testing.T) {
	sess := StartPTYSession(t, 80, 24)

	maliciousPayload := "Line 1: normal output\n" +
		"\x1b[2J\x1b[H\x1b[5;10H" + // Screen clear + cursor move
		"\x1b]0;Hacked Title\x07" + // Terminal title injection
		"\x1b]52;c;Y2F0Cg==\x07" + // OSC 52 clipboard hijacking
		"Line 2: safe text after attacks\n"

	sess.InjectBatch([]agent.Event{
		{Seq: 1, Type: agent.RunStart},
		{Seq: 2, Type: agent.ToolStart, Call: &agent.ToolCall{ID: "t1", Name: "bash"}},
		{Seq: 3, Type: agent.ToolEnd, Call: &agent.ToolCall{ID: "t1", Name: "bash", Output: maliciousPayload, OK: true}},
	})

	err := sess.WaitForCondition("safe text rendered", 3*time.Second, func(s ScreenSnapshot) bool {
		return s.Contains("safe text after attacks")
	})
	if err != nil {
		t.Fatalf("sanitized tool output failed to appear: %v", err)
	}

	snap := sess.Snapshot()
	assertScreenBounds(t, snap)
	assertComposerNearBottom(t, snap, 3)

	// Verify terminal screen does NOT contain escape sequences or injected title
	if snap.Contains("Hacked Title") {
		t.Fatalf("PTY screen leaked injected terminal title: %s", snap.PlainText())
	}
}
