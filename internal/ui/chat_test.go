package ui

import (
	"context"
	"strconv"
	"strings"
	"testing"

	"nabd/internal/agent"

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
	ch := make(chan agent.Event, 8)
	m := asChat(t, NewChat(runnerStub{}, ch))

	// Delta 1: accumulates, no print, buffer holds it.
	mdl, _ := m.Update(evMsg(agent.Event{Type: agent.TextDelta, Text: "فقرة أولى "}))
	m = asChat(t, mdl)
	if m.buf != "فقرة أولى " {
		t.Fatalf("after delta 1: buf=%q, want %q", m.buf, "فقرة أولى ")
	}

	// Delta 2: accumulates more.
	mdl, _ = m.Update(evMsg(agent.Event{Type: agent.TextDelta, Text: "فقرة ثانية"}))
	m = asChat(t, mdl)
	if m.buf != "فقرة أولى فقرة ثانية" {
		t.Fatalf("after delta 2: buf=%q", m.buf)
	}

	// Interrupted arrives through the channel: the evMsg path flushes the
	// buffer (that is the whole mechanism — the event carries the flush).
	mdl, _ = m.Update(evMsg(agent.Event{Type: agent.Interrupted, Text: "ctrl+c"}))
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
	ch := make(chan agent.Event, 8)
	m := asChat(t, NewChat(runnerStub{}, ch))

	mdl, _ := m.Update(evMsg(agent.Event{Type: agent.TextDelta, Text: "كلمة "}))
	m = asChat(t, mdl)
	mdl, _ = m.Update(evMsg(agent.Event{Type: agent.TextDelta, Text: "أخرى"}))
	m = asChat(t, mdl)
	if m.buf != "كلمة أخرى" {
		t.Fatalf("before turn end: buf=%q", m.buf)
	}

	mdl, _ = m.Update(evMsg(agent.Event{Type: agent.TurnEnd}))
	m = asChat(t, mdl)
	if m.buf != "" {
		t.Fatalf("after turn end: buf=%q, want empty", m.buf)
	}
}

func TestHelpMentionsRewind(t *testing.T) {
	m := NewChat(nil, nil)
	got := m.command("/help")
	if !strings.Contains(got, "/rewind") {
		t.Errorf("/help = %q, missing /rewind", got)
	}
	for _, want := range []string{"/undo", "/edits", "/ctx", "/compact"} {
		if !strings.Contains(got, want) {
			t.Errorf("/help = %q, missing %s", got, want)
		}
	}
}

func TestUnknownCommandSaysSo(t *testing.T) {
	m := NewChat(nil, nil)
	if got := m.command("/bogus"); !strings.Contains(got, "unknown command") {
		t.Errorf("/bogus = %q, want an unknown-command notice", got)
	}
}

// /rewind is advertised in /help, so it must actually dispatch to its
// handler rather than fall through to the unknown-command reply.
func TestRewindDispatchesToHandler(t *testing.T) {
	called := 0
	m := NewChat(nil, nil)
	m.OnRewind = func(n int) string {
		called++
		return "rewound " + strconv.Itoa(n)
	}

	if got := m.command("/rewind 2"); got != "rewound 2" || called != 1 {
		t.Fatalf("/rewind 2 = %q (handler called %d times), want the handler to run", got, called)
	}
}

func TestRewindWithoutHandlerIsNotUnknown(t *testing.T) {
	m := NewChat(nil, nil)
	if got := m.command("/rewind"); strings.Contains(got, "unknown command") {
		t.Errorf("/rewind with no handler = %q, must not be the unknown-command reply", got)
	}
}
