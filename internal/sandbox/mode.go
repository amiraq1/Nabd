package sandbox

import (
	"fmt"
	"strings"
)

// Mode controls whether Bash should use the filesystem sandbox.
//
// Auto preserves the compatibility fallback for kernels and platforms that
// do not provide Landlock. On is fail-closed: Bash is refused unless the
// helper and Landlock are both available. Off is an explicit operator
// override that keeps the pre-Landlock behavior.
type Mode uint8

const (
	ModeAuto Mode = iota
	ModeOn
	ModeOff
)

// ParseMode parses NABD_BASH_SANDBOX. Empty is the safe compatibility default.
func ParseMode(raw string) (Mode, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", "auto":
		return ModeAuto, nil
	case "on":
		return ModeOn, nil
	case "off":
		return ModeOff, nil
	default:
		return ModeAuto, fmt.Errorf("NABD_BASH_SANDBOX must be one of: auto, on, off")
	}
}

// UseHelper decides whether Bash may be launched through the sandbox helper.
// In auto mode, unavailable isolation retains the documented compatibility
// fallback. In on mode, the same condition is a hard refusal.
func UseHelper(mode Mode, helperAvailable, landlockAvailable bool) (bool, error) {
	if mode == ModeOff {
		return false, nil
	}
	if helperAvailable && landlockAvailable {
		return true, nil
	}
	if mode == ModeOn {
		return false, fmt.Errorf("bash sandbox is required but unavailable: %w", ErrUnavailable)
	}
	return false, nil
}
