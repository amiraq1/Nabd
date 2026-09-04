package ui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// TestCtrlDMultilineArabicKeepsUTF8: deleting forward across a multiline
// Arabic/emoji buffer never produces lone bytes.
func TestCtrlDMultilineArabicKeepsUTF8(t *testing.T) {
	f := NewFeed()
	typeIntoFeed(t, f, "مرحبا")
	_, _ = f.Update(tea.KeyMsg{Type: tea.KeyCtrlJ})
	typeIntoFeed(t, f, "😀👍")
	// Cursor at the very end (after paste typing). Ctrl+D is a no-op.
	_, _ = f.Update(tea.KeyMsg{Type: tea.KeyCtrlD})
	if v := f.composer.value(); v != "مرحبا\n😀👍" {
		t.Fatalf("Ctrl+D at end changed text: %q", v)
	}
	// Move to the very start of the input (Ctrl+Home = InputBegin in the
	// textarea keymap) and delete forward across the newline.
	_, _ = f.Update(tea.KeyMsg{Type: tea.KeyCtrlHome})
	_, _ = f.Update(tea.KeyMsg{Type: tea.KeyCtrlD})
	v := f.composer.value()
	if !validUTF8(v) {
		t.Fatalf("composer invalid UTF-8 after delete: %q", v)
	}
	// Deleting the first rune removed 'م' only.
	if v != "رحبا\n😀👍" {
		t.Fatalf("after delete at start = %q, want 'رحبا\\n😀👍'", v)
	}
}

// TestCtrlJThenUpDownCursorMove: Up/Down inside a multiline composer move
// the cursor between logical lines and never touch history while the cursor
// is not on the first/last line.
func TestCtrlJThenUpDownCursorMove(t *testing.T) {
	f := NewFeed()
	typeIntoFeed(t, f, "line 1")
	_, _ = f.Update(tea.KeyMsg{Type: tea.KeyCtrlJ})
	typeIntoFeed(t, f, "line 2")
	_, _ = f.Update(tea.KeyMsg{Type: tea.KeyCtrlJ})
	typeIntoFeed(t, f, "line 3")

	// Cursor is at the end of line 3. Up once → line 2 (cursor move).
	_, _ = f.Update(tea.KeyMsg{Type: tea.KeyUp})
	if f.HistoryBrowsing() {
		t.Fatal("Up on a non-first line must not open history")
	}
	// Up again → line 1: still a cursor move, not history.
	_, _ = f.Update(tea.KeyMsg{Type: tea.KeyUp})
	if f.HistoryBrowsing() {
		t.Fatal("Up on line 1 of a 3-line buffer with cursor movement must not open history")
	}
	// Down twice → back to line 3.
	_, _ = f.Update(tea.KeyMsg{Type: tea.KeyDown})
	_, _ = f.Update(tea.KeyMsg{Type: tea.KeyDown})
	if got := f.composer.value(); got != "line 1\nline 2\nline 3" {
		t.Fatalf("text changed by cursor moves: %q", got)
	}
}

// TestUpOnFirstLineOpensHistory: Up with the cursor on the first logical
// line of a multiline draft recalls history.
func TestUpOnFirstLineOpensHistory(t *testing.T) {
	f := NewFeed()
	f.SetRunner(runnerFunc(func(string) error { return nil }))
	sendAndRun(f, "stored message")
	_, _ = f.Update(doneMsg{err: nil})

	typeIntoFeed(t, f, "draft first line")
	_, _ = f.Update(tea.KeyMsg{Type: tea.KeyCtrlJ})
	typeIntoFeed(t, f, "draft second line")
	// Move to the first logical line (Up from line 2 → line 1), then Up
	// again on the first logical line opens history.
	_, _ = f.Update(tea.KeyMsg{Type: tea.KeyUp}) // line 2 → line 1
	_, _ = f.Update(tea.KeyMsg{Type: tea.KeyUp}) // line 1: opens history
	if !f.HistoryBrowsing() {
		t.Fatal("Up on the first logical line must open history")
	}
	if got := f.composer.value(); got != "stored message" {
		t.Fatalf("history recall = %q, want 'stored message'", got)
	}
}

// TestEnterClearsHistoryBrowsing: after a successful send, history
// browsing state is reset.
func TestEnterClearsHistoryBrowsing(t *testing.T) {
	f := NewFeed()
	f.SetRunner(runnerFunc(func(string) error { return nil }))
	sendAndRun(f, "one")
	_, _ = f.Update(doneMsg{err: nil})

	_, _ = f.Update(tea.KeyMsg{Type: tea.KeyUp}) // browsing
	if !f.HistoryBrowsing() {
		t.Fatal("expected browsing after Up")
	}
	// Type a new message and send.
	typeIntoFeed(t, f, "fresh")
	_, cmd := f.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("Enter must send")
	}
	if f.HistoryBrowsing() {
		t.Fatal("send must reset history browsing")
	}
}

// TestAltRuneEditingPassesToTextarea: Alt+letter keys (word movement,
// word deletion) reach the textarea editing rules untouched and never send.
func TestAltRuneEditingPassesToTextarea(t *testing.T) {
	f := NewFeed()
	typeIntoFeed(t, f, "one two three")
	// Alt+Left moves the cursor back a word (WordBackward binding). The
	// text must not change and no run may start.
	_, _ = f.Update(tea.KeyMsg{Type: tea.KeyLeft, Alt: true})
	if v := f.composer.value(); v != "one two three" {
		t.Fatalf("alt+left changed text: %q", v)
	}
	if f.busy {
		t.Fatal("alt+left must not start a run")
	}
	// Alt+Enter inserts a newline AT THE CURSOR (after "two "), never sends.
	_, cmd := f.Update(tea.KeyMsg{Type: tea.KeyEnter, Alt: true})
	if cmd != nil {
		t.Fatal("alt+enter must not produce a run command")
	}
	if v := f.composer.value(); v != "one two \nthree" {
		t.Fatalf("alt+enter should insert a newline at the cursor, got %q", v)
	}
	if f.busy {
		t.Fatal("alt+enter must not start a run")
	}
}
