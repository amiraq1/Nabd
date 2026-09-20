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

// NetworkMode controls TCP networking for a Bash child.
type NetworkMode uint8

const (
	NetworkAllow NetworkMode = iota
	NetworkDeny
)

// ResourceMode controls inherited POSIX resource limits for a Bash child.
type ResourceMode uint8

const (
	ResourcesAllow ResourceMode = iota
	ResourcesLimit
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

// ParseNetworkMode parses NABD_BASH_NETWORK. Empty is the compatibility
// default because approved Bash commands may legitimately need networking.
func ParseNetworkMode(raw string) (NetworkMode, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", "allow":
		return NetworkAllow, nil
	case "deny":
		return NetworkDeny, nil
	default:
		return NetworkAllow, fmt.Errorf("NABD_BASH_NETWORK must be one of: allow, deny")
	}
}

// ParseResourceMode parses NABD_BASH_RESOURCES. Empty keeps the compatibility
// default because existing builds may legitimately need more memory or files.
func ParseResourceMode(raw string) (ResourceMode, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", "allow":
		return ResourcesAllow, nil
	case "limit":
		return ResourcesLimit, nil
	default:
		return ResourcesAllow, fmt.Errorf("NABD_BASH_RESOURCES must be one of: allow, limit")
	}
}

// UseHelper decides whether Bash may be launched through the sandbox helper.
// In auto mode, unavailable isolation retains the documented compatibility
// fallback. In on mode, the same condition is a hard refusal.
func UseHelper(mode Mode, helperAvailable, landlockAvailable bool) (bool, error) {
	return Select(mode, NetworkAllow, ResourcesAllow, helperAvailable, landlockAvailable, false, false)
}

// Select decides whether Bash may use the helper for the requested policies.
// Network denial and resource limits are fail-closed even when filesystem mode
// is auto or off: silently allowing a requested boundary would be misleading.
func Select(mode Mode, network NetworkMode, resources ResourceMode, helperAvailable, landlockAvailable, networkAvailable, resourcesAvailable bool) (bool, error) {
	if network == NetworkDeny &&
		(mode == ModeOff || !helperAvailable || !networkAvailable) {
		return false, fmt.Errorf("bash network denial requires Landlock network support: %w", ErrNetworkUnavailable)
	}
	if resources == ResourcesLimit &&
		(mode == ModeOff || !helperAvailable || !resourcesAvailable) {
		return false, fmt.Errorf("bash resource limits require the sandbox helper and Linux support: %w", ErrResourcesUnavailable)
	}
	if mode == ModeOff {
		return false, nil
	}
	if helperAvailable && landlockAvailable {
		return true, nil
	}
	if mode == ModeOn {
		return false, fmt.Errorf("bash sandbox is required but unavailable: %w", ErrUnavailable)
	}
	if network == NetworkDeny {
		return false, fmt.Errorf("bash network denial requires filesystem sandbox support: %w", ErrUnavailable)
	}
	if resources == ResourcesLimit {
		return false, fmt.Errorf("bash resource limits require filesystem sandbox support: %w", ErrUnavailable)
	}
	return false, nil
}
