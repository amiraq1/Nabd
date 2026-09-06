package ui

import (
	"fmt"
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

func runBackspaceBenchmark(b *testing.B, nRunes, kKeys int, payloadRune rune, withView bool) {
	b.Helper()
	payload := strings.Repeat(string(payloadRune), nRunes)
	const termWidth = 80
	const termHeight = 24

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		b.StopTimer()
		f := NewFeed()
		f.width = termWidth
		f.height = termHeight
		f.history.add(payload)
		_, _ = f.Update(tea.KeyMsg{Type: tea.KeyUp})
		if got := f.composer.valueLen(); got != nRunes {
			b.Fatalf("setup failed: got %d runes, want %d", got, nRunes)
		}

		b.StartTimer()
		for j := 0; j < kKeys; j++ {
			_, _ = f.Update(tea.KeyMsg{Type: tea.KeyBackspace})
			if withView {
				_ = f.composer.view()
			}
		}
		b.StopTimer()

		if got := f.composer.valueLen(); got != nRunes-kKeys {
			b.Fatalf("after %d backspaces got %d runes, want %d", kKeys, got, nRunes-kKeys)
		}
	}
}

// BenchmarkComposerBackspaceOversized measures the input-update path latency
// when deleting unbroken text in the composer.
// One op = one sequence of K Backspaces on a freshly prepared N-rune payload at width W.
func BenchmarkComposerBackspaceOversized(b *testing.B) {
	// Axis A: Payload Size (fixed K=101 backspaces, ASCII unbroken payload)
	b.Run("AxisA_Size", func(b *testing.B) {
		sizes := []int{1000, 2000, 4000, 8000, maxInputRunes + 100}
		for _, n := range sizes {
			b.Run(fmt.Sprintf("N%d_K101", n), func(b *testing.B) {
				runBackspaceBenchmark(b, n, 101, 'a', false)
			})
		}
	})

	// Axis B: Keypress Count (fixed N=8100 runes, ASCII unbroken payload)
	b.Run("AxisB_Keys", func(b *testing.B) {
		keys := []int{1, 3, 20, 101}
		for _, k := range keys {
			b.Run(fmt.Sprintf("N8100_K%d", k), func(b *testing.B) {
				runBackspaceBenchmark(b, maxInputRunes+100, k, 'a', false)
			})
		}
	})

	// Representative Update-plus-View path matching real UI behavior
	b.Run("UpdateView", func(b *testing.B) {
		b.Run("N8100_K101", func(b *testing.B) {
			runBackspaceBenchmark(b, maxInputRunes+100, 101, 'a', true)
		})
	})

	// Non-ASCII Arabic performance case
	b.Run("Arabic", func(b *testing.B) {
		b.Run("N8100_K101", func(b *testing.B) {
			runBackspaceBenchmark(b, maxInputRunes+100, 101, 'م', false)
		})
	})
}
