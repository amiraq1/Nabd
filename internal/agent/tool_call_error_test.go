package agent

import (
	"context"
	"errors"
	"testing"

	"nabd/internal/provider"
)

// recordingSink lets a test fail one specific event without naming the event
// type's Go type, so the test stays valid if that type is renamed.
type recordingSink struct {
	fail   func(Event) error
	seen   []Event
	failed error
}

func (s *recordingSink) Emit(e Event) error {
	s.seen = append(s.seen, e)
	if s.fail == nil {
		return nil
	}
	if err := s.fail(e); err != nil {
		s.failed = err
		return err
	}
	return nil
}

func TestWrapToolCallErrorKeepsTheMessage(t *testing.T) {
	inner := &PersistError{Path: "/tmp/session.jsonl", Err: errors.New("no space left on device")}
	wrapped := WrapToolCallError(ToolCall{ID: "call-1", Name: "write_file"}, inner)

	if wrapped.Error() != inner.Error() {
		t.Fatalf("attribution must not change the message\n got: %q\nwant: %q", wrapped.Error(), inner.Error())
	}
}

func TestWrapToolCallErrorStaysTransparent(t *testing.T) {
	inner := &PersistError{Path: "/tmp/session.jsonl", Err: errors.New("no space left on device")}
	wrapped := WrapToolCallError(ToolCall{ID: "call-1", Name: "write_file"}, inner)

	if code := ErrorCodeOf(wrapped); code != ErrCodePersist {
		t.Fatalf("wrapped persist error classified as %q, want %q", code, ErrCodePersist)
	}
	if path := JournalPathOf(wrapped); path != "/tmp/session.jsonl" {
		t.Fatalf("journal path lost through the wrapper: %q", path)
	}
	if !errors.Is(WrapToolCallError(ToolCall{ID: "c", Name: "bash"}, ErrMaxTurns), ErrMaxTurns) {
		t.Fatal("errors.Is must see through the wrapper")
	}
}

func TestWrapToolCallErrorKeepsTheInnermostCall(t *testing.T) {
	first := WrapToolCallError(ToolCall{ID: "inner", Name: "read_file"}, errors.New("boom"))
	second := WrapToolCallError(ToolCall{ID: "outer", Name: "bash"}, first)

	id, name, ok := ToolCallOf(second)
	if !ok {
		t.Fatal("attribution was lost")
	}
	if id != "inner" || name != "read_file" {
		t.Fatalf("outer frame overwrote the failing call: got %q/%q", id, name)
	}
}

func TestWrapToolCallErrorIsANoOpWhenThereIsNothingToSay(t *testing.T) {
	if err := WrapToolCallError(ToolCall{ID: "c", Name: "bash"}, nil); err != nil {
		t.Fatalf("nil error must stay nil, got %v", err)
	}

	bare := errors.New("boom")
	if got := WrapToolCallError(ToolCall{}, bare); got != bare {
		t.Fatalf("a call with no identity must be returned unchanged, got %v", got)
	}
	if _, _, ok := ToolCallOf(bare); ok {
		t.Fatal("an unattributed error must report no call")
	}
}

func TestRunErrorEventNamesTheFailingCall(t *testing.T) {
	inner := &PersistError{Path: "/tmp/session.jsonl", Err: errors.New("no space left on device")}
	e := RunErrorEvent(WrapToolCallError(ToolCall{ID: "call-7", Name: "write_file"}, inner))

	if e.Call == nil {
		t.Fatal("run_error must report the call that was in flight")
	}
	if e.Call.ID != "call-7" || e.Call.Name != "write_file" {
		t.Fatalf("wrong call reported: %q/%q", e.Call.ID, e.Call.Name)
	}
	if e.ErrorCode != string(ErrCodePersist) {
		t.Fatalf("error code lost: %q", e.ErrorCode)
	}
	if e.Err != inner.Error() {
		t.Fatalf("message changed: %q", e.Err)
	}
}

func TestRunErrorEventReportsNoResultForTheFailingCall(t *testing.T) {
	e := RunErrorEvent(WrapToolCallError(
		ToolCall{ID: "call-7", Name: "write_file", Output: "wrote 12 bytes", OK: true, Exit: 0, MS: 42},
		errors.New("boom"),
	))

	if e.Call == nil {
		t.Fatal("run_error must report the call that was in flight")
	}
	if e.Call.Output != "" || e.Call.OK || e.Call.MS != 0 {
		t.Fatalf("only the identity may travel, got %+v", *e.Call)
	}
}

func TestRunErrorEventStaysQuietWhenNoCallIsKnown(t *testing.T) {
	if e := RunErrorEvent(ErrMaxTurns); e.Call != nil {
		t.Fatalf("an unattributed failure must not invent a call: %+v", *e.Call)
	}
}

func TestSinkFailureNamesTheCallInFlight(t *testing.T) {
	boom := errors.New("journal is full")
	sink := &recordingSink{fail: func(e Event) error {
		if e.Type == ToolStart {
			return boom
		}
		return nil
	}}
	l := &Loop{Sink: sink}

	_, err := l.runCalls(context.Background(), []provider.ToolCall{{ID: "call-3", Name: "read_file"}})
	if err == nil {
		t.Fatal("a sink failure must stop the batch")
	}
	// emit already wraps a sink failure in a *PersistError; the attribution
	// wrapper must leave that message byte-identical and stay transparent to
	// the underlying sink failure.
	want := NewPersistError(boom, "").Error()
	if err.Error() != want {
		t.Fatalf("message changed: got %q, want %q", err.Error(), want)
	}
	if !errors.Is(err, boom) {
		t.Fatal("the wrapper must stay transparent to the sink failure")
	}

	id, name, ok := ToolCallOf(err)
	if !ok {
		t.Fatal("the loop knows which call it was running and must say so")
	}
	if id != "call-3" || name != "read_file" {
		t.Fatalf("wrong call attributed: %q/%q", id, name)
	}
}
