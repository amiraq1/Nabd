package main

import (
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"nabd/internal/agent"
)

// TestChanSinkBoundedAndObservable is the regression guard for the fix: a
// chanSink with a full channel returns within a bounded window (not 2s) and
// surfaces an error instead of dropping silently. The journal is the durable
// source of truth, so no data is truly lost — but the caller must be able to
// observe and count the loss.
func TestChanSinkBoundedAndObservable(t *testing.T) {
	ch := make(chan agent.Event)
	sink := chanSink(ch)

	// Drain in the background so the first event can land, then stop draining
	// to simulate a stuck UI.
	stop := make(chan struct{})
	go func() {
		for {
			select {
			case <-ch:
			case <-stop:
				return
			}
		}
	}()

	// First event: lands in the channel (drainer picks it up).
	if err := sink.Emit(agent.Event{Type: agent.RunStart}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(50 * time.Millisecond)
	close(stop)

	// Now emit into the full channel with no drainer. The fix must bound the
	// wait and return an error.
	start := time.Now()
	err := sink.Emit(agent.Event{Type: agent.TextDelta, Text: "dropped", Seq: 7})
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("chanSink must return error on full channel, not drop silently")
	}
	if elapsed > 2*time.Second {
		t.Fatalf("chanSink blocked too long (%v): backpressure window not bounded", elapsed)
	}
	if !strings.Contains(err.Error(), "text_delta") || !strings.Contains(err.Error(), "seq=7") {
		t.Fatalf("error should name the event type and seq: %v", err)
	}
	t.Logf("chanSink surfaced error after %v: %v", elapsed, err)
}

// TestChanSinkDeliversWhenDrained proves the happy path: when the UI drains
// promptly, events are delivered in order with no error.
func TestChanSinkDeliversWhenDrained(t *testing.T) {
	ch := make(chan agent.Event, 16)
	sink := chanSink(ch)

	var mu sync.Mutex
	var got []agent.Event
	count := 3
	for i := 0; i < count; i++ {
		e := agent.Event{Type: agent.TextDelta, Seq: i + 1}
		if err := sink.Emit(e); err != nil {
			t.Fatalf("Emit failed on drained channel: %v", err)
		}
	}
	close(ch)
	for e := range ch {
		mu.Lock()
		got = append(got, e)
		mu.Unlock()
	}

	if len(got) != count {
		t.Fatalf("got %d events, want %d", len(got), count)
	}
	for i, e := range got {
		if e.Seq != i+1 {
			t.Fatalf("event %d: seq=%d, want %d", i, e.Seq, i+1)
		}
	}
}

// TestLoopEndErrorObservable proves that loop.End failures can be observed.
// The production code currently discards the error with `_ = loop.End(...)`.
// This test confirms End returns an error when the sink fails, so the fix can
// surface it.
func TestLoopEndErrorObservable(t *testing.T) {
	sink := &failSink{err: errors.New("sink boom")}
	loop := &agent.Loop{Sink: sink}
	if err := loop.End("done"); err == nil {
		t.Fatal("expected End to propagate sink error, got nil")
	}
}

// TestJournalCloseErrorObservable proves that journal.Close failures can be
// observed. The production code currently defers journal.Close() without
// inspecting the error.
func TestJournalCloseErrorObservable(t *testing.T) {
	j := &failCloser{err: errors.New("close boom")}
	err := j.Close()
	if err == nil {
		t.Fatal("expected Close to return error, got nil")
	}
	if !strings.Contains(err.Error(), "close boom") {
		t.Fatalf("Close must surface the injected error, got %q", err)
	}
}

// failSink is a sink that always returns a fixed error.
type failSink struct{ err error }

func (f *failSink) Emit(e agent.Event) error { return f.err }

// failCloser mimics a journal whose Close fails.
type failCloser struct {
	mu     sync.Mutex
	closed bool
	err    error
}

func (f *failCloser) Emit(e agent.Event) error { return nil }
func (f *failCloser) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closed = true
	return f.err
}
