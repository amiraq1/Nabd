//go:build android

package endpoint

import (
	"context"
	"net"
	"sync"
	"sync/atomic"
)

var (
	termuxResolverOnce sync.Once
	termuxResolver     *net.Resolver
)

// getTermuxResolver returns a *net.Resolver configured to use nameservers from
// Termux's resolv.conf ($PREFIX/etc/resolv.conf). If no nameservers can be found
// and public DNS is not opted in, it returns nil so default behavior is preserved.
func getTermuxResolver() *net.Resolver {
	termuxResolverOnce.Do(func() {
		servers, err := loadTermuxNameservers()
		if err != nil || len(servers) == 0 {
			return
		}
		var roundRobin atomic.Uint32
		termuxResolver = &net.Resolver{
			PreferGo: true,
			Dial: func(ctx context.Context, network, address string) (net.Conn, error) {
				var d net.Dialer
				start := int(roundRobin.Add(1) - 1)
				var lastErr error
				for i := 0; i < len(servers); i++ {
					ns := servers[(start+i)%len(servers)]
					conn, err := d.DialContext(ctx, network, ns)
					if err == nil {
						return conn, nil
					}
					lastErr = err
				}
				return nil, lastErr
			},
		}
	})
	return termuxResolver
}

func init() {
	if r := getTermuxResolver(); r != nil {
		net.DefaultResolver = r
	}
}

func configureDialerResolver(d *net.Dialer) {
	if d == nil {
		return
	}
	if r := getTermuxResolver(); r != nil {
		d.Resolver = r
	}
}
