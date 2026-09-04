package ui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// TestSlashMenuOpenOnSlash confirms that typing '/' into an empty composer opens the menu.
func TestSlashMenuOpenOnSlash(t *testing.T) {
	f, _ := feedWithRunner(t)
	typeIntoFeed(t, f, "/")

	if !f.menu.visible {
		t.Fatal("slash menu must be visible after typing '/'")
	}
	if len(f.menu.items) != 6 {
		t.Fatalf("expected 6 commands, got %d", len(f.menu.items))
	}

	view := f.View()
	if !strings.Contains(view, "Commands") {
		t.Fatalf("view missing 'Commands' header:\n%s", view)
	}
}

// TestSlashMenuPrefixFiltering confirms that typing '/re' filters to '/rewind'.
func TestSlashMenuPrefixFiltering(t *testing.T) {
	f, _ := feedWithRunner(t)
	typeIntoFeed(t, f, "/re")

	if !f.menu.visible {
		t.Fatal("slash menu must be visible after typing '/re'")
	}
	if len(f.menu.items) != 1 || f.menu.items[0].Name != "/rewind" {
		t.Fatalf("expected only /rewind, got %+v", f.menu.items)
	}
}

// TestSlashMenuNotOpenedInNormalText confirms leading whitespace or internal slash doesn't open menu.
func TestSlashMenuNotOpenedInNormalText(t *testing.T) {
	f, _ := feedWithRunner(t)
	typeIntoFeed(t, f, "hello /world")
	if f.menu.visible {
		t.Fatal("menu must not open when slash is in middle of text")
	}

	f2, _ := feedWithRunner(t)
	typeIntoFeed(t, f2, "  /undo")
	if f2.menu.visible {
		t.Fatal("menu must not open when slash has leading whitespace")
	}
}

// TestSlashMenuNotOpenedOnMultiline confirms multiline text doesn't open menu.
func TestSlashMenuNotOpenedOnMultiline(t *testing.T) {
	f, _ := feedWithRunner(t)
	typeIntoFeed(t, f, "/undo")
	if !f.menu.visible {
		t.Fatal("menu should be open for /undo")
	}

	// Insert newline
	f.Update(tea.KeyMsg{Type: tea.KeyCtrlJ})
	if f.menu.visible {
		t.Fatal("menu must close on multiline input")
	}
}

// TestSlashMenuUpDownNoHistoryPollution confirms Up/Down navigate menu selection and history is untouched.
func TestSlashMenuUpDownNoHistoryPollution(t *testing.T) {
	f, _ := feedWithRunner(t)
	// Seed history
	cmd := sendAndRun(f, "previous user message")
	execCmd(cmd)
	f.Update(doneMsg{err: nil})

	if f.HistoryLen() != 1 {
		t.Fatalf("history len = %d, want 1", f.HistoryLen())
	}

	// Open menu
	typeIntoFeed(t, f, "/")
	if !f.menu.visible {
		t.Fatal("menu must be visible")
	}
	if f.menu.selected != 0 {
		t.Fatalf("initial menu selected = %d, want 0", f.menu.selected)
	}

	// Down arrow moves menu selection
	f.Update(tea.KeyMsg{Type: tea.KeyDown})
	if f.menu.selected != 1 {
		t.Fatalf("after Down, menu selected = %d, want 1", f.menu.selected)
	}
	if f.HistoryBrowsing() {
		t.Fatal("Down inside menu must not start history browsing")
	}

	// Up arrow moves back
	f.Update(tea.KeyMsg{Type: tea.KeyUp})
	if f.menu.selected != 0 {
		t.Fatalf("after Up, menu selected = %d, want 0", f.menu.selected)
	}
	if f.HistoryBrowsing() {
		t.Fatal("Up inside menu must not start history browsing")
	}
}

// TestSlashMenuTabCompletionDoesNotExecute confirms Tab completes text and does NOT execute.
func TestSlashMenuTabCompletionDoesNotExecute(t *testing.T) {
	f, r := feedWithRunner(t)
	typeIntoFeed(t, f, "/re")

	_, cmd := f.Update(tea.KeyMsg{Type: tea.KeyTab})
	if cmd != nil {
		t.Fatal("Tab completion must NOT produce a command")
	}
	if r.textsLen() != 0 {
		t.Fatal("Tab completion must NEVER execute or invoke runner")
	}
	if f.menu.visible {
		t.Fatal("menu must close after completion")
	}
	if got := f.composer.value(); got != "/rewind " {
		t.Fatalf("composer after Tab = %q, want '/rewind '", got)
	}
}

// TestSlashMenuFirstEnterCompletesSecondEnterExecutes confirms:
// 1. First Enter completes selected command without execution.
// 2. Second Enter dispatches the completed command.
func TestSlashMenuFirstEnterCompletesSecondEnterExecutes(t *testing.T) {
	f, r := feedWithRunner(t)
	var undoCalled bool
	f.SetCallbacks(&FeedCallbacks{
		OnUndo: func(n int) string {
			undoCalled = true
			return "undone"
		},
	})

	typeIntoFeed(t, f, "/un")
	if !f.menu.visible {
		t.Fatal("menu must be visible for /un")
	}

	// First Enter: complete ONLY
	_, cmd1 := f.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd1 != nil {
		t.Fatal("first Enter must NOT produce an execution command")
	}
	if undoCalled {
		t.Fatal("first Enter must NOT invoke OnUndo")
	}
	if r.textsLen() != 0 {
		t.Fatal("first Enter must NOT invoke runner")
	}
	if f.menu.visible {
		t.Fatal("menu must close after first Enter")
	}
	if got := f.composer.value(); got != "/undo " {
		t.Fatalf("composer after first Enter = %q, want '/undo '", got)
	}

	// Second Enter: executes command via existing dispatch path
	_, cmd2 := f.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd2 != nil {
		t.Fatal("slash command execution produces local status, no async cmd")
	}
	if !undoCalled {
		t.Fatal("second Enter must execute the command (OnUndo)")
	}
	if f.status != "undone" {
		t.Fatalf("status = %q, want 'undone'", f.status)
	}
}

// TestSlashMenuEscClosesAndPreservesText confirms Esc closes menu and preserves text.
func TestSlashMenuEscClosesAndPreservesText(t *testing.T) {
	f, _ := feedWithRunner(t)
	typeIntoFeed(t, f, "/ed")
	if !f.menu.visible {
		t.Fatal("menu must be visible for /ed")
	}

	f.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if f.menu.visible {
		t.Fatal("menu must close after Esc")
	}
	if got := f.composer.value(); got != "/ed" {
		t.Fatalf("composer text after Esc = %q, want '/ed'", got)
	}
}

// TestSlashMenuPasteDoesNotExecute confirms pasting a command does NOT execute it.
func TestSlashMenuPasteDoesNotExecute(t *testing.T) {
	f, r := feedWithRunner(t)
	pasteInto(f, "/undo 3")

	if r.textsLen() != 0 {
		t.Fatal("paste must never invoke runner")
	}
	if got := f.composer.value(); got != "/undo 3" {
		t.Fatalf("composer after paste = %q, want '/undo 3'", got)
	}
}

// TestSlashMenuBusyPolicyBlocked confirms commands are blocked while a run is in flight.
func TestSlashMenuBusyPolicyBlocked(t *testing.T) {
	f, r := feedWithBlockingRunner(t)
	startBlockingRun(t, f, r, "active work")

	// Typing '/' while busy: menu should not open (modal or busy guard)
	typeIntoFeed(t, f, "/undo")
	_, _ = f.Update(tea.KeyMsg{Type: tea.KeyEnter})

	if f.status != "wait for the current run to finish first" {
		t.Fatalf("expected busy error status, got %q", f.status)
	}

	close(r.release)
	r.waitReturned(t)
}
