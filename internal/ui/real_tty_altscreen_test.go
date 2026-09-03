package ui

import (
	"strings"
	"testing"
	"time"
)

// TestRealTTYFeedEntersAlternateScreen asserts that the full-screen Feed UI
// enters the terminal alternate screen buffer during cold startup.
func TestRealTTYFeedEntersAlternateScreen(t *testing.T) {
	sess := StartPTYSession(t, 70, 29)

	// Wait for cold startup layout ready
	if err := sess.WaitForCondition("cold-start placeholder", 3*time.Second, func(snap ScreenSnapshot) bool {
		return snap.Contains("type a message")
	}); err != nil {
		t.Fatalf("cold-start placeholder never appeared: %v", err)
	}

	if sess.AltScreenEnters() < 1 {
		t.Fatalf("alternate-screen enter missing: got %d enters, want at least 1", sess.AltScreenEnters())
	}
	if !sess.IsAltScreenActive() {
		t.Fatalf("alternate screen is not active on VT emulator after startup")
	}
}

// TestRealTTYFeedLeavesAlternateScreenOnCtrlC asserts that exiting the feed
// via Ctrl+C restores the terminal from alternate screen to primary screen.
func TestRealTTYFeedLeavesAlternateScreenOnCtrlC(t *testing.T) {
	sess := StartPTYSession(t, 70, 29)

	if err := sess.WaitForCondition("startup ready", 3*time.Second, func(snap ScreenSnapshot) bool {
		return snap.Contains("type a message")
	}); err != nil {
		t.Fatalf("startup ready condition timed out: %v", err)
	}

	if sess.AltScreenEnters() < 1 {
		t.Fatalf("alternate-screen enter missing: got %d enters, want at least 1", sess.AltScreenEnters())
	}

	// Send Ctrl+C
	sess.SendKey([]byte{3})

	select {
	case <-sess.Done():
	case <-time.After(3 * time.Second):
		t.Fatalf("program did not shut down after Ctrl+C within 3s")
	}

	// Brief pause for PTY background reader to flush trailing terminal resets
	time.Sleep(50 * time.Millisecond)

	if sess.AltScreenExits() < 1 {
		t.Fatalf("alternate-screen exit missing on Ctrl+C: got %d exits, want at least 1", sess.AltScreenExits())
	}
	if sess.IsAltScreenActive() {
		t.Fatalf("alternate screen remains active after Ctrl+C shutdown")
	}
}

// TestRealTTYFeedLeavesAlternateScreenOnNormalQuit asserts that a clean program
// exit leaves the alternate screen and restores the primary screen.
func TestRealTTYFeedLeavesAlternateScreenOnNormalQuit(t *testing.T) {
	sess := StartPTYSession(t, 70, 29)

	if err := sess.WaitForCondition("startup ready", 3*time.Second, func(snap ScreenSnapshot) bool {
		return snap.Contains("type a message")
	}); err != nil {
		t.Fatalf("startup ready condition timed out: %v", err)
	}

	if sess.AltScreenEnters() < 1 {
		t.Fatalf("alternate-screen enter missing: got %d enters, want at least 1", sess.AltScreenEnters())
	}

	sess.Program.Quit()

	select {
	case <-sess.Done():
	case <-time.After(3 * time.Second):
		t.Fatalf("program did not shut down after Quit within 3s")
	}

	time.Sleep(50 * time.Millisecond)

	if sess.AltScreenExits() < 1 {
		t.Fatalf("alternate-screen exit missing on clean Quit: got %d exits, want at least 1", sess.AltScreenExits())
	}
	if sess.IsAltScreenActive() {
		t.Fatalf("alternate screen remains active after clean Quit")
	}
}

// TestRealTTYAlternateScreenLifecycleBalanced asserts that every enter sequence
// is strictly balanced by an exit sequence upon program termination.
func TestRealTTYAlternateScreenLifecycleBalanced(t *testing.T) {
	sess := StartPTYSession(t, 70, 29)

	if err := sess.WaitForCondition("startup ready", 3*time.Second, func(snap ScreenSnapshot) bool {
		return snap.Contains("type a message")
	}); err != nil {
		t.Fatalf("startup ready condition timed out: %v", err)
	}

	sess.Program.Quit()

	select {
	case <-sess.Done():
	case <-time.After(3 * time.Second):
		t.Fatalf("program did not shut down after Quit within 3s")
	}

	time.Sleep(50 * time.Millisecond)

	enters := sess.AltScreenEnters()
	exits := sess.AltScreenExits()

	if enters < 1 {
		t.Fatalf("no alternate-screen enter occurred: enters=%d", enters)
	}
	if enters != exits {
		t.Fatalf("alternate-screen lifecycle unbalanced: enters=%d, exits=%d", enters, exits)
	}
}

// TestRealTTYPrimaryScreenRestoredAfterExit asserts that pre-existing primary screen
// content is restored and not clobbered by TUI frames after program exit.
func TestRealTTYPrimaryScreenRestoredAfterExit(t *testing.T) {
	const marker = "PRIMARY-BEFORE-LAUNCH"
	sess := StartPTYSessionWithPrimaryMarker(t, 70, 29, marker)

	if err := sess.WaitForCondition("startup ready", 3*time.Second, func(snap ScreenSnapshot) bool {
		return snap.Contains("type a message")
	}); err != nil {
		t.Fatalf("startup ready condition timed out: %v", err)
	}

	// While running in alt screen, marker from primary screen should not be visible
	snapRunning := sess.Snapshot()
	if snapRunning.Contains(marker) {
		t.Fatalf("primary screen marker leaked into alternate screen:\n%s", snapRunning.PlainText())
	}

	sess.Program.Quit()

	select {
	case <-sess.Done():
	case <-time.After(3 * time.Second):
		t.Fatalf("program did not shut down after Quit within 3s")
	}

	time.Sleep(50 * time.Millisecond)

	// After exit, VT emulator must be back on primary screen with marker intact
	if sess.IsAltScreenActive() {
		t.Fatalf("still in alternate screen after exit")
	}

	snapAfter := sess.Snapshot()
	if !snapAfter.Contains(marker) {
		t.Fatalf("primary screen marker was clobbered by inline TUI frames:\n%s", snapAfter.PlainText())
	}
	if snapAfter.Contains("›") || snapAfter.Contains("Ctrl+J newline") {
		t.Fatalf("feed TUI chrome was left behind in primary screen after exit:\n%s", snapAfter.PlainText())
	}
}

// TestRealTTYNoFullFrameOutputWhilePrimaryScreenActive asserts that no full-height
// TUI frames are written to the terminal while the primary screen is active.
func TestRealTTYNoFullFrameOutputWhilePrimaryScreenActive(t *testing.T) {
	sess := StartPTYSession(t, 70, 29)

	if err := sess.WaitForCondition("startup ready", 3*time.Second, func(snap ScreenSnapshot) bool {
		return snap.Contains("type a message")
	}); err != nil {
		t.Fatalf("startup ready condition timed out: %v", err)
	}

	if sess.AltScreenEnters() < 1 {
		t.Fatalf("alternate-screen enter missing: got %d enters, want at least 1", sess.AltScreenEnters())
	}

	if sess.PrimaryHasFullFrame() {
		t.Fatalf("full TUI frame leaked into primary screen buffer")
	}
}

// TestRealTTYAltScreenTypingAndBottomAnchor verifies real Termux dimensions (70x29)
// running inside the alternate screen: typing replaces placeholder, footer is bottom-anchored,
// and unused rows remain inside the viewport above composer.
func TestRealTTYAltScreenTypingAndBottomAnchor(t *testing.T) {
	const (
		cols = 70
		rows = 29
	)

	sess := StartPTYSession(t, cols, rows)

	if err := sess.WaitForCondition("startup ready", 3*time.Second, func(snap ScreenSnapshot) bool {
		return snap.Contains("type a message")
	}); err != nil {
		t.Fatalf("startup ready condition timed out: %v", err)
	}

	if !sess.IsAltScreenActive() {
		t.Fatalf("alternate screen must be active for Termux full-screen UI")
	}

	sess.WriteString("abc")

	err := sess.WaitForCondition("typed abc visible", 3*time.Second, func(snap ScreenSnapshot) bool {
		return snap.Contains("abc") && !snap.Contains("type a message")
	})
	snap := sess.Snapshot()
	if err != nil {
		t.Fatalf("typed text did not appear: %v\nScreen:\n%s", err, snap.PlainText())
	}

	assertScreenBounds(t, snap)

	compRow, ok := snap.ComposerRow()
	if !ok {
		t.Fatalf("composer row not found in screen")
	}
	plainRows := snap.PlainRows()
	if !strings.Contains(plainRows[compRow], "abc") {
		t.Fatalf("typed text not in composer row %d: %q", compRow, plainRows[compRow])
	}

	// Footer bottom anchored: row rows-1
	footerRow, ok := snap.FooterRow()
	if !ok {
		t.Fatalf("footer not found in screen:\n%s", snap.PlainText())
	}
	if footerRow != rows-1 {
		t.Fatalf("footer not on bottom row: footerRow=%d, want %d\nScreen:\n%s", footerRow, rows-1, snap.PlainText())
	}
}

// TestRealTTYAltScreenResizeKeepsBottomChrome verifies terminal resizing
// (70x29 -> 70x16 -> 70x29 -> 40x20) in alternate screen retains bottom chrome and focus.
func TestRealTTYAltScreenResizeKeepsBottomChrome(t *testing.T) {
	sess := StartPTYSession(t, 70, 29)

	if err := sess.WaitForCondition("startup ready", 3*time.Second, func(snap ScreenSnapshot) bool {
		return snap.Contains("type a message")
	}); err != nil {
		t.Fatalf("startup ready condition timed out: %v", err)
	}

	if !sess.IsAltScreenActive() {
		t.Fatalf("alternate screen must be active")
	}

	sess.WriteString("draft preserved across resize")
	if err := sess.WaitForText("draft preserved across resize", 2*time.Second); err != nil {
		t.Fatalf("draft text did not appear: %v", err)
	}

	resizes := []struct{ w, h int }{
		{70, 16}, // keyboard open
		{70, 29}, // keyboard closed
		{40, 20}, // rotated / split
	}

	for _, sz := range resizes {
		sess.Resize(sz.w, sz.h)
		err := sess.WaitForCondition("resized", 3*time.Second, func(s ScreenSnapshot) bool {
			return s.Width == sz.w && s.Height == sz.h
		})
		if err != nil {
			t.Fatalf("resize to %dx%d timed out: %v", sz.w, sz.h, err)
		}

		if !sess.IsAltScreenActive() {
			t.Fatalf("alternate screen lost during resize to %dx%d", sz.w, sz.h)
		}

		snap := sess.Snapshot()
		assertScreenBounds(t, snap)

		if !snap.Contains("draft preserved across resize") {
			t.Fatalf("draft lost after resize to %dx%d:\n%s", sz.w, sz.h, snap.PlainText())
		}
	}
}
