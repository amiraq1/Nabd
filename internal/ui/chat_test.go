package ui

import (
	"context"
	"strings"
	"testing"

	"nabd/internal/event"

	tea "github.com/charmbracelet/bubbletea"
)

// runnerStub satisfies Chat.Runner without touching any provider.
type runnerStub struct{}

func (runnerStub) Run(ctx context.Context, text string) error { return nil }

// asChat narrows the tea.Model back to *Chat for buffer inspection.
func asChat(t *testing.T, mdl tea.Model) *Chat {
	t.Helper()
	c, ok := mdl.(*Chat)
	if !ok {
		t.Fatalf("model is %T, want *Chat", mdl)
	}
	return c
}

// TestBufFlushViaEventChannel proves the core invariant behind the doneMsg
// change: the accumulated TextDelta buffer is flushed by the Interrupted
// event arriving through the event channel (the evMsg path), so doneMsg does
// not need its own flush. This is the runtime proof that removing the
// doneMsg flush did not drop the final paragraph.
func TestBufFlushViaEventChannel(t *testing.T) {
	ch := make(chan event.Event, 8)
	m := asChat(t, NewChat(runnerStub{}, ch))

	// Delta 1: accumulates, no print, buffer holds it.
	mdl, _ := m.Update(evMsg(event.Event{Type: event.TextDelta, Text: "فقرة أولى "}))
	m = asChat(t, mdl)
	if m.buf != "فقرة أولى " {
		t.Fatalf("after delta 1: buf=%q, want %q", m.buf, "فقرة أولى ")
	}

	// Delta 2: accumulates more.
	mdl, _ = m.Update(evMsg(event.Event{Type: event.TextDelta, Text: "فقرة ثانية"}))
	m = asChat(t, mdl)
	if m.buf != "فقرة أولى فقرة ثانية" {
		t.Fatalf("after delta 2: buf=%q", m.buf)
	}

	// Interrupted arrives through the channel: the evMsg path flushes the
	// buffer (that is the whole mechanism — the event carries the flush).
	mdl, _ = m.Update(evMsg(event.Event{Type: event.Interrupted, Text: "ctrl+c"}))
	m = asChat(t, mdl)
	if m.buf != "" {
		t.Fatalf("after interrupted: buf=%q, want empty (must be flushed)", m.buf)
	}

	// doneMsg arrives later: buffer already empty, so it has nothing to
	// print and drops nothing.
	mdl, _ = m.Update(doneMsg{err: nil})
	m = asChat(t, mdl)
	if m.buf != "" {
		t.Fatalf("after doneMsg: buf=%q, want empty", m.buf)
	}
	if m.running {
		t.Fatal("after doneMsg: running should be false")
	}
}

// TestBufFlushOnTurnEnd mirrors the same invariant for a successful run: the
// TurnEnd event (rendered as "" but still a non-delta event) flushes the
// buffer through the evMsg path.
func TestBufFlushOnTurnEnd(t *testing.T) {
	ch := make(chan event.Event, 8)
	m := asChat(t, NewChat(runnerStub{}, ch))

	mdl, _ := m.Update(evMsg(event.Event{Type: event.TextDelta, Text: "كلمة "}))
	m = asChat(t, mdl)
	mdl, _ = m.Update(evMsg(event.Event{Type: event.TextDelta, Text: "أخرى"}))
	m = asChat(t, mdl)
	if m.buf != "كلمة أخرى" {
		t.Fatalf("before turn end: buf=%q", m.buf)
	}

	mdl, _ = m.Update(evMsg(event.Event{Type: event.TurnEnd}))
	m = asChat(t, mdl)
	if m.buf != "" {
		t.Fatalf("after turn end: buf=%q, want empty", m.buf)
	}
}

// TestChatBashSessionKeyRejected asserts that Chat neither displays the session
// grant hint for bash nor accepts 'a' or 'A' to reply with AllowSession.
func TestChatBashSessionKeyRejected(t *testing.T) {
	ch := make(chan event.Event, 8)
	chat := asChat(t, NewChat(runnerStub{}, ch))
	ap := NewApprover()
	chat.Approve = ap

	// Ask permission for bash
	mdl, _ := chat.Update(evMsg(event.Event{
		Type: event.PermAsk,
		Call: &event.ToolCall{ID: "c_bash", Name: "bash"},
	}))
	chat = asChat(t, mdl)

	// View must not show "a allow session"
	view := chat.View()
	if strings.Contains(view, "a allow session") {
		t.Fatalf("chat view exposes session grant for bash:\n%s", view)
	}
	if !strings.Contains(view, "(no session allow for commands)") {
		t.Fatalf("chat view missing no session allow notice for bash:\n%s", view)
	}

	// Sending 'a' must NOT reply with session
	mdl, _ = chat.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("a")})
	chat = asChat(t, mdl)
	select {
	case d := <-ap.reply:
		t.Fatalf("chat approved session unexpectedly on 'a': %v", d)
	default:
	}

	// Sending 'A' must NOT reply with session
	mdl, _ = chat.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("A")})
	chat = asChat(t, mdl)
	select {
	case d := <-ap.reply:
		t.Fatalf("chat approved session unexpectedly on 'A': %v", d)
	default:
	}

	// Sending 'y' must reply AllowOnce
	mdl, _ = chat.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("y")})
	chat = asChat(t, mdl)
	if chat.pending != nil {
		t.Fatal("chat pending must be nil after 'y'")
	}
	select {
	case d := <-ap.reply:
		if d != event.AllowOnce {
			t.Fatalf("chat approver received %v, want AllowOnce", d)
		}
	default:
		t.Fatal("chat approver received no reply after 'y'")
	}
}
