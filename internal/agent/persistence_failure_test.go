package agent

import (
	"context"
	"errors"
	"strings"
	"testing"
)

type alwaysFailSink struct{ err error }

func (s alwaysFailSink) Emit(Event) error { return s.err }

func TestRewindPropagatesSinkFailureWithoutMutatingHistory(t *testing.T) {
	boom := errors.New("disk full")
	l := &Loop{}

	// Seed in-memory history without a sink, then enable the failing journal.
	if err := l.emit(Event{Type: UserMsg, Text: "first"}); err != nil {
		t.Fatal(err)
	}
	if err := l.emit(Event{Type: UserMsg, Text: "second"}); err != nil {
		t.Fatal(err)
	}
	before := l.Hist()
	l.Sink = alwaysFailSink{err: boom}

	if _, err := l.Rewind(1); !errors.Is(err, boom) {
		t.Fatalf("Rewind error = %v, want %v", err, boom)
	}
	if got := l.Hist(); len(got) != len(before) {
		t.Fatalf("failed Rewind changed history length: got %d, want %d", len(got), len(before))
	}
	if got := l.Hist()[len(l.Hist())-1].Type; got == Rewind {
		t.Fatal("failed Rewind appended a Rewind event")
	}
}

func TestCompactPropagatesSinkFailureWithoutMutatingHistory(t *testing.T) {
	boom := errors.New("journal unavailable")
	l := &Loop{Budget: NewBudget()}
	if err := l.emit(Event{Type: UserMsg, Text: strings.Repeat("old context ", 100)}); err != nil {
		t.Fatal(err)
	}
	if err := l.emit(Event{Type: UserMsg, Text: "keep this turn"}); err != nil {
		t.Fatal(err)
	}
	before := l.Hist()
	l.Sink = alwaysFailSink{err: boom}

	if err := l.Compact(context.Background(), 20); !errors.Is(err, boom) {
		t.Fatalf("Compact error = %v, want %v", err, boom)
	}
	got := l.Hist()
	if len(got) != len(before) {
		t.Fatalf("failed Compact changed history length: got %d, want %d", len(got), len(before))
	}
	for _, e := range got {
		if e.Type == Compact {
			t.Fatal("failed Compact appended a Compact event")
		}
	}
}
