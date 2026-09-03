package ui

import (
	"testing"

	"nabd/internal/agent"
	"nabd/internal/presentation"

	tea "github.com/charmbracelet/bubbletea"
)

// TestNoDuplicateUserFeedItem: an accepted send does NOT add a User
// FeedItem manually. The user message appears in the feed only when the
// loop emits user_msg through the event pipeline (projector), so there is
// exactly one ItemUserMsg.
func TestNoDuplicateUserFeedItem(t *testing.T) {
	f := NewFeed()
	f.width = 80
	f.height = 24
	f.SetRunner(runnerFunc(func(string) error { return nil }))

	// A previous run boundary exists in the feed.
	_, _ = f.Update(agentEventBatchMsg{Events: []agent.Event{
		{Seq: 1, Type: agent.RunStart, Text: "start"},
	}})

	// Send a message through the real key path.
	typeIntoFeed(t, f, "my question")
	if !f.composer.focused() {
		t.Fatal("composer must be focused before send")
	}
	if v := f.composer.value(); v != "my question" {
		t.Fatalf("composer value = %q", v)
	}
	_, cmd := f.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("Enter must produce a run command")
	}
	_ = cmd

	// Before the loop emits user_msg, the feed must NOT contain the
	// message: the projector is the only source of feed items.
	items := f.proj.Items()
	for _, it := range items {
		if it.Type == presentation.ItemUserMsg {
			t.Fatalf("user message appeared in the feed before the loop emitted user_msg: %+v", it)
		}
	}

	// The loop emits user_msg through the event pipeline.
	_, _ = f.Update(agentEventBatchMsg{Events: []agent.Event{
		{Seq: 2, Type: agent.UserMsg, Text: "my question"},
	}})

	// Exactly one ItemUserMsg now, with the right text.
	count := 0
	for _, it := range f.proj.Items() {
		if it.Type == presentation.ItemUserMsg {
			count++
			if it.Text != "my question" {
				t.Fatalf("user item text = %q, want 'my question'", it.Text)
			}
		}
	}
	if count != 1 {
		t.Fatalf("ItemUserMsg count = %d, want exactly 1", count)
	}
}

// TestRejectedBusySendNotDuplicatedInFeed: a message rejected while busy
// never appears in the feed, not even once (the UI never adds it and the
// loop never runs it).
func TestRejectedBusySendNotDuplicatedInFeed(t *testing.T) {
	f, r := feedWithBlockingRunner(t)
	startBlockingRun(t, f, r, "first run")

	// Second send while busy is rejected.
	typeIntoFeed(t, f, "rejected second")
	_, cmd := f.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd != nil {
		t.Fatal("busy send must not produce a run command")
	}
	// No user_msg was ever emitted by the loop for the rejected message.
	_, _ = f.Update(agentEventBatchMsg{Events: []agent.Event{
		{Seq: 2, Type: agent.UserMsg, Text: "first run"},
	}})
	count := 0
	for _, it := range f.proj.Items() {
		if it.Type == presentation.ItemUserMsg {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("feed user items = %d, want exactly 1 (only the accepted run)", count)
	}
	close(r.release)
	r.waitReturned(t)
}
