package agent

import "errors"

// ToolCallError attributes a failure to the tool call that was in flight when
// it happened.
//
// The loop already knows which call it was running when a sink write failed,
// but that knowledge used to die at the return statement: `runCalls` handed
// back a bare error, `RunErrorEvent` recorded only its message and code, and
// the reader was told "session event was not saved" with no way to learn
// whether the interrupted call was a harmless read or a `write_file` whose
// result never reached the journal. That distinction decides whether the tree
// on disk can still be trusted, so it belongs in the event.
//
// The wrapper deliberately does not decorate the message. Several tests and
// the rendered failure block assert exact error text, and a prefix here would
// change what the user reads for every persistence failure while adding
// nothing they could act on. Attribution travels as fields; the message stays
// the message.
type ToolCallError struct {
	CallID   string
	ToolName string
	Err      error
}

// Error returns the wrapped message unchanged.
func (e *ToolCallError) Error() string {
	if e == nil || e.Err == nil {
		return ""
	}
	return e.Err.Error()
}

// Unwrap keeps errors.Is and errors.As transparent, so ErrorCodeOf still
// classifies a wrapped *PersistError as persist rather than unknown.
func (e *ToolCallError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

// WrapToolCallError attributes err to c. It is a no-op for a nil error, for a
// call with no identity to report, and for an error that already carries
// attribution — the innermost call is the one that failed, so an outer frame
// must not overwrite it.
func WrapToolCallError(c ToolCall, err error) error {
	if err == nil {
		return nil
	}
	if c.ID == "" && c.Name == "" {
		return err
	}
	if errors.As(err, new(*ToolCallError)) {
		return err
	}
	return &ToolCallError{CallID: c.ID, ToolName: c.Name, Err: err}
}

// ToolCallOf reports the attributed call, if any. Callers must treat a false
// result as "not attributable" rather than guessing from the message text.
func ToolCallOf(err error) (callID, toolName string, ok bool) {
	var attributed *ToolCallError
	if errors.As(err, &attributed) {
		return attributed.CallID, attributed.ToolName, true
	}
	return "", "", false
}
