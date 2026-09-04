package ui

import (
	"errors"
	"strings"
	"testing"

	"nabd/internal/agent"

	tea "github.com/charmbracelet/bubbletea"
)

// linesContaining returns the rendered feed lines that contain s (ANSI
// styling ignored via substring search on the plain payload).
func linesContaining(lines []string, s string) []string {
	var out []string
	for _, l := range lines {
		if strings.Contains(l, s) {
			out = append(out, l)
		}
	}
	return out
}

// TestFeedRunErrorEntersFeedPermanently: a run that fails without the loop
// having journaled a RunError (e.g. the runner itself returned an error)
// must land in the Feed as a scrollable line, not only in the transient
// status row. Typing in the composer must not remove it.
func TestFeedRunErrorEntersFeedPermanently(t *testing.T) {
	f := NewFeed()
	f.width = 60
	f.height = 12
	f.SetRunner(runnerFunc(func(string) error { return errors.New("boom") }))

	_, _ = f.Update(agentEventBatchMsg{Events: []agent.Event{
		{Seq: 1, Type: agent.RunStart, Text: "start"},
	}})
	before := len(f.lines)

	typeIntoFeed(t, f, "hello")
	_, cmd := f.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("send must produce a run command")
	}
	msg := cmd()
	done, ok := msg.(doneMsg)
	if !ok || done.err == nil {
		t.Fatalf("run cmd produced %T (%v), want doneMsg with error", msg, msg)
	}
	_, _ = f.Update(done)

	if got := linesContaining(f.lines, "boom"); len(got) != 1 {
		t.Fatalf("error must be exactly one feed line, got %d:\n%s", len(got), strings.Join(f.lines, "\n"))
	}
	if len(f.lines) <= before {
		t.Fatalf("feed lines did not grow: before=%d after=%d", before, len(f.lines))
	}
	if f.status != "" {
		t.Fatalf("status must stay transient/empty after a journaled-in-feed error, got %q", f.status)
	}

	// First keystroke used to wipe the notice (it lived in m.status).
	typeIntoFeed(t, f, "next draft")
	if got := linesContaining(f.lines, "boom"); len(got) != 1 {
		t.Fatalf("error notice vanished after typing; lines:\n%s", strings.Join(f.lines, "\n"))
	}
}

// TestFeedRunErrorNotDuplicatedWhenJournaled: when the loop already emitted
// RunError through the event pipeline, doneMsg must not add a second line.
func TestFeedRunErrorNotDuplicatedWhenJournaled(t *testing.T) {
	f := NewFeed()
	f.width = 60
	f.height = 12
	f.SetRunner(runnerFunc(func(string) error { return errors.New("boom") }))

	typeIntoFeed(t, f, "hello")
	_, cmd := f.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("send must produce a run command")
	}
	// The loop journals the failure before Run returns.
	_, _ = f.Update(agentEventBatchMsg{Events: []agent.Event{
		{Seq: 1, Type: agent.UserMsg, Text: "hello"},
		{Seq: 2, Type: agent.RunError, Err: "boom"},
	}})
	_, _ = f.Update(cmd())

	if got := linesContaining(f.lines, "boom"); len(got) != 1 {
		t.Fatalf("journaled error must appear once, got %d:\n%s", len(got), strings.Join(f.lines, "\n"))
	}
}

// TestFeedNoticeKeepsChronologicalOrder: a UI notice is anchored after the
// last event seen; later journal events render below it, not above.
func TestFeedNoticeKeepsChronologicalOrder(t *testing.T) {
	f := NewFeed()
	f.width = 60
	f.height = 12
	f.SetRunner(runnerFunc(func(string) error { return errors.New("first failure") }))

	_, _ = f.Update(agentEventBatchMsg{Events: []agent.Event{
		{Seq: 1, Type: agent.UserMsg, Text: "alpha"},
	}})
	typeIntoFeed(t, f, "x")
	_, cmd := f.Update(tea.KeyMsg{Type: tea.KeyEnter})
	_, _ = f.Update(cmd())

	_, _ = f.Update(agentEventBatchMsg{Events: []agent.Event{
		{Seq: 5, Type: agent.UserMsg, Text: "omega"},
	}})

	idx := func(s string) int {
		for i, l := range f.lines {
			if strings.Contains(l, s) {
				return i
			}
		}
		return -1
	}
	a, n, o := idx("alpha"), idx("first failure"), idx("omega")
	if a < 0 || n < 0 || o < 0 {
		t.Fatalf("missing lines a=%d n=%d o=%d:\n%s", a, n, o, strings.Join(f.lines, "\n"))
	}
	if !(a < n && n < o) {
		t.Fatalf("order broken: alpha=%d notice=%d omega=%d", a, n, o)
	}
}

// TestFeedNoticeCountsInScroll: the notice contributes to len(lines), so
// PgUp can scroll it into view and scrollTop is bounded by it.
func TestFeedNoticeCountsInScroll(t *testing.T) {
	f := NewFeed()
	f.width = 60
	_, _ = f.Update(tea.WindowSizeMsg{Width: 60, Height: 8})
	f.SetRunner(runnerFunc(func(string) error { return errors.New("scroll me") }))

	var evs []agent.Event
	for i := 1; i <= 20; i++ {
		evs = append(evs, agent.Event{Seq: i, Type: agent.UserMsg, Text: "line"})
	}
	_, _ = f.Update(agentEventBatchMsg{Events: evs})
	base := len(f.lines)

	typeIntoFeed(t, f, "x")
	_, cmd := f.Update(tea.KeyMsg{Type: tea.KeyEnter})
	_, _ = f.Update(cmd())
	if len(f.lines) != base+1 {
		t.Fatalf("notice must add exactly one line: base=%d now=%d", base, len(f.lines))
	}

	// Leave the composer so the viewport owns keys, then scroll to the top.
	f.composer.blur()
	_, _ = f.Update(tea.KeyMsg{Type: tea.KeyHome})
	vh := f.viewportHeight()
	if want := max(0, len(f.lines)-vh); f.scrollTop != want {
		t.Fatalf("scrollTop=%d want %d (lines=%d vh=%d)", f.scrollTop, want, len(f.lines), vh)
	}
}
