package ui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// TestSlashCommandNotSentToRunner: a known slash command is handled
// locally; the runner is never invoked and history gains no entry.
func TestSlashCommandNotSentToRunner(t *testing.T) {
	f, r := feedWithRunner(t)
	var undoCalls int
	f.SetCallbacks(&FeedCallbacks{
		OnUndo: func(n int) string {
			undoCalls++
			if n != 2 {
				t.Errorf("OnUndo n = %d, want 2", n)
			}
			return "undone"
		},
	})
	typeIntoFeed(t, f, "/undo 2")
	_, cmd := f.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd != nil {
		t.Fatal("slash command must not produce a run command")
	}
	if r.textsLen() != 0 {
		t.Fatal("runner must not be invoked for a slash command")
	}
	if undoCalls != 1 {
		t.Fatalf("OnUndo calls = %d, want 1", undoCalls)
	}
	if f.HistoryLen() != 0 {
		t.Fatal("slash command must not enter history")
	}
	if v := f.composer.value(); v != "" {
		t.Fatalf("composer after command = %q, want empty", v)
	}
	if f.status != "undone" {
		t.Fatalf("status = %q, want 'undone'", f.status)
	}
}

// TestUnknownSlashKeepsText: an unknown slash command keeps the text in the
// composer and shows an error; nothing is sent.
func TestUnknownSlashKeepsText(t *testing.T) {
	f, r := feedWithRunner(t)
	typeIntoFeed(t, f, "/nosuchcmd")
	_, cmd := f.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd != nil {
		t.Fatal("unknown command must not produce a run command")
	}
	if r.textsLen() != 0 {
		t.Fatal("runner must not be invoked for an unknown command")
	}
	if v := f.composer.value(); v != "/nosuchcmd" {
		t.Fatalf("composer after unknown command = %q, want text kept", v)
	}
}

// TestSlashUndoEmptyStatusNoDuplicate: the feed's /undo path calls the
// callback once, clears the composer, and leaves the status empty when the
// callback returns "" (the Notice arriving through the event stream is the
// single visible feedback, matching the classic Chat path).
func TestSlashUndoEmptyStatusNoDuplicate(t *testing.T) {
	f, r := feedWithRunner(t)
	var undoCalls int
	f.SetCallbacks(&FeedCallbacks{
		OnUndo: func(n int) string {
			undoCalls++
			if n != 3 {
				t.Errorf("OnUndo n = %d, want 3", n)
			}
			return "" // fileUndo emits a Notice instead of a status
		},
	})
	typeIntoFeed(t, f, "/undo 3")
	_, cmd := f.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd != nil {
		t.Fatal("slash command must not produce a run command")
	}
	if r.textsLen() != 0 {
		t.Fatal("runner must not be invoked for /undo")
	}
	if undoCalls != 1 {
		t.Fatalf("OnUndo calls = %d, want 1", undoCalls)
	}
	if v := f.composer.value(); v != "" {
		t.Fatalf("composer after /undo = %q, want empty", v)
	}
	if f.status != "" {
		t.Fatalf("status after /undo with '' callback = %q, want empty (no duplicate)", f.status)
	}
	if f.HistoryLen() != 0 {
		t.Fatal("slash command must not enter history")
	}
}
