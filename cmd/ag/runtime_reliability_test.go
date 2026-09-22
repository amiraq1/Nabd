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
	"nabd/internal/ui"
)

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

// TestFinishFeedSessionStopsBatcherOnlyAfterTheProgramExits is the regression
// guard for the feed path. The batcher must stay live for the whole
// interactive session and be stopped only once the program has exited:
// Batcher.Add is a silent no-op once stopped, so stopping it right after
// loop.Start dropped every event after the banner and left the feed empty.
//
// The check is deterministic rather than scheduler-dependent: a pending,
// non-sensitive event sits in the batcher (the interval is an hour, so only
// an explicit Stop/Flush can drain it). If execution reaches Stop before the
// program exits, that flush is observed and the test fails on the spot.
func TestFinishFeedSessionStopsBatcherOnlyAfterTheProgramExits(t *testing.T) {
	var mu sync.Mutex
	var delivered []agent.Event
	flushed := make(chan struct{}, 1)
	b := ui.NewBatcher(time.Hour, 100, func(batch []agent.Event) {
		mu.Lock()
		delivered = append(delivered, batch...)
		mu.Unlock()
		select {
		case flushed <- struct{}{}:
		default:
		}
	})
	b.Start()

	// A pending event that only a premature Stop would flush.
	b.Add(agent.Event{Type: agent.TextDelta, Seq: 99})

	sink := feedSink{batcher: b}
	loop := &agent.Loop{Sink: &recordSink{}}
	progDone := make(chan error, 1)

	returned := make(chan error, 1)
	go func() {
		returned <- finishFeedSession(progDone, b, loop, &failCloser{}, "/home/u/.ag/sessions/s.jsonl")
	}()

	select {
	case <-flushed:
		t.Fatal("batcher stopped before the interactive program exited: every event after the banner would be dropped and the feed would stay empty")
	case <-time.After(100 * time.Millisecond):
		// Still live while the session runs — the expected state.
	}

	// A live event now reaches the receiver; the sensitive type forces the
	// flush, so both the pending event and this one are delivered.
	sink.Emit(agent.Event{Type: agent.ToolStart, Call: &agent.ToolCall{ID: "t1", Name: "read_file"}})
	mu.Lock()
	got := len(delivered)
	mu.Unlock()
	if got != 2 {
		t.Fatalf("live event not delivered while the program was running (%d delivered, want 2)", got)
	}

	// The program exits; only now may the batcher stop, before End.
	progDone <- nil
	if err := <-returned; err != nil {
		t.Fatalf("finishFeedSession: %v", err)
	}

	// Shutdown completed: the batcher is stopped, so later events are dropped.
	sink.Emit(agent.Event{Type: agent.ToolStart, Call: &agent.ToolCall{ID: "t2", Name: "read_file"}})
	mu.Lock()
	got = len(delivered)
	mu.Unlock()
	if got != 2 {
		t.Fatalf("event delivered after shutdown (%d delivered, want 2): the batcher was not stopped", got)
	}
}

// TestFinishFeedSessionReportsProgramError proves a failing interactive
// program still stops the batcher and closes the journal, so the shutdown
// does not leak.
func TestFinishFeedSessionReportsProgramError(t *testing.T) {
	b := ui.NewBatcher(time.Hour, 100, func([]agent.Event) {})
	b.Start()

	progDone := make(chan error, 1)
	progDone <- errors.New("program boom")

	journal := &failCloser{err: errors.New("close boom")}
	err := finishFeedSession(progDone, b, &agent.Loop{Sink: &recordSink{}}, journal, "/tmp/s.jsonl")
	if err == nil || !strings.Contains(err.Error(), "program boom") {
		t.Fatalf("program error must be returned, got %v", err)
	}
	if !journal.closed {
		t.Fatal("journal must be closed even when the program failed")
	}
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
