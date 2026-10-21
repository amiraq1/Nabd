package event

import (
	"context"
	"errors"
	"fmt"
)

// ErrorCode is the single machine-readable error vocabulary shared by the
// journal, presentation projector, and UI.
type ErrorCode string

const (
	ErrCodeProviderTemporary ErrorCode = "provider_temporary"
	ErrCodeProviderAuth      ErrorCode = "provider_auth"
	ErrCodePersist           ErrorCode = "persist"
	ErrCodeBudget            ErrorCode = "budget"
	ErrCodeMaxTurns          ErrorCode = "max_turns"
	ErrCodeCanceled          ErrorCode = "canceled"
	ErrCodeLoopDetected      ErrorCode = "loop_detected"
	ErrCodeEndpointRefused   ErrorCode = "endpoint_refused"
	ErrCodeUnknown           ErrorCode = "unknown"
)

// RemedyEndpointRefused is the canonical guidance string when an endpoint is refused by policy.
const RemedyEndpointRefused = "set NABD_ENDPOINT_POLICY=loopback for a local proxy, or use an https endpoint"

// PersistError marks a journal/sink failure. It is returned without mutating
// loop history, so callers can enter safe-stop instead of reporting success.
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

// The sentinel errors below are matched by ErrorCodeOf. They are defined
// here — next to the classifier — rather than in the agent coordinator so
// the error-code contract is whole in one leaf package; the coordinator
// raises them, it does not own their identities.
var (
	ErrSpendBudget     = errors.New("run token spend budget exhausted")
	ErrMaxTurns        = errors.New("turn ceiling reached")
	ErrToolLoop        = errors.New("tool execution loop detected: identical call repeated past threshold")
	ErrRateLimitBudget = errors.New("rate limit budget exhausted")
)

// ProviderErrorKind mirrors the provider layer's error-kind vocabulary
// without importing it, so the classifier below stays in this leaf package.
// The string values must match provider.ErrorKind exactly; the agent package
// converts by value and TestProviderKindValuesPinned guards the mapping.
type ProviderErrorKind string

const (
	ProviderErrorKindUnknown         ProviderErrorKind = "unknown"
	ProviderErrorKindTemporary       ProviderErrorKind = "temporary"
	ProviderErrorKindAuth            ProviderErrorKind = "auth"
	ProviderErrorKindEndpointRefused ProviderErrorKind = "endpoint_refused"
)

// ErrorCodeOf classifies errors from typed values only. It never parses error
// strings, HTTP text, or provider messages. providerKind carries the provider
// layer's classification; callers without a provider pass
// ProviderErrorKindUnknown.
func ErrorCodeOf(err error, providerKind ProviderErrorKind) ErrorCode {
	switch {
	case err == nil:
		return ErrCodeUnknown
	case errors.As(err, new(*PersistError)):
		return ErrCodePersist
	case errors.Is(err, ErrSpendBudget):
		return ErrCodeBudget
	case errors.Is(err, ErrMaxTurns):
		return ErrCodeMaxTurns
	case errors.Is(err, ErrToolLoop):
		return ErrCodeLoopDetected
	case errors.Is(err, context.Canceled):
		return ErrCodeCanceled
	case errors.Is(err, ErrRateLimitBudget):
		return ErrCodeProviderTemporary
	}
	switch providerKind {
	case ProviderErrorKindAuth:
		return ErrCodeProviderAuth
	case ProviderErrorKindTemporary:
		return ErrCodeProviderTemporary
	case ProviderErrorKindEndpointRefused:
		return ErrCodeEndpointRefused
	default:
		return ErrCodeUnknown
	}
}
