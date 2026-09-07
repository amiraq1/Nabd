package ui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// TestComposerHistoryRecallEditResizeEdit validates the sequence:
// recall historical entry -> edit it -> resize terminal via WindowSizeMsg -> edit further -> send.
func TestComposerHistoryRecallEditResizeEdit(t *testing.T) {
	f := NewFeed()
	_, _ = f.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
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

	// 3. Resize terminal via tea.WindowSizeMsg
	_, _ = f.Update(tea.WindowSizeMsg{Width: 40, Height: 24})
	if got := f.composer.value(); got != expected1 {
		t.Fatalf("after resize to 40: value = %q, want %q", got, expected1)
	}
	li := f.composer.ta.LineInfo()
	if li.Height < 2 {
		t.Fatalf("expected text to wrap into at least 2 rows, got %d", li.Height)
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
	_, _ = f.Update(tea.WindowSizeMsg{Width: 80, Height: 24})

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
	_, _ = f.Update(tea.WindowSizeMsg{Width: 80, Height: 24})

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
	_, _ = f.Update(tea.WindowSizeMsg{Width: 80, Height: 24})

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
// via WindowSizeMsg does not cause stale rendering or corrupted layout.
func TestComposerResizeMaintainsConsistentView(t *testing.T) {
	f := NewFeed()
	_, _ = f.Update(tea.WindowSizeMsg{Width: 80, Height: 24})

	text := "A continuous unbroken text string designed to be tested across width resizes."
	typeIntoFeed(t, f, text)

	widths := []int{80, 50, 30, 20, 40, 90}
	for _, w := range widths {
		_, _ = f.Update(tea.WindowSizeMsg{Width: w, Height: 24})
		if got := f.composer.value(); got != text {
			t.Fatalf("at width %d value corrupted: got %q, want %q", w, got, text)
		}
		if f.composer.width != w {
			t.Fatalf("at width %d: composer.width = %d, want %d", w, f.composer.width, w)
		}
		if f.composer.ta.Width() != w-2 {
			t.Fatalf("at width %d: ta.Width = %d, want %d", w, f.composer.ta.Width(), w-2)
		}
		view := f.composer.view()
		if !strings.HasPrefix(view, "›") {
			t.Fatalf("at width %d view missing prompt: %q", w, view)
		}
	}
}

// TestComposerWindowSizeMsgResizeAndEdit tests that terminal window resize events
// (tea.WindowSizeMsg) sent through Feed.Update properly reconfigure the composer layout,
// and subsequent editing operations (typing, backspacing, cursor navigation) maintain
// valid state, accurate cursor position, and consistent rendered view without stale artifacts.
func TestComposerWindowSizeMsgResizeAndEdit(t *testing.T) {
	f := NewFeed()
	_, _ = f.Update(tea.WindowSizeMsg{Width: 80, Height: 24})

	text := "Testing window resize events across multiple terminal dimensions"
	typeIntoFeed(t, f, text)

	sizes := []struct {
		width  int
		height int
	}{
		{width: 60, height: 20},
		{width: 40, height: 16},
		{width: 30, height: 12},
		{width: 25, height: 10},
		{width: 80, height: 24},
		{width: 100, height: 30},
	}

	for _, sz := range sizes {
		// 1. Dispatch full terminal resize event
		_, _ = f.Update(tea.WindowSizeMsg{Width: sz.width, Height: sz.height})

		if f.width != sz.width {
			t.Fatalf("after resize to %dx%d: feed width = %d, want %d", sz.width, sz.height, f.width, sz.width)
		}
		if f.composer.width != sz.width {
			t.Fatalf("after resize to %dx%d: composer width = %d, want %d", sz.width, sz.height, f.composer.width, sz.width)
		}
		if f.composer.ta.Width() != sz.width-2 {
			t.Fatalf("after resize to %dx%d: ta.Width = %d, want %d", sz.width, sz.height, f.composer.ta.Width(), sz.width-2)
		}
		if got := f.composer.value(); got != text {
			t.Fatalf("after resize to %dx%d: value = %q, want %q", sz.width, sz.height, got, text)
		}

		// 2. Perform edit at this window size: append a suffix
		typeIntoFeed(t, f, " PLUS")
		expected := text + " PLUS"
		if got := f.composer.value(); got != expected {
			t.Fatalf("after edit at width %d: value = %q, want %q", sz.width, got, expected)
		}

		// Check view is rendered with prompt
		view := f.composer.view()
		if !strings.HasPrefix(view, "›") {
			t.Fatalf("view at width %d missing prompt '›':\n%s", sz.width, view)
		}

		// Check cursor is within valid bounds and row/column offsets are non-negative
		li := f.composer.ta.LineInfo()
		if li.ColumnOffset < 0 || li.RowOffset < 0 {
			t.Fatalf("invalid cursor offset at width %d: %+v", sz.width, li)
		}
		if li.Height < 1 {
			t.Fatalf("invalid LineInfo.Height at width %d: %d", sz.width, li.Height)
		}

		// 3. Revert edit (backspace 5 runes)
		for i := 0; i < 5; i++ {
			_, _ = f.Update(tea.KeyMsg{Type: tea.KeyBackspace})
		}
		if got := f.composer.value(); got != text {
			t.Fatalf("after backspacing edit at width %d: value = %q, want %q", sz.width, got, text)
		}

		// Verify cursor and view after revert
		liAfter := f.composer.ta.LineInfo()
		if liAfter.ColumnOffset < 0 || liAfter.RowOffset < 0 {
			t.Fatalf("invalid cursor offset after revert at width %d: %+v", sz.width, liAfter)
		}
		viewAfter := f.composer.view()
		if !strings.HasPrefix(viewAfter, "›") {
			t.Fatalf("view after revert at width %d missing prompt '›':\n%s", sz.width, viewAfter)
		}
	}
}

// TestComposerMultilineWindowResizeAndEdit validates multiline content during terminal resizes.
func TestComposerMultilineWindowResizeAndEdit(t *testing.T) {
	f := NewFeed()
	_, _ = f.Update(tea.WindowSizeMsg{Width: 80, Height: 24})

	typeIntoFeed(t, f, "First line of content")
	_, _ = f.Update(tea.KeyMsg{Type: tea.KeyCtrlJ})
	typeIntoFeed(t, f, "Second line of content")
	_, _ = f.Update(tea.KeyMsg{Type: tea.KeyCtrlJ})
	typeIntoFeed(t, f, "Third line")

	if f.composer.logicalLineCount() != 3 {
		t.Fatalf("expected 3 logical lines, got %d", f.composer.logicalLineCount())
	}

	for _, w := range []int{80, 60, 50, 70} {
		_, _ = f.Update(tea.WindowSizeMsg{Width: w, Height: 24})
		view := f.composer.view()
		if !strings.Contains(view, "First") || !strings.Contains(view, "Second") || !strings.Contains(view, "Third") {
			t.Fatalf("at width %d multiline view corrupted:\n%s", w, view)
		}

		// Edit on third line
		typeIntoFeed(t, f, " edited")
		if !strings.Contains(f.composer.value(), "Third line edited") {
			t.Fatalf("at width %d failed to edit multiline content: %q", w, f.composer.value())
		}
		// Backspace the addition
		for i := 0; i < 7; i++ {
			_, _ = f.Update(tea.KeyMsg{Type: tea.KeyBackspace})
		}
		if !strings.Contains(f.composer.value(), "Third line") {
			t.Fatalf("after backspace value corrupted: %q", f.composer.value())
		}
	}
}
