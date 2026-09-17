package ui

import (
	"strings"
	"testing"

	"nabd/internal/providercmd"

	tea "github.com/charmbracelet/bubbletea"
)

// TestFeedRefusedConnectArgumentLeavesNoDraft pins that a refused /connect
// argument does not survive as the history draft. Every keystroke goes
// through composerEdit, which stores the composer text via history.setDraft;
// the refusal branch cleared the composer but did not reset browsing, so the
// typed line — a rejected credential argument included — stayed in memory.
// This is an in-memory artifact, not a display leak, and the assertion is
// exactly that: the draft no longer holds the refused bytes.
func TestFeedRefusedConnectArgumentLeavesNoDraft(t *testing.T) {
	const canary = "sk-canary-refused-argument-1234567890"

	f := NewFeed()
	f.SetCallbacks(&SessionCallbacks{
		OnConnect: func(providerID, key string) (string, error) { return "connected", nil },
	})

	for _, r := range "/connect testprov " + canary {
		f.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	f.Update(tea.KeyMsg{Type: tea.KeyEnter})

	if got := f.Status(); got != providercmd.ErrKeyAsArgument.Error() {
		t.Fatalf("status = %q, want ErrKeyAsArgument", got)
	}
	if strings.Contains(f.ComposerValue(), canary) {
		t.Fatalf("composer retained the refused argument: %q", f.ComposerValue())
	}
	if strings.Contains(f.history.draft, canary) {
		t.Fatalf("history draft retained the refused credential argument: %q", f.history.draft)
	}
}
