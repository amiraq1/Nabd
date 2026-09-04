package ui

import (
	"context"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// keyRunes builds a KeyRunes message from a string.
func keyRunes(s string) tea.KeyMsg {
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
}

// typeIntoFeed types a string rune by rune into the feed's composer via the
// full Update path (as a real terminal would deliver them).
func typeIntoFeed(t *testing.T, f *Feed, s string) {
	t.Helper()
	for _, r := range s {
		_, _ = f.Update(keyRunes(string(r)))
	}
}

// runnerFunc adapts a func to the Runner interface.
type runnerFunc func(text string) error

func (f runnerFunc) Run(ctx context.Context, text string) error { return f(text) }

func TestComposerStartsFocused(t *testing.T) {
	f := NewFeed()
	if !f.composer.focused() {
		t.Fatal("composer must start focused")
	}
}

func TestComposerTypingASCII(t *testing.T) {
	f := NewFeed()
	typeIntoFeed(t, f, "hello world")
	if got := f.composer.value(); got != "hello world" {
		t.Fatalf("composer value = %q, want 'hello world'", got)
	}
}

func TestComposerTypingArabic(t *testing.T) {
	f := NewFeed()
	arabic := "مرحبا بالعالم"
	typeIntoFeed(t, f, arabic)
	if got := f.composer.value(); got != arabic {
		t.Fatalf("composer value = %q, want %q", got, arabic)
	}
}

func TestComposerTypingEmoji(t *testing.T) {
	f := NewFeed()
	emoji := "مرحبا 😀👍"
	typeIntoFeed(t, f, emoji)
	if got := f.composer.value(); got != emoji {
		t.Fatalf("composer value = %q, want %q", got, emoji)
	}
}

func TestComposerEnterSendsValidMessage(t *testing.T) {
	f := NewFeed()
	f.width = 80
	f.height = 24
	// Wire a runner that records the message.
	var got string
	f.SetRunner(runnerFunc(func(text string) error { got = text; return nil }))

	typeIntoFeed(t, f, "hello")
	mdl, cmd := f.Update(tea.KeyMsg{Type: tea.KeyEnter})
	f = mdl.(*Feed)

	if cmd == nil {
		t.Fatal("Enter must produce a command")
	}
	// The message must have been accepted: composer cleared, busy set.
	if v := f.composer.value(); v != "" {
		t.Fatalf("composer after send = %q, want empty", v)
	}
	if !f.busy {
		t.Fatal("feed must be busy after a send")
	}
	if f.HistoryLen() != 1 {
		t.Fatalf("history len = %d, want 1", f.HistoryLen())
	}
	// Execute the command: it calls the runner.
	msg := cmd()
	if _, ok := msg.(doneMsg); !ok {
		t.Fatalf("run command produced %T, want doneMsg", msg)
	}
	if got != "hello" {
		t.Fatalf("runner got %q, want 'hello'", got)
	}
}

func TestComposerEnterEmptyDoesNotSend(t *testing.T) {
	f := NewFeed()
	_, cmd := f.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd != nil {
		t.Fatal("Enter with empty composer must not produce a command")
	}
	if f.busy {
		t.Fatal("empty send must not set busy")
	}
}

func TestComposerEnterWhitespaceDoesNotSend(t *testing.T) {
	f := NewFeed()
	typeIntoFeed(t, f, "   ")
	_, cmd := f.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd != nil {
		t.Fatal("Enter with whitespace-only composer must not produce a command")
	}
	if f.busy {
		t.Fatal("whitespace send must not set busy")
	}
}

func TestComposerNewlineShortcutAddsLine(t *testing.T) {
	f := NewFeed()
	typeIntoFeed(t, f, "line one")
	// Ctrl+J is the documented newline shortcut.
	_, _ = f.Update(tea.KeyMsg{Type: tea.KeyCtrlJ})
	typeIntoFeed(t, f, "line two")
	want := "line one\nline two"
	if got := f.composer.value(); got != want {
		t.Fatalf("composer after Ctrl+J = %q, want %q", got, want)
	}
	// No run was started: not busy, no command pending.
	if f.busy {
		t.Fatal("newline must not start a run")
	}
}

func TestComposerAltEnterAddsNewline(t *testing.T) {
	f := NewFeed()
	typeIntoFeed(t, f, "a")
	_, _ = f.Update(tea.KeyMsg{Type: tea.KeyEnter, Alt: true})
	typeIntoFeed(t, f, "b")
	if got := f.composer.value(); got != "a\nb" {
		t.Fatalf("composer after Alt+Enter = %q, want 'a\\nb'", got)
	}
}

func TestComposerMultilinePasteDoesNotSend(t *testing.T) {
	f := NewFeed()
	// A bracketed paste arrives as one KeyRunes message with Paste=true
	// and newlines inside.
	paste := "first line\nsecond line\nthird line"
	_, _ = f.Update(tea.KeyMsg{
		Type:  tea.KeyRunes,
		Runes: []rune(paste),
		Paste: true,
	})
	if got := f.composer.value(); got != paste {
		t.Fatalf("composer after paste = %q, want %q", got, paste)
	}
	if f.busy {
		t.Fatal("multiline paste must not auto-send")
	}
	if f.HistoryLen() != 0 {
		t.Fatal("paste must not enter history")
	}
}

func TestComposerSendClearsComposer(t *testing.T) {
	f := NewFeed()
	f.SetRunner(runnerFunc(func(string) error { return nil }))
	typeIntoFeed(t, f, "to send")
	_, cmd := f.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("Enter must produce a command")
	}
	if v := f.composer.value(); v != "" {
		t.Fatalf("composer after send = %q, want empty", v)
	}
}

// TestComposerSendFailureKeepsText verifies the send-failure contract: when
// the runner is unavailable, the send is rejected in place — the composer
// text is kept, nothing enters history, no busy state and no panic.
func TestComposerSendFailureKeepsText(t *testing.T) {
	f := NewFeed()
	// No runner wired: trySend rejects immediately.
	typeIntoFeed(t, f, "keep me")
	_, cmd := f.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd != nil {
		t.Fatal("Enter with no runner must not produce a command")
	}
	if v := f.composer.value(); v != "keep me" {
		t.Fatalf("composer after failed send = %q, want 'keep me'", v)
	}
	if f.busy {
		t.Fatal("failed send must not set busy")
	}
	if f.HistoryLen() != 0 {
		t.Fatal("failed send must not enter history")
	}
	// The status line must carry the error (ASCII-safe).
	if f.status == "" {
		t.Fatal("failed send must set a status")
	}
}
