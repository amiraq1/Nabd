package ui

import (
	"strings"
	"testing"
	"time"
)

// TestRealTTYStartupTypingAndBottomAnchor reproduces the Phase 3B.2.1 real
// human TTY regression on a real PTY + VT10x grid:
//
// Given: a cold startup of the real ag -feed program path in an 80x24 grid.
// When:  the terminal delivers the printable bytes "abc" before Enter.
// Then:  "abc" replaces the placeholder in the Composer on the CURRENT grid,
//
//	the Footer occupies the bottom chrome row, no used rows exist below
//	the Footer, and all unused vertical space belongs to the viewport
//	ABOVE the composer.
//
// The test goes through the same program wiring as the production -feed path
// (inline renderer, no AltScreen, PTY-backed input/output), types via raw PTY
// bytes only, never calls Focus() manually, never uses SetValue(), never
// touches the network, and waits by polling the current grid with bounded
// deadlines (no blind sleeps).
func TestRealTTYStartupTypingAndBottomAnchor(t *testing.T) {
	const (
		gridW = 80
		gridH = 24
	)

	sess := StartPTYSession(t, gridW, gridH)

	// Cold startup: the placeholder must be visible in the current grid.
	if err := sess.WaitForCondition("cold-start placeholder", 5*time.Second, func(snap ScreenSnapshot) bool {
		return snap.Contains("type a message")
	}); err != nil {
		t.Fatalf("cold-start placeholder never appeared: %v", err)
	}

	// The human types "abc": raw PTY bytes, no Enter pressed.
	sess.WriteString("abc")

	// Wait for a NEW frame: typed runes visible AND placeholder replaced.
	err := sess.WaitForCondition("typed text abc visible", 5*time.Second, func(snap ScreenSnapshot) bool {
		return snap.Contains("abc") && !snap.Contains("type a message")
	})
	snap := sess.Snapshot()
	if err != nil {
		t.Fatalf("typing did not reach the current grid: %v\nScreen:\n%s", err, snap.PlainText())
	}

	rows := snap.PlainRows()

	// "abc" must be visible inside the composer row of the current grid.
	compRow, ok := snap.ComposerRow()
	if !ok {
		t.Fatalf("composer row not found after typing:\n%s", snap.PlainText())
	}
	if !strings.Contains(rows[compRow], "abc") {
		t.Fatalf("abc not visible in composer row %d:\n%s", compRow, snap.PlainText())
	}
	if snap.Contains("type a message") {
		t.Fatalf("placeholder still visible after typing:\n%s", snap.PlainText())
	}

	// Footer anchored: find the footer from the BOTTOM using markers that
	// never occur in the composer placeholder ("Enter" in the placeholder
	// would otherwise false-match; see H10).
	footerRow := -1
	for i := len(rows) - 1; i >= 0; i-- {
		if strings.Contains(rows[i], "Ctrl+J newline") || strings.Contains(rows[i], "Ctrl+D exit") {
			footerRow = i
			break
		}
	}
	if footerRow == -1 {
		t.Fatalf("footer not found in grid:\n%s", snap.PlainText())
	}
	if footerRow != snap.Height-1 {
		t.Fatalf("footer not bottom-anchored: footerRow=%d, terminalHeight=%d\nScreen:\n%s",
			footerRow, snap.Height, snap.PlainText())
	}

	// No used rows below the footer.
	for r := footerRow + 1; r < len(rows); r++ {
		if rows[r] != "" {
			t.Fatalf("used row below footer at %d: %q\nScreen:\n%s", r, rows[r], snap.PlainText())
		}
	}

	// All otherwise unused vertical space belongs to the viewport ABOVE the
	// composer: every row above the top separator must be blank, and on an
	// 80x24 grid at least one such unused row must exist.
	topSepRow := -1
	for i, r := range rows {
		if strings.Contains(r, "─") && strings.Trim(r, "─") == "" {
			topSepRow = i
			break
		}
	}
	if topSepRow == -1 {
		t.Fatalf("top separator not found:\n%s", snap.PlainText())
	}
	unused := 0
	for i := 0; i < topSepRow; i++ {
		if rows[i] == "" {
			unused++
			continue
		}
		t.Fatalf("non-viewport content at row %d above composer: %q\nScreen:\n%s", i, rows[i], snap.PlainText())
	}
	if unused < 1 {
		t.Fatalf("no unused viewport rows above composer (rows above topSep=%d):\n%s", topSepRow, snap.PlainText())
	}
}
