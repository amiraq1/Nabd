package ui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// TestOversizedHistoryRecallEditableDown: a historical entry larger than
// the cap may be recalled and edited DOWN (backspace works), but cannot be
// sent until it is reduced under the cap.
func TestOversizedHistoryRecallEditableDown(t *testing.T) {
	f := NewFeed()
	f.width = 80
	f.height = 24
	// Directly seed an oversized entry into history (as if from an old
	// journal written before the limit policy).
	big := strings.Repeat("م", maxInputRunes+100)
	f.history.add(big)

	// Recall via Up.
	_, _ = f.Update(tea.KeyMsg{Type: tea.KeyUp})
	if got := f.composer.valueLen(); got != maxInputRunes+100 {
		t.Fatalf("recalled len = %d, want %d (historical entries are not truncated)", got, maxInputRunes+100)
	}
	// Sending is blocked while over the cap.
	f.SetRunner(runnerFunc(func(string) error { return nil }))
	_, cmd := f.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd != nil {
		t.Fatal("sending an oversized historical message must be rejected")
	}
	if f.HistoryLen() != 1 {
		t.Fatal("rejected send must not add a history entry")
	}
	if v := f.composer.valueLen(); v != maxInputRunes+100 {
		t.Fatal("rejected send must keep the text")
	}
	// Editing DOWN is allowed: delete 101 runes to get under the cap.
	for i := 0; i < 101; i++ {
		_, _ = f.Update(tea.KeyMsg{Type: tea.KeyBackspace})
	}
	if v := f.composer.valueLen(); v != maxInputRunes-1 {
		t.Fatalf("after deleting 101 runes len = %d, want %d", v, maxInputRunes-1)
	}
	// Now it can be sent.
	_, cmd2 := f.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd2 == nil {
		t.Fatal("a reduced message must be sendable")
	}
}
