package ui

import (
	"bytes"
	"errors"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

const (
	altEnter = "\x1b[?1049h"
	altLeave = "\x1b[?1049l"
)

// TestRealTTYRawStreamAltScreenEnterBeforeFirstFrameAndLeaveAtExit inspects
// the raw byte history written to the PTY — not the VT emulator's final
// screen — and asserts the alternate-screen lifecycle byte-for-byte:
//
//  1. the enter sequence appears before the first full-height frame, so no
//     frame can ever reach the primary screen's scrollback;
//  2. the leave sequence appears after the program exits;
//  3. no frame chrome is written after the leave sequence (a trailing frame
//     would land in the primary scrollback on a real terminal).
//
// The order check is the point: a final screen snapshot cannot tell whether
// a frame was flushed before switching buffers.
func TestRealTTYRawStreamAltScreenEnterBeforeFirstFrameAndLeaveAtExit(t *testing.T) {
	sess := StartPTYSession(t, 70, 29)

	if err := sess.WaitForCondition("startup ready", 3*time.Second, func(snap ScreenSnapshot) bool {
		return snap.Contains("type a message")
	}); err != nil {
		t.Fatalf("startup ready condition timed out: %v", err)
	}

	raw := sess.RawBytes()
	enterAt := bytes.Index(raw, []byte(altEnter))
	if enterAt < 0 {
		t.Fatalf("raw stream has no alternate-screen enter %q", altEnter)
	}
	// Frame chrome: the composer placeholder is only ever rendered by a
	// full frame. Its first occurrence must be after the enter sequence.
	frameAt := bytes.Index(raw, []byte("type a message"))
	if frameAt < 0 {
		t.Fatalf("raw stream has no frame although the screen shows one")
	}
	if frameAt < enterAt {
		t.Fatalf("frame written at byte %d before alternate-screen enter at %d: the frame leaked to primary scrollback", frameAt, enterAt)
	}
	if bytes.Contains(raw, []byte(altLeave)) {
		t.Fatalf("alternate-screen leave emitted while the program is still running")
	}

	sess.Program.Quit()
	select {
	case <-sess.Done():
	case <-time.After(3 * time.Second):
		t.Fatalf("program did not shut down after Quit within 3s")
	}
	time.Sleep(50 * time.Millisecond)

	raw = sess.RawBytes()
	leaveAt := bytes.LastIndex(raw, []byte(altLeave))
	if leaveAt < 0 {
		t.Fatalf("raw stream has no alternate-screen leave %q after exit", altLeave)
	}
	if leaveAt < enterAt {
		t.Fatalf("leave at %d precedes enter at %d", leaveAt, enterAt)
	}
	tail := raw[leaveAt+len(altLeave):]
	for _, chrome := range []string{"type a message", "──", "Ctrl+J"} {
		if bytes.Contains(tail, []byte(chrome)) {
			t.Fatalf("frame chrome %q written after alternate-screen leave; it would land in the primary scrollback:\n%q", chrome, tail)
		}
	}
}

// TestRealTTYRawStreamInlineRendererLeaksFrames is the control experiment:
// the same program without tea.WithAltScreen() writes frames straight into
// the primary buffer. If this test ever fails, the harness stopped observing
// the raw stream and the positive test above proves nothing.
func TestRealTTYRawStreamInlineRendererLeaksFrames(t *testing.T) {
	old := defaultFeedProgramOptions
	// A non-empty slice bypasses feed.ProgramOptions(); WithoutSignals is
	// a harmless option that keeps the inline renderer.
	defaultFeedProgramOptions = []tea.ProgramOption{tea.WithoutSignals()}
	t.Cleanup(func() { defaultFeedProgramOptions = old })

	sess := StartPTYSession(t, 70, 29)
	if err := sess.WaitForCondition("startup ready", 3*time.Second, func(snap ScreenSnapshot) bool {
		return snap.Contains("type a message")
	}); err != nil {
		t.Fatalf("startup ready condition timed out: %v", err)
	}
	raw := sess.RawBytes()
	if bytes.Contains(raw, []byte(altEnter)) {
		t.Fatalf("control run unexpectedly entered the alternate screen")
	}
	if !bytes.Contains(sess.PrimaryBytes(), []byte("type a message")) {
		t.Fatalf("control run did not write a frame to the primary buffer; harness is not observing raw output")
	}
}

// TestRealTTYRunErrorStaysInFeedWhileTyping drives the failure through a real
// PTY: the runner fails, the error must be a feed line, and typing the next
// draft must not remove it from the screen.
func TestRealTTYRunErrorStaysInFeedWhileTyping(t *testing.T) {
	sess := StartPTYSession(t, 70, 29)
	sess.Runner.FailWith(errors.New("provider exploded"))

	if err := sess.WaitForCondition("startup ready", 3*time.Second, func(snap ScreenSnapshot) bool {
		return snap.Contains("type a message")
	}); err != nil {
		t.Fatalf("startup ready condition timed out: %v", err)
	}

	sess.WriteString("hello")
	if err := sess.WaitForText("hello", 2*time.Second); err != nil {
		t.Fatalf("typed text did not appear: %v", err)
	}
	sess.SendKey([]byte{'\r'})

	if err := sess.WaitForText("provider exploded", 3*time.Second); err != nil {
		t.Fatalf("error never reached the screen: %v\n%s", err, sess.Snapshot().PlainText())
	}

	// The error row must be a feed line: above the composer, not the
	// runtime status row that the next keystroke clears.
	sess.WriteString("next draft")
	if err := sess.WaitForText("next draft", 2*time.Second); err != nil {
		t.Fatalf("second draft did not appear: %v", err)
	}
	snap := sess.Snapshot()
	if !snap.Contains("provider exploded") {
		t.Fatalf("error vanished on first keystroke (still transient status?):\n%s", snap.PlainText())
	}
	errRow, compRow := -1, -1
	for i, row := range snap.PlainRows() {
		if errRow < 0 && bytes.Contains([]byte(row), []byte("provider exploded")) {
			errRow = i
		}
		if bytes.Contains([]byte(row), []byte("next draft")) {
			compRow = i
		}
	}
	if errRow < 0 || compRow < 0 || errRow >= compRow {
		t.Fatalf("error row %d must sit above composer row %d:\n%s", errRow, compRow, snap.PlainText())
	}
}
