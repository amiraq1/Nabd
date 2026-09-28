package ui

import (
	"sync"
	"testing"

	"nabd/internal/event"

	tea "github.com/charmbracelet/bubbletea"
)

// TestRunWithEventStream simulates a full agent run: the user sends a
// message, the runner streams events back through the batcher path, and the
// feed stays interactive without losing focus or allowing a second run.
func TestRunWithEventStream(t *testing.T) {
	f := NewFeed()
	f.width = 80
	f.height = 24
	_, _ = f.Update(tea.WindowSizeMsg{Width: 80, Height: 24})

	// The fake runner emits events into a channel the test drains into the
	// feed (mimicking the batcher flush callback).
	var events []event.Event
	var mu sync.Mutex
	emit := func(e event.Event) {
		mu.Lock()
		events = append(events, e)
		mu.Unlock()
	}
	f.SetRunner(runnerFunc(func(text string) error {
		emit(event.Event{Seq: 1, Type: event.UserMsg, Text: text})
		emit(event.Event{Seq: 2, Type: event.TextDelta, Text: "رد "})
		emit(event.Event{Seq: 3, Type: event.TextDelta, Text: "المساعد"})
		emit(event.Event{Seq: 4, Type: event.TurnEnd})
		return nil
	}))

	// Send a message.
	typeIntoFeed(t, f, "سؤال")
	_, cmd := f.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("Enter must produce a run command")
	}
	if !f.busy {
		t.Fatal("feed must be busy during the run")
	}

	// Run the command synchronously (fake runner returns immediately).
	done := cmd().(doneMsg)
	mu.Lock()
	evs := append([]event.Event(nil), events...)
	mu.Unlock()
	// Deliver events to the feed.
	for _, e := range evs {
		_, _ = f.Update(agentEventBatchMsg{Events: []event.Event{e}})
	}
	// Deliver the done message.
	_, _ = f.Update(done)

	if f.busy {
		t.Fatal("feed must be idle after doneMsg")
	}
	if !f.composer.focused() {
		t.Fatal("composer focus must return after the run")
	}
	// The feed projected the assistant reply.
	var sawReply bool
	for _, it := range f.proj.Items() {
		if it.Text == "رد المساعد" {
			sawReply = true
		}
	}
	if !sawReply {
		t.Fatal("feed must show the streamed assistant reply")
	}
	// A new send is possible now.
	typeIntoFeed(t, f, "متابعة")
	_, cmd2 := f.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd2 == nil {
		t.Fatal("send after a completed run must work")
	}
}

// TestTypingDuringRunKeepsText: typing while a run streams events must not
// clear or corrupt the composer draft.
func TestTypingDuringRunKeepsText(t *testing.T) {
	f := NewFeed()
	f.width = 80
	f.height = 24
	f.SetRunner(runnerFunc(func(string) error { return nil }))

	typeIntoFeed(t, f, "draft while running")
	_, cmd := f.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("send must start a run")
	}
	// The run is now "in flight" (busy). Text events arrive.
	_, _ = f.Update(agentEventBatchMsg{Events: []event.Event{
		{Seq: 2, Type: event.TextDelta, Text: "assistant streaming"},
	}})
	// The composer is empty (the draft was sent) and stays empty.
	if v := f.composer.value(); v != "" {
		t.Fatalf("composer after send = %q, want empty", v)
	}
	// Typing while busy accumulates a NEW draft that is NOT sent.
	typeIntoFeed(t, f, "second draft")
	if v := f.composer.value(); v != "second draft" {
		t.Fatalf("new draft while busy = %q", v)
	}
	// Enter while busy rejects and keeps the draft.
	_, cmd2 := f.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd2 != nil {
		t.Fatal("Enter while busy must not send")
	}
	if v := f.composer.value(); v != "second draft" {
		t.Fatalf("draft lost on busy-Enter: %q", v)
	}
	// Run ends; the draft survives and can be sent.
	_, _ = f.Update(doneMsg{err: nil})
	if !f.busy {
		_, cmd3 := f.Update(tea.KeyMsg{Type: tea.KeyEnter})
		if cmd3 == nil {
			t.Fatal("draft send after run end must work")
		}
	}
}
