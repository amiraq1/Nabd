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
	big := strings.Repeat("م", maxInputRunes+2)
	f.history.add(big)

	// Recall via Up.
	_, _ = f.Update(tea.KeyMsg{Type: tea.KeyUp})
	if got := f.composer.valueLen(); got != maxInputRunes+2 {
		t.Fatalf("recalled len = %d, want %d (historical entries are not truncated)", got, maxInputRunes+2)
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
	if v := f.composer.valueLen(); v != maxInputRunes+2 {
		t.Fatal("rejected send must keep the text")
	}
	// Editing DOWN is allowed: delete 3 runes to get under the cap.
	for i := 0; i < 3; i++ {
		_, _ = f.Update(tea.KeyMsg{Type: tea.KeyBackspace})
	}
	if v := f.composer.valueLen(); v != maxInputRunes-1 {
		t.Fatalf("after deleting 3 runes len = %d, want %d", v, maxInputRunes-1)
	}
	// Now it can be sent.
	_, cmd2 := f.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd2 == nil {
		t.Fatal("a reduced message must be sendable")
	}
}

// BenchmarkComposerBackspaceOversized records the original performance bottleneck
// found in U1, where 101 backspaces on a +100 oversized string without spaces
// causes O(N) wrap recalculations in bubbles/textarea. This remains as tech debt.
func BenchmarkComposerBackspaceOversized(b *testing.B) {
	for i := 0; i < b.N; i++ {
		f := NewFeed()
		f.width = 80
		f.height = 24
		big := strings.Repeat("م", maxInputRunes+100)
		f.history.add(big)
		_, _ = f.Update(tea.KeyMsg{Type: tea.KeyUp})

		b.StartTimer()
		for j := 0; j < 101; j++ {
			_, _ = f.Update(tea.KeyMsg{Type: tea.KeyBackspace})
		}
		b.StopTimer()
	}
}
