// Package agent: rewind.go cuts the conversation by appending, never by
// deleting. One entry whose parent points backwards makes the branch after
// it unreachable to Live() while leaving it on disk, readable forever.
package agent

import (
	"errors"
	"fmt"
	"time"
)

// Rewind drops the last n user turns. It returns the text of the turn the
// human is now free to retype, so a rewind is a correction, not a loss.
func (l *Loop) Rewind(n int) (string, error) {
	if !l.historyMu.TryLock() {
		return "", ErrHistoryMutationInProgress
	}
	defer l.historyMu.Unlock()

	if n < 1 {
		n = 1
	}
	l.mu.Lock()
	live := Live(l.hist)
	l.mu.Unlock()

	var idx []int
	for i, e := range live {
		if e.Type == UserMsg {
			idx = append(idx, i)
		}
	}
	if len(idx) == 0 {
		return "", errors.New("no turns to rewind")
	}
	if n > len(idx) {
		n = len(idx)
	}
	cut := live[idx[len(idx)-n]] // the user message that dies, and all after it
	dropped := len(live) - idx[len(idx)-n]

	if err := l.emitAt(cut.Parent, Event{
		Type: Rewind,
		Text: fmt.Sprintf("rewound %d turns (%d events)", n, dropped),
	}); err != nil {
		return "", err
	}
	return cut.Text, nil
}

// emitAt acquires l.mu and delegates to emitLocked, allowing the caller
// to choose the parent event instead of using the current parent. A sink
// failure is returned so callers can stop the loop instead of continuing
// as if the event was durably recorded.
func (l *Loop) emitAt(parent int, e Event) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.emitLocked(parent, e)
}

// emitLocked stamps and appends an event. The caller must already hold l.mu.
// Use inside a critical section where an invariant must be validated and the
// event appended atomically.
//
// WARNING: l.Sink.Emit is invoked while l.mu remains held. A Sink that calls
// back into the same Loop (e.g. calling emit or emitAt) will deadlock
// (pre-existing hazard noted in NOTES.md P0-1.5).
func (l *Loop) emitLocked(parent int, e Event) error {
	nextSeq := l.seq + 1
	e.Seq, e.Parent = nextSeq, parent
	if e.Time.IsZero() {
		e.Time = l.clockNowUTC()
	}
	if l.Sink != nil {
		if err := l.Sink.Emit(e); err != nil {
			return NewPersistError(err, sinkJournalPath(l.Sink))
		}
	}
	l.seq = nextSeq
	l.parent = e.Seq
	l.hist = append(l.hist, e)
	return nil
}

// clockNowUTC mirrors Loop.clockNow but is usable here without embedding.
// It returns time.Now().UTC() unless a fake clock is injected via l.now.
func (l *Loop) clockNowUTC() time.Time {
	if l.now != nil {
		return l.now().UTC()
	}
	return time.Now().UTC()
}
