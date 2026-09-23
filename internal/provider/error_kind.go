package provider

import (
	"context"
	"errors"
	"net/http"

	"nabd/internal/endpoint"
)

// ErrorKind is the provider layer's structured classification. User-facing
// codes live in agent; this type prevents presentation code from parsing text.
type ErrorKind string

const (
	ErrorKindUnknown         ErrorKind = "unknown"
	ErrorKindTemporary       ErrorKind = "temporary"
	ErrorKindAuth            ErrorKind = "auth"
	ErrorKindEndpointRefused ErrorKind = "endpoint_refused"
)

// ClassifyHTTPStatus maps an HTTP status onto the provider error kind, using
// the same table ErrorKindOf applies to provider responses. Callers outside the
// request path (for example the `models` probe) use it so a status is read one
// way, not two.
func ClassifyHTTPStatus(status int) ErrorKind {
	switch status {
	case http.StatusUnauthorized, http.StatusForbidden:
		return ErrorKindAuth
	case http.StatusRequestTimeout, http.StatusConflict, http.StatusTooManyRequests, http.StatusGone:
		return ErrorKindTemporary
	}
	if status >= 500 {
		return ErrorKindTemporary
	}
	return ErrorKindUnknown
}

// Kinded is implemented by an error that already carries its provider error
// kind. Errors raised outside the request path (for example the /models probe
// in internal/providercmd) implement it so provider.ErrorKindOf — and therefore
// agent.ErrorCodeOf — classifies them without a second, local switch that can
// drift and silently drop a kind such as endpoint_refused.
type Kinded interface{ ErrorKind() ErrorKind }

// ErrorKindOf classifies an error into the provider vocabulary. It is the single
// classifier for provider errors, consulted by agent.ErrorCodeOf.
func ErrorKindOf(err error) ErrorKind {
	if err == nil {
		return ErrorKindUnknown
	}
	if errors.Is(err, endpoint.ErrEndpointRefused) {
		return ErrorKindEndpointRefused
	}
	var kinded Kinded
	if errors.As(err, &kinded) {
		return kinded.ErrorKind()
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
		return ClassifyHTTPStatus(httpErr.Status)
	}
	var tpm *TPMError
	if errors.As(err, &tpm) {
		return ErrorKindTemporary
	}
	return ErrorKindUnknown
}
