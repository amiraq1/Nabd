package ui

import (
	"strings"
	"testing"

	"nabd/internal/agent"

	tea "github.com/charmbracelet/bubbletea"
)

// undoCallRecorder installs an OnUndo stub that records the requested depth.
func undoCallRecorder(f *Feed) *[]int {
	calls := &[]int{}
	f.SetCallbacks(&SessionCallbacks{
		OnUndo: func(n int) string {
			*calls = append(*calls, n)
			return "undone"
		},
	})
	return calls
}

// TestFeedBareTokenUndoExecutesOnSingleEnter: an exactly typed slash command is
// what the user meant to run. The first Enter must execute it instead of
// quietly completing it into a trailing-space placeholder.
func TestFeedBareTokenUndoExecutesOnSingleEnter(t *testing.T) {
	f, _ := feedWithRunner(t)
	calls := undoCallRecorder(f)

	typeIntoFeed(t, f, "/undo")
	_, cmd := f.Update(tea.KeyMsg{Type: tea.KeyEnter})

	if cmd != nil {
		t.Fatal("slash command is handled synchronously; it must not return a tea.Cmd")
	}
	if len(*calls) != 1 || (*calls)[0] != 1 {
		t.Fatalf("OnUndo calls = %v, want exactly one call with n=1", *calls)
	}
	if got := f.composer.value(); got != "" {
		t.Fatalf("composer = %q, want cleared after execution", got)
	}
	if got := f.Status(); got != "undone" {
		t.Fatalf("status = %q, want the callback's result", got)
	}
}

// TestFeedPartialSlashTokenOnlyCompletes: a partial token keeps the completion
// behavior — Enter completes it into the composer and never executes.
func TestFeedPartialSlashTokenOnlyCompletes(t *testing.T) {
	f, _ := feedWithRunner(t)
	calls := undoCallRecorder(f)

	typeIntoFeed(t, f, "/und")
	_, cmd := f.Update(tea.KeyMsg{Type: tea.KeyEnter})

	if cmd != nil {
		t.Fatal("completion must not produce a command")
	}
	if len(*calls) != 0 {
		t.Fatalf("partial token executed: OnUndo calls = %v", *calls)
	}
	if got := f.composer.value(); got != "/undo " {
		t.Fatalf("composer = %q, want the completion '/undo '", got)
	}
}

// TestFeedExactTokenWithArgumentPassesN: an argument typed after the command
// still reaches the callback (the space closes the menu, so Enter sends it).
func TestFeedExactTokenWithArgumentPassesN(t *testing.T) {
	f, _ := feedWithRunner(t)
	calls := undoCallRecorder(f)

	typeIntoFeed(t, f, "/undo 2")
	_, _ = f.Update(tea.KeyMsg{Type: tea.KeyEnter})

	if len(*calls) != 1 || (*calls)[0] != 2 {
		t.Fatalf("OnUndo calls = %v, want one call with n=2", *calls)
	}
}

// TestFeedUnknownSlashCommandShowsNoticeNotSilence: an unknown command is
// never swallowed by the completion path; it is reported, exactly as Chat
// reports it, and never reaches the model.
func TestFeedUnknownSlashCommandShowsNoticeNotSilence(t *testing.T) {
	f, r := feedWithRunner(t)

	typeIntoFeed(t, f, "/nosuchcmd")
	_, _ = f.Update(tea.KeyMsg{Type: tea.KeyEnter})

	if got := f.Status(); got != "unknown command: /nosuchcmd" {
		t.Fatalf("status = %q, want the unknown-command notice", got)
	}
	if r.textsLen() != 0 {
		t.Fatal("unknown command must not be sent to the model")
	}
}

// TestChatExactTokenUndoExecutesOnSingleEnter documents the Chat side of the
// comparison: Chat has no completion menu, so a single Enter has always
// executed an exactly typed command. Feed now matches it for the exact case;
// the partial case intentionally differs (Chat reports unknown, Feed completes).
func TestChatExactTokenUndoExecutesOnSingleEnter(t *testing.T) {
	c := NewChat(runnerStub{}, make(chan agent.Event, 1))
	var calls []int
	c.SetCallbacks(&SessionCallbacks{
		OnUndo: func(n int) string {
			calls = append(calls, n)
			return "undone"
		},
	})

	c.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/undo")})
	c.Update(tea.KeyMsg{Type: tea.KeyEnter})

	if len(calls) != 1 || calls[0] != 1 {
		t.Fatalf("chat OnUndo calls = %v, want exactly one call with n=1", calls)
	}
}

// TestChatPartialTokenReportsUnknown documents the other side of the
// comparison: without a completion menu, Chat cannot complete "/und" and says
// so. The Feed keeps completing instead, by design.
func TestChatPartialTokenReportsUnknown(t *testing.T) {
	c := NewChat(runnerStub{}, make(chan agent.Event, 1))
	c.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/und")})
	c.Update(tea.KeyMsg{Type: tea.KeyEnter})

	if got := c.Status(); !strings.Contains(got, "unknown command: /und") {
		t.Fatalf("chat status = %q, want the unknown-command notice", got)
	}
}
