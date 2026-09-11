package agent

import (
	"context"
	"errors"
)

// RunErrorEvent preserves a machine-readable error code while keeping legacy
// journals valid. Presentation must use ErrorCode and treat an empty value as
// unknown rather than guessing from the message text.
func RunErrorEvent(err error) Event {
	code := "unknown"
	switch {
	case errors.Is(err, ErrSpendBudget), errors.Is(err, ErrRateLimitBudget):
		code = "budget"
	case errors.Is(err, context.Canceled):
		code = "canceled"
	}
	return Event{Type: RunError, Err: err.Error(), ErrorCode: code}
}
