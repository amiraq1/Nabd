// Package ui: approve.go carries one human answer back to the loop
// goroutine that is blocked on it. Nothing else crosses this channel.
package ui

import (
	"context"

	"nabd/internal/event"
)

type Approver struct{ reply chan event.Decision }

func NewApprover() *Approver { return &Approver{reply: make(chan event.Decision, 1)} }

// Ask blocks until the keyboard answers or the context dies.
// A dead context is a refusal.
func (a *Approver) Ask(ctx context.Context, c event.ToolCall) event.Decision {
	select { // drop an answer left over from an interrupted turn
	case <-a.reply:
	default:
	}
	select {
	case d := <-a.reply:
		return d
	case <-ctx.Done():
		return event.Deny
	}
}

// Reply is called from the UI goroutine and never blocks: an answer with
// no question behind it is dropped, not queued for the next one.
func (a *Approver) Reply(d event.Decision) {
	select {
	case a.reply <- d:
	default:
	}
}
