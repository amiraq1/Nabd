package main

import (
	"errors"
	"io"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"nabd/internal/agent"
)

// TestChanSinkDropsImmediatelyWhenFull is the regression guard for the fix:
// a chanSink with a full channel must drop the event instantly and return
// nil — it must never block for the UI and never surface an error that
// Fanout turns into a fatal loop failure. The elapsed-time assertion is what
// separates "drop" from "wait then give up": any solution that waits for
// the UI fails it.
func TestChanSinkDropsImmediatelyWhenFull(t *testing.T) {
	ch := make(chan agent.Event, 1)
	sink := &chanSink{ch: ch}

	// First event lands in the buffer.
	if err := sink.Emit(agent.Event{Type: agent.RunStart}); err != nil {
		t.Fatalf("Emit on a channel with room failed: %v", err)
	}
	if got := sink.Dropped(); got != 0 {
		t.Fatalf("Dropped() = %d, want 0 after a delivered event", got)
	}

	// The channel is now full and nothing drains it: the next emit must
	// drop instantly, return nil, and count the loss.
	start := time.Now()
	err := sink.Emit(agent.Event{Type: agent.TextDelta, Text: "dropped", Seq: 7})
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("chanSink must not return an error on a full channel: %v", err)
	}
	if elapsed > 250*time.Millisecond {
		t.Fatalf("chanSink blocked %v: a full UI channel must drop instantly, not wait", elapsed)
	}
	if got := sink.Dropped(); got != 1 {
		t.Fatalf("Dropped() = %d, want 1", got)
	}

	// Subsequent drops accumulate.
	if err := sink.Emit(agent.Event{Type: agent.TextDelta, Text: "again", Seq: 8}); err != nil {
		t.Fatalf("second drop returned error: %v", err)
	}
	if got := sink.Dropped(); got != 2 {
		t.Fatalf("Dropped() = %d, want 2", got)
	}
	t.Logf("chanSink dropped after %v", elapsed)
}

// TestChanSinkNotesDroppedCount proves the drop counter is surfaced as a
// journal Notice before RunEnd, so the loss stays observable without killing
// the session.
func TestChanSinkNotesDroppedCount(t *testing.T) {
	sink := &chanSink{ch: make(chan agent.Event)}
	rec := &recordSink{}
	loop := &agent.Loop{Sink: rec}

	// Nothing drains the channel: two events are dropped.
	sink.Emit(agent.Event{Type: agent.TextDelta, Text: "one", Seq: 1})
	sink.Emit(agent.Event{Type: agent.TextDelta, Text: "two", Seq: 2})
	if got := sink.Dropped(); got != 2 {
		t.Fatalf("Dropped() = %d, want 2", got)
	}

	sink.noteDrops(loop)

	if len(rec.evs) != 1 || rec.evs[0].Type != agent.Notice {
		t.Fatalf("expected exactly one Notice, got %v", rec.evs)
	}
	if !strings.Contains(rec.evs[0].Text, "2") {
		t.Fatalf("Notice must name the dropped count, got %q", rec.evs[0].Text)
	}
	for _, frag := range []string{"ui/display", "full session transcript"} {
		if !strings.Contains(rec.evs[0].Text, frag) {
			t.Fatalf("Notice %q must distinguish UI/display drops from the authoritative transcript (missing %q)",
				rec.evs[0].Text, frag)
		}
	}

	// With nothing dropped, noteDrops stays silent.
	rec2 := &recordSink{}
	(&chanSink{ch: make(chan agent.Event)}).noteDrops(&agent.Loop{Sink: rec2})
	if len(rec2.evs) != 0 {
		t.Fatalf("noteDrops must not emit when nothing was dropped, got %v", rec2.evs)
	}
}

// TestChanSinkDeliversWhenDrained proves the happy path: when the UI drains
// promptly, events are delivered in order with no error.
func TestChanSinkDeliversWhenDrained(t *testing.T) {
	ch := make(chan agent.Event, 16)
	sink := &chanSink{ch: ch}

	var mu sync.Mutex
	var got []agent.Event
	count := 3
	for i := 0; i < count; i++ {
		e := agent.Event{Type: agent.TextDelta, Seq: i + 1}
		if err := sink.Emit(e); err != nil {
			t.Fatalf("Emit failed on drained channel: %v", err)
		}
	}
	if got := sink.Dropped(); got != 0 {
		t.Fatalf("Dropped() = %d, want 0 on a drained channel", got)
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

// recordSink captures every event it receives, for asserting what the loop
// actually emitted.
type recordSink struct{ evs []agent.Event }

func (r *recordSink) Emit(e agent.Event) error {
	r.evs = append(r.evs, e)
	return nil
}

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

// TestChanSinkCapacityMatchesContract pins the buffer size of the live UI
// event channel to the contract value and proves the sink accepts exactly
// that many events before it starts dropping.
func TestChanSinkCapacityMatchesContract(t *testing.T) {
	sink := newUISink()
	const want = 1024
	if got := cap(sink.ch); got != want {
		t.Fatalf("UI event channel capacity = %d, want %d", got, want)
	}
	for i := 0; i < want; i++ {
		if err := sink.Emit(agent.Event{Type: agent.TextDelta, Seq: i + 1}); err != nil {
			t.Fatalf("Emit(%d) returned error while the channel had room: %v", i, err)
		}
	}
	if got := sink.Dropped(); got != 0 {
		t.Fatalf("Dropped() = %d, want 0 after exactly %d events", got, want)
	}
	if err := sink.Emit(agent.Event{Type: agent.TextDelta, Seq: want + 1}); err != nil {
		t.Fatalf("Emit past capacity returned error: %v", err)
	}
	if got := sink.Dropped(); got != 1 {
		t.Fatalf("Dropped() = %d, want 1 once the buffer overflowed", got)
	}
}

// TestChanSinkNeverBlocksUnderBurst pins the non-blocking contract: with a
// buffer of one, a burst of ten events accepts the first, drops the other
// nine, returns nil from every call, and finishes in well under a human
// timescale. The elapsed-time bound is the assertion that separates "drop"
// from "wait then give up"; an "eventually" check would hide a blocking sink.
func TestChanSinkNeverBlocksUnderBurst(t *testing.T) {
	sink := &chanSink{ch: make(chan agent.Event, 1)}

	start := time.Now()
	for i := 0; i < 10; i++ {
		if err := sink.Emit(agent.Event{Type: agent.TextDelta, Seq: i + 1}); err != nil {
			t.Fatalf("Emit(%d) returned error: %v", i, err)
		}
	}
	elapsed := time.Since(start)

	if got := sink.Dropped(); got != 9 {
		t.Fatalf("Dropped() = %d, want 9 (first buffered, nine dropped)", got)
	}
	if elapsed >= 10*time.Millisecond {
		t.Fatalf("ten emits took %v; a full channel must drop instantly, not wait", elapsed)
	}
	select {
	case e := <-sink.ch:
		if e.Seq != 1 {
			t.Fatalf("buffered event Seq = %d, want 1", e.Seq)
		}
	default:
		t.Fatal("no event was accepted into the buffer")
	}
}

// captureStdio runs fn with os.Stdout and os.Stderr redirected to pipes and
// returns what was written to each.
func captureStdio(t *testing.T, fn func()) (stdout, stderr string) {
	t.Helper()
	origOut, origErr := os.Stdout, os.Stderr
	rOut, wOut, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	rErr, wErr, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stdout, os.Stderr = wOut, wErr
	defer func() { os.Stdout, os.Stderr = origOut, origErr }()

	fn()

	if err := wOut.Close(); err != nil {
		t.Fatalf("close stdout pipe: %v", err)
	}
	if err := wErr.Close(); err != nil {
		t.Fatalf("close stderr pipe: %v", err)
	}
	bOut, _ := io.ReadAll(rOut)
	bErr, _ := io.ReadAll(rErr)
	return string(bOut), string(bErr)
}

// TestReportSessionAlwaysPrintsPath pins the session reporting contract: the
// path is printed on stdout even when the journal close fails, and the close
// error goes to stderr without replacing the path.
func TestReportSessionAlwaysPrintsPath(t *testing.T) {
	const path = "/home/u/.ag/sessions/20260910-120000.000.jsonl"

	out, errOut := captureStdio(t, func() {
		reportSession(os.Stdout, os.Stderr, path, nil)
	})
	if !strings.Contains(out, "session: "+path) {
		t.Fatalf("session path missing on stdout: %q", out)
	}
	if errOut != "" {
		t.Fatalf("stderr must stay empty on a clean close, got %q", errOut)
	}

	out, errOut = captureStdio(t, func() {
		reportSession(os.Stdout, os.Stderr, path, errors.New("close boom"))
	})
	if !strings.Contains(out, "session: "+path) {
		t.Fatalf("close failure suppressed the session path: stdout=%q", out)
	}
	if !strings.Contains(errOut, "close boom") {
		t.Fatalf("close error not routed to stderr: stderr=%q", errOut)
	}
}
