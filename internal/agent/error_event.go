package agent

import (
	"context"
	"errors"
	"fmt"

	"nabd/internal/provider"
)

// ErrorCode is the single machine-readable error vocabulary shared by the
// journal, presentation projector, and UI.
type ErrorCode string

const (
	ErrProviderTemporary ErrorCode = "provider_temporary"
	ErrProviderAuth      ErrorCode = "provider_auth"
	ErrPersist           ErrorCode = "persist"
	ErrBudget            ErrorCode = "budget"
	ErrMaxTurnsCode       ErrorCode = "max_turns"
	ErrCanceled          ErrorCode = "canceled"
	ErrUnknown           ErrorCode = "unknown"
)

// PersistError marks a journal/sink failure. It is returned without mutating
// Loop history, so callers can enter safe-stop instead of reporting success.
type PersistError struct {
	Path string
	Err  error
}

func (e *PersistError) Error() string {
	if e.Path == "" {
		return fmt.Sprintf("session event was not saved: %v", e.Err)
	}
	return fmt.Sprintf("session event was not saved (%s): %v", e.Path, e.Err)
}
func (e *PersistError) Unwrap() error { return e.Err }

func NewPersistError(err error, path string) error {
	if err == nil {
		return nil
	}
	var existing *PersistError
	if errors.As(err, &existing) {
		return err
	}
	return &PersistError{Path: path, Err: err}
}

func JournalPathOf(err error) string {
	var persist *PersistError
	if errors.As(err, &persist) {
		return persist.Path
	}
	return ""
}

// ErrorCodeOf classifies errors from typed values only. It never parses error
// strings, HTTP text, or provider messages.
func ErrorCodeOf(err error) ErrorCode {
	switch {
	case err == nil:
		return ErrUnknown
	case errors.As(err, new(*PersistError)):
		return ErrPersist
	case errors.Is(err, ErrSpendBudget):
		return ErrBudget
	case errors.Is(err, ErrMaxTurns):
		return ErrMaxTurnsCode
	case errors.Is(err, context.Canceled):
		return ErrCanceled
	case errors.Is(err, ErrRateLimitBudget):
		return ErrProviderTemporary
	}
	switch provider.ErrorKindOf(err) {
	case provider.ErrorKindAuth:
		return ErrProviderAuth
	case provider.ErrorKindTemporary:
		return ErrProviderTemporary
	default:
		return ErrUnknown
	}
}

func RunErrorEvent(err error) Event {
	if err == nil {
		err = errors.New("unknown run error")
	}
	return Event{Type: RunError, Err: err.Error(), ErrorCode: string(ErrorCodeOf(err)), JournalPath: JournalPathOf(err)}
}
