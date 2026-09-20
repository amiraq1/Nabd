//go:build !linux

package sandbox

import "errors"

var ErrUnavailable = errors.New("landlock sandbox is unavailable on this platform")
var ErrNetworkUnavailable = errors.New("landlock network restrictions are unavailable on this platform")

func Available() bool       { return false }
func SupportsNetwork() bool { return false }

func Apply(Config) error { return ErrUnavailable }

func Run([]string) int { return 1 }
