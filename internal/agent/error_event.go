package agent

import (
	"errors"

	"nabd/internal/event"
	"nabd/internal/provider"
)

// ErrorCodeOf classifies errors from typed values only. It never parses error
// strings, HTTP text, or provider messages.
//
// The classifier itself lives in the event package so storage and
// presentation do not import the coordinator; this wrapper only supplies the
// provider layer's error kind.
func ErrorCodeOf(err error) event.ErrorCode {
	return event.ErrorCodeOf(err, event.ProviderErrorKind(provider.ErrorKindOf(err)))
}

// RunErrorEvent preserves a machine-readable error code while keeping legacy
// journals valid. Presentation must use ErrorCode and treat an empty value as
// unknown rather than guessing from the message text.
//
// When the error carries tool-call attribution, it is reported through the
// event's existing Call field rather than a new one: the journal already
// describes a tool call that way on ToolStart, ToolEnd, PermAsk, and PermReply,
// so a reader and every existing decoder already know how to read it. Only the
// identity is copied — no output, no arguments, no exit status — because the
// failing call produced no result to report.
func RunErrorEvent(err error) event.Event {
	if err == nil {
		err = errors.New("unknown run error")
	}
	e := event.Event{Type: event.RunError, Err: err.Error(), ErrorCode: string(ErrorCodeOf(err)), JournalPath: event.JournalPathOf(err)}
	// Preserve the router's structured retry-after as a first-class field rather
	// than leaving it inside the free-text error string. The error card hides its
	// details line below 40 columns and truncates it to the terminal width above
	// that, so a number that lives only in the message disappears exactly where
	// it is needed. If the run-level error is a RouterExhaustedError, its shortest
	// positive retry-after is the one piece of information the user acts on, so
	// it travels to the card as a field.
	var ree *provider.RouterExhaustedError
	if errors.As(err, &ree) && ree.RetryAfter > 0 {
		e.RetryAfter = ree.RetryAfter.Seconds()
	}
	if id, name, ok := ToolCallOf(err); ok {
		e.Call = &event.ToolCall{ID: id, Name: name}
	}
	return e
}

func sinkJournalPath(s Sink) string {
	if p, ok := s.(interface{ JournalPath() string }); ok {
		return p.JournalPath()
	}
	if f, ok := s.(Fanout); ok {
		for _, child := range f {
			if path := sinkJournalPath(child); path != "" {
				return path
			}
		}
	}
	return ""
}
