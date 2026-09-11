package provider

import (
	"context"
	"errors"
	"net/http"
)

// ErrorKind is the provider layer's structured classification. User-facing
// codes live in agent; this type prevents presentation code from parsing text.
type ErrorKind string

const (
	ErrorKindUnknown   ErrorKind = "unknown"
	ErrorKindTemporary ErrorKind = "temporary"
	ErrorKindAuth      ErrorKind = "auth"
)

// ErrorKindOf classifies provider errors using typed/status data only.
func ErrorKindOf(err error) ErrorKind {
	if err == nil {
		return ErrorKindUnknown
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, ErrRouteCleanupTimeout) || errors.Is(err, ErrRouterExhausted) {
		return ErrorKindTemporary
	}
	var exhausted *RouterExhaustedError
	if errors.As(err, &exhausted) {
		for _, attempt := range exhausted.Attempts {
			if attempt.Status == http.StatusUnauthorized || attempt.Status == http.StatusForbidden {
				return ErrorKindAuth
			}
		}
		return ErrorKindTemporary
	}
	var httpErr *httpError
	if errors.As(err, &httpErr) {
		switch httpErr.Status {
		case http.StatusUnauthorized, http.StatusForbidden:
			return ErrorKindAuth
		case http.StatusRequestTimeout, http.StatusConflict, http.StatusTooManyRequests, http.StatusGone:
			return ErrorKindTemporary
		}
		if httpErr.Status >= 500 {
			return ErrorKindTemporary
		}
	}
	var tpm *TPMError
	if errors.As(err, &tpm) {
		return ErrorKindTemporary
	}
	return ErrorKindUnknown
}
