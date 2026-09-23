//go:build !android

package endpoint

import "net"

func configureDialerResolver(d *net.Dialer) {
	// No-op on non-Android platforms.
}

func resetTermuxResolverForTest() {
	// No-op on non-Android platforms.
}
