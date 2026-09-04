package ui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// TestNewlineAtLineCapRejected: Ctrl+J beyond maxInputLines is rejected
// with the notice and the text stays intact.
func TestNewlineAtLineCapRejected(t *testing.T) {
	f := NewFeed()
	f.width = 80
	f.height = 24
	// Build a buffer at the line cap (200 lines).
	pasteInto(f, strings.Repeat("x\n", maxInputLines-1)+"y")
	if f.status != "" {
		t.Fatalf("unexpected notice while filling: %q", f.status)
	}
	if countInputLines(f.composer.value()) != maxInputLines {
		t.Fatalf("setup: want %d lines", maxInputLines)
	}
	before := f.composer.value()
	_, _ = f.Update(tea.KeyMsg{Type: tea.KeyCtrlJ})
	if countInputLines(f.composer.value()) != maxInputLines {
		t.Fatalf("Ctrl+J beyond the line cap must be rejected; got %d lines", countInputLines(f.composer.value()))
	}
	if f.composer.value() != before {
		t.Fatal("text changed by rejected Ctrl+J")
	}
	if f.status == "" {
		t.Fatal("Ctrl+J beyond the cap must raise a notice")
	}
}

// TestTypingBeyondRuneCapRejected: individual runes that would cross the
// rune cap are rejected one at a time (the notice appears once and the
// text never exceeds the cap).
func TestTypingBeyondRuneCapRejected(t *testing.T) {
	f := NewFeed()
	// Fill to the cap.
	pasteInto(f, arabicRunes(maxInputRunes))
	if f.status != "" {
		t.Fatalf("unexpected notice at the cap: %q", f.status)
	}
	// One more rune must be rejected.
	typeIntoFeed(t, f, "م")
	if got := f.composer.valueLen(); got != maxInputRunes {
		t.Fatalf("composer len = %d after over-cap rune, want %d", got, maxInputRunes)
	}
	if f.status == "" {
		t.Fatal("over-cap rune must raise a notice")
	}
	// A valid edit (backspace) clears the notice.
	_, _ = f.Update(tea.KeyMsg{Type: tea.KeyBackspace})
	if f.status == limitNotice {
		t.Fatal("stale limit notice must clear on a valid edit")
	}
}

// TestPasteRollbackRestoresCursorText: a rejected paste leaves the text and
// cursor position usable (no panic, valid UTF-8).
func TestPasteRollbackRestoresCursorText(t *testing.T) {
	f := NewFeed()
	typeIntoFeed(t, f, "أساس سليم")
	before := f.composer.value()
	pasteInto(f, strings.Repeat("كبير\n", 300))
	if f.composer.value() != before {
		t.Fatal("rejected paste changed the buffer")
	}
	if !validUTF8(f.composer.value()) {
		t.Fatal("buffer is invalid UTF-8 after rollback")
	}
	// The composer still accepts edits after the rollback.
	typeIntoFeed(t, f, "!")
	if got := f.composer.value(); got != before+"!" {
		t.Fatalf("composer not editable after rollback: %q", got)
	}
}
