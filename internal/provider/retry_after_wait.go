package provider

import (
	"fmt"
	"strconv"
	"strings"
)

// Retry-After wait budget bounds for NABD_ROUTER_RETRY_AFTER_WAIT.
//
// The budget answers a single question: when every route has failed and the
// provider told us exactly how long to wait, how long is the router allowed to
// honor that instruction before giving up? Zero — the default — preserves the
// historical behavior of reporting exhaustion immediately.
const (
	// DefaultRetryAfterWaitSec disables the wait unless explicitly configured.
	DefaultRetryAfterWaitSec = 0
	// MinRetryAfterWaitSec is the lowest accepted value (0 = disabled).
	MinRetryAfterWaitSec = 0
	// MaxRetryAfterWaitSec matches the agent's own retry ceiling (120s).
	MaxRetryAfterWaitSec = 120
)

// ParseRetryAfterWait parses NABD_ROUTER_RETRY_AFTER_WAIT into whole seconds.
// An empty value yields the default (disabled). Anything outside
// [MinRetryAfterWaitSec, MaxRetryAfterWaitSec] is rejected rather than clamped,
// so a typo in the config is reported instead of silently reinterpreted.
func ParseRetryAfterWait(raw string) (int, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return DefaultRetryAfterWaitSec, nil
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0, fmt.Errorf("NABD_ROUTER_RETRY_AFTER_WAIT must be a whole number of seconds")
	}
	if n < MinRetryAfterWaitSec || n > MaxRetryAfterWaitSec {
		return 0, fmt.Errorf("NABD_ROUTER_RETRY_AFTER_WAIT must be between %d and %d seconds",
			MinRetryAfterWaitSec, MaxRetryAfterWaitSec)
	}
	return n, nil
}
