package ui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// TestComposerHistoryRecallEditResizeEdit validates the sequence:
// recall historical entry -> edit it -> resize terminal -> edit further -> send.
func TestComposerHistoryRecallEditResizeEdit(t *testing.T) {
	f := NewFeed()
	f.width = 80
	f.height = 24
	var sentMessage string
	f.SetRunner(runnerFunc(func(text string) error { sentMessage = text; return nil }))

	// Add entry to history
	entry := "historical message with several words that wraps across columns"
	f.history.add(entry)

	// 1. Recall via Up
	_, _ = f.Update(tea.KeyMsg{Type: tea.KeyUp})
	if got := f.composer.value(); got != entry {
		t.Fatalf("after Up: value = %q, want %q", got, entry)
	}

	// 2. Edit (append 5 runes)
	typeIntoFeed(t, f, " PLUS")
	expected1 := entry + " PLUS"
	if got := f.composer.value(); got != expected1 {
		t.Fatalf("after typing PLUS: value = %q, want %q", got, expected1)
	}

	// 3. Resize terminal: set feed and composer width to 40
	f.width = 40
	f.composer.setWidth(40)
	f.composer.ta.SetHeight(4) // allow multi-row view for inspection
	view1 := f.composer.view()
	if !strings.Contains(view1, "PLUS") {
		t.Fatalf("after resize to 40 with height 4: view missing PLUS:\n%s", view1)
	}

	// 4. Edit further (backspace 5 times to remove ' PLUS')
	for i := 0; i < 5; i++ {
		_, _ = f.Update(tea.KeyMsg{Type: tea.KeyBackspace})
	}
	if got := f.composer.value(); got != entry {
		t.Fatalf("after deleting PLUS: value = %q, want %q", got, entry)
	}

	// 5. Send message
	_, cmd := f.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("Enter must produce a command")
	}
	_ = cmd()
	if sentMessage != entry {
		t.Fatalf("sent message = %q, want %q", sentMessage, entry)
	}
	if v := f.composer.value(); v != "" {
		t.Fatalf("composer after send = %q, want empty", v)
	}
}

// TestComposerDraftRestoration verifies that an in-progress draft is saved
// when browsing history and restored when returning down.
func TestComposerDraftRestoration(t *testing.T) {
	f := NewFeed()
	f.width = 80
	f.height = 24

	f.history.add("old message 1")
	f.history.add("old message 2")

	// Type an unfinished draft
	draft := "my in-progress draft"
	typeIntoFeed(t, f, draft)

	// Browse history: Up -> old message 2
	_, _ = f.Update(tea.KeyMsg{Type: tea.KeyUp})
	if got := f.composer.value(); got != "old message 2" {
		t.Fatalf("after Up: got %q, want 'old message 2'", got)
	}

	// Up again -> old message 1
	_, _ = f.Update(tea.KeyMsg{Type: tea.KeyUp})
	if got := f.composer.value(); got != "old message 1" {
		t.Fatalf("after second Up: got %q, want 'old message 1'", got)
	}

	// Browse back down: Down -> old message 2
	_, _ = f.Update(tea.KeyMsg{Type: tea.KeyDown})
	if got := f.composer.value(); got != "old message 2" {
		t.Fatalf("after Down: got %q, want 'old message 2'", got)
	}

	// Down again -> restored draft
	_, _ = f.Update(tea.KeyMsg{Type: tea.KeyDown})
	if got := f.composer.value(); got != draft {
		t.Fatalf("after second Down: got %q, want %q (draft restored)", got, draft)
	}
}

// TestComposerClearResetNewInput verifies clear and reset behavior followed by new typing.
func TestComposerClearResetNewInput(t *testing.T) {
	f := NewFeed()
	f.width = 80
	f.height = 24

	typeIntoFeed(t, f, "some initial text")
	if v := f.composer.value(); v != "some initial text" {
		t.Fatalf("initial value = %q", v)
	}

	// Clear composer
	f.composer.clear()
	if v := f.composer.value(); v != "" {
		t.Fatalf("after clear value = %q, want empty", v)
	}

	// Type new input
	typeIntoFeed(t, f, "fresh text after clear")
	if v := f.composer.value(); v != "fresh text after clear" {
		t.Fatalf("after retyping value = %q, want 'fresh text after clear'", v)
	}
}

// TestComposerNewlineInsertionAndRemoval verifies adding and removing newlines across lines.
func TestComposerNewlineInsertionAndRemoval(t *testing.T) {
	f := NewFeed()
	f.width = 80
	f.height = 24

	typeIntoFeed(t, f, "Line 1")
	// Insert newline via Ctrl+J
	_, _ = f.Update(tea.KeyMsg{Type: tea.KeyCtrlJ})
	typeIntoFeed(t, f, "Line 2")

	wantMulti := "Line 1\nLine 2"
	if got := f.composer.value(); got != wantMulti {
		t.Fatalf("composer value = %q, want %q", got, wantMulti)
	}

	// Backspace 6 times to remove 'Line 2'
	for i := 0; i < 6; i++ {
		_, _ = f.Update(tea.KeyMsg{Type: tea.KeyBackspace})
	}
	if got := f.composer.value(); got != "Line 1\n" {
		t.Fatalf("after deleting Line 2 got %q, want 'Line 1\\n'", got)
	}

	// Backspace once more to remove the newline and merge back to Line 1
	_, _ = f.Update(tea.KeyMsg{Type: tea.KeyBackspace})
	if got := f.composer.value(); got != "Line 1" {
		t.Fatalf("after deleting newline got %q, want 'Line 1'", got)
	}
}

// TestComposerResizeMaintainsConsistentView verifies that changing terminal width
// does not cause stale rendering or corrupted layout.
func TestComposerResizeMaintainsConsistentView(t *testing.T) {
	f := NewFeed()
	f.width = 80
	f.height = 24

	text := "A continuous unbroken text string designed to be tested across width resizes."
	typeIntoFeed(t, f, text)

	widths := []int{80, 50, 30, 20, 40, 90}
	for _, w := range widths {
		f.width = w
		f.composer.setWidth(w)
		f.composer.ta.SetHeight(6) // allow full wrapped viewing
		view := f.composer.view()
		if !strings.Contains(view, "continuous") || !strings.Contains(view, "resizes") {
			t.Fatalf("at width %d view does not contain expected words:\n%s", w, view)
		}
		if got := f.composer.value(); got != text {
			t.Fatalf("at width %d value corrupted: got %q, want %q", w, got, text)
		}
	}
}
