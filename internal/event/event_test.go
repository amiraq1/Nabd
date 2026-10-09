package event_test

import (
	"strings"
	"testing"

	"nabd/internal/event"
)

// chain builds n events with Seq 1..n chained by Parent.
func chain(n int, typ event.EventType) []event.Event {
	evs := make([]event.Event, n)
	for i := range evs {
		evs[i] = event.Event{Seq: i + 1, Parent: i, Type: typ}
	}
	return evs
}

func seqs(evs []event.Event) []int {
	out := make([]int, len(evs))
	for i, e := range evs {
		out[i] = e.Seq
	}
	return out
}

func equalInts(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestLiveEmpty(t *testing.T) {
	if got := event.Live(nil); got != nil {
		t.Fatalf("Live(nil) = %v, want nil", got)
	}
	if got := event.Live([]event.Event{}); got != nil {
		t.Fatalf("Live(empty) = %v, want nil", got)
	}
}

func TestLiveReturnsLinearBranch(t *testing.T) {
	evs := chain(5, event.UserMsg)
	if got := seqs(event.Live(evs)); !equalInts(got, []int{1, 2, 3, 4, 5}) {
		t.Fatalf("Live = %v, want [1 2 3 4 5]", got)
	}
}

func TestLiveFollowsRewindBranch(t *testing.T) {
	// Mirrors testdata/replay-corpus/rewind.jsonl: seq 6 parents seq 1,
	// orphaning seqs 2-5. Live must see only the branch ending at the
	// last written event.
	evs := chain(5, event.UserMsg)
	evs = append(evs, event.Event{Seq: 6, Parent: 1, Type: event.Rewind})
	if got := seqs(event.Live(evs)); !equalInts(got, []int{1, 6}) {
		t.Fatalf("Live = %v, want [1 6]", got)
	}
}

func TestLiveAppliesCompactionBoundary(t *testing.T) {
	evs := chain(10, event.UserMsg)
	// Compact at seq 7 keeps seqs >= 5; the summary heads the result.
	evs[6].Type = event.Compact
	evs[6].FirstKept = 5
	if got := seqs(event.Live(evs)); !equalInts(got, []int{7, 5, 6, 8, 9, 10}) {
		t.Fatalf("Live = %v, want [7 5 6 8 9 10]", got)
	}
}

func TestLiveStopsAtBrokenParent(t *testing.T) {
	evs := chain(3, event.UserMsg)
	// Parent points at a seq that does not exist: branch ends there
	// instead of walking into garbage.
	evs = append(evs, event.Event{Seq: 4, Parent: 99, Type: event.UserMsg})
	if got := seqs(event.Live(evs)); !equalInts(got, []int{4}) {
		t.Fatalf("Live = %v, want [4]", got)
	}
}

func TestForStoreLeavesSmallOutputAlone(t *testing.T) {
	e := event.Event{Seq: 1, Call: &event.ToolCall{ID: "1", Output: "short"}}
	got := e.ForStore()
	if got.Call.Output != "short" || got.Call.TruncatedBytes != 0 {
		t.Fatalf("small output mutated: %+v", got.Call)
	}
}

func TestForStoreTruncatesAtRuneBoundary(t *testing.T) {
	// 16383 ASCII + 2-byte rune + 100 ASCII = 16485 > MaxPersistedOutput.
	// The cut must back off to the rune start, never splitting the rune.
	out := strings.Repeat("a", 16383) + "é" + strings.Repeat("b", 100)
	e := event.Event{Seq: 1, Call: &event.ToolCall{ID: "1", Output: out}}
	got := e.ForStore()
	if got.Call.TruncatedBytes != 102 {
		t.Fatalf("TruncatedBytes = %d, want 102", got.Call.TruncatedBytes)
	}
	if !strings.HasSuffix(got.Call.Output, "\n...[truncated 102 bytes]") {
		t.Fatalf("missing prose marker: %q", got.Call.Output[len(got.Call.Output)-30:])
	}
	body := strings.TrimSuffix(got.Call.Output, "\n...[truncated 102 bytes]")
	if len(body) != 16383 || !strings.HasSuffix(body, "a") {
		t.Fatalf("cut split the rune or mis-sized: len=%d", len(body))
	}
	// The input event must not be mutated.
	if len(e.Call.Output) != 16485 || e.Call.TruncatedBytes != 0 {
		t.Fatal("ForStore mutated the input event")
	}
}

func TestDecisionFailClosed(t *testing.T) {
	var zero event.Decision
	if zero != event.Deny || zero.String() != "deny" {
		t.Fatalf("zero Decision = %v/%q, want Deny/deny", zero, zero.String())
	}
	if got := event.Decision(99).String(); got != "deny" {
		t.Fatalf("Decision(99).String() = %q, want deny", got)
	}
	if got := event.AllowOnce.String(); got != "once" {
		t.Fatalf("AllowOnce.String() = %q, want once", got)
	}
	var d event.Decision
	if err := d.UnmarshalText([]byte("bogus")); err != nil || d != event.Deny {
		t.Fatalf("UnmarshalText(bogus) = %v, %v; want Deny, nil", d, err)
	}
	if err := d.UnmarshalText([]byte("session")); err != nil || d != event.AllowSession {
		t.Fatalf("UnmarshalText(session) = %v, %v; want AllowSession, nil", d, err)
	}
	b, err := event.AllowOnce.MarshalText()
	if err != nil || string(b) != "once" {
		t.Fatalf("MarshalText(AllowOnce) = %q, %v; want once, nil", b, err)
	}
}
