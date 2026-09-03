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

	l.emitAt(cut.Parent, Event{
		Type: Rewind,
		Text: fmt.Sprintf("rewound %d turns (%d events)", n, dropped),
	})
	return cut.Text, nil
}

// emitAt is emit with the parent chosen by the caller instead of by time.
// It is the whole mechanism: seq still grows, parent walks back. A sink
// failure is returned so callers can stop the loop instead of continuing
// as if the event was durably recorded.
func (l *Loop) emitAt(parent int, e Event) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.seq++
	e.Seq, e.Parent = l.seq, parent
	if e.Time.IsZero() {
		e.Time = l.clockNowUTC()
	}
	l.parent = e.Seq
	l.hist = append(l.hist, e)
	if l.Sink != nil {
		return l.Sink.Emit(e)
	}
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
