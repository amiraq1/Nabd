// Package endpoint implements the two-layer endpoint acceptance policy for
// provider base URLs.
//
// Layer 1 — load-time (CheckBaseURL): validates the configured base URL at
// registry parse time and standalone override time. Under PolicyStrict (default)
// the URL must use HTTPS and must not resolve to a loopback, RFC 1918, link-local,
// CGNAT, ULA, or cloud-metadata address based on literal host or suffix.
//
// Layer 2 — connect-time (Dialer / Control hook): a hook on net.Dialer.Control
// that inspects the literal IP address and port right before connection
// establishment on the socket, after DNS resolution. This eliminates the TOCTOU
// window and closes the DNS-rebinding path completely.
//
// Policy is selected from the NABD_ENDPOINT_POLICY environment variable:
//
//	strict   (default) — both layers active; http and non-public addresses refused.
//	loopback            — HTTPS required, loopback and RFC 1918 allowed (for ollama/local runtimes).
//	open                — both layers disabled; declared endpoints are accepted unconditionally.
package endpoint

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"syscall"
	"time"

	"nabd/internal/config"
)

// Policy controls endpoint acceptance strictness.
type Policy uint8

const (
	// PolicyStrict is the default: HTTPS required, non-public addresses refused
	// at load time and connect time.
	PolicyStrict Policy = iota
	// PolicyLoopback allows loopback and RFC 1918 addresses but still requires HTTPS.
	// Use for local runtimes such as ollama.
	PolicyLoopback
	// PolicyOpen disables both layers. Declared endpoints are accepted
	// unconditionally. This reduces the security posture for the session.
	PolicyOpen
)

// ErrEndpointRefused is the sentinel error returned when a URL or dial is refused
// by the active endpoint policy.
var ErrEndpointRefused = errors.New("endpoint refused")

// ParsePolicy converts the NABD_ENDPOINT_POLICY value to a Policy.
// An empty string maps to PolicyStrict. Unknown values are an error.
func ParsePolicy(raw string) (Policy, error) {
	switch strings.TrimSpace(strings.ToLower(raw)) {
	case "", "strict":
		return PolicyStrict, nil
	case "loopback":
		return PolicyLoopback, nil
	case "open":
		return PolicyOpen, nil
	default:
		return PolicyStrict, fmt.Errorf(
			"NABD_ENDPOINT_POLICY: unknown value %q; valid values: strict, loopback, open", raw)
	}
}

// CheckBaseURL validates baseURL according to pol at load time.
// An empty baseURL is always accepted (the provider will use its default).
func CheckBaseURL(baseURL string, pol Policy) error {
	if pol == PolicyOpen {
		return nil
	}
	if baseURL == "" {
		return nil
	}
	u, err := url.Parse(baseURL)
	if err != nil {
		return fmt.Errorf("%w: invalid baseURL %q: %v", ErrEndpointRefused, baseURL, err)
	}
	if u.Scheme != "https" {
		return fmt.Errorf(
			"%w: baseURL %q uses scheme %q; only https is permitted (set NABD_ENDPOINT_POLICY=loopback for a local runtime)",
			ErrEndpointRefused, baseURL, u.Scheme)
	}
	host := u.Hostname()
	if host == "" {
		return fmt.Errorf("%w: baseURL %q has no host", ErrEndpointRefused, baseURL)
	}
	if pol == PolicyLoopback {
		// Loopback allows any host; scheme is already checked.
		return nil
	}
	// PolicyStrict: reject private/internal literal hosts.
	if isPrivateLiteralHost(host) {
		return fmt.Errorf(
			"%w: baseURL %q resolves to a non-public address; "+
				"set NABD_ENDPOINT_POLICY=loopback for a local runtime",
			ErrEndpointRefused, baseURL)
	}
	return nil
}

// Dialer returns a copy of d configured with a Control hook that validates the
// resolved literal IP and port immediately before socket connection establishment.
// This closes the DNS-rebinding window with zero TOCTOU: no second DNS resolution
// occurs, and the address evaluated is the exact IP being dialed on the socket.
// Under PolicyStrict, connections to private, loopback, link-local, CGNAT, ULA,
// or cloud-metadata addresses are refused with ErrEndpointRefused.
// Under PolicyOpen or PolicyLoopback, d is returned without modification.
func (pol Policy) Dialer(d net.Dialer) *net.Dialer {
	if pol != PolicyStrict {
		return &d
	}
	prev := d.Control
	d.Control = func(network, address string, c syscall.RawConn) error {
		if prev != nil {
			if err := prev(network, address, c); err != nil {
				return err
			}
		}
		host, _, err := net.SplitHostPort(address)
		if err != nil {
			return fmt.Errorf("%w: invalid dial address %q: %v", ErrEndpointRefused, address, err)
		}
		ip, err := netip.ParseAddr(host)
		if err != nil {
			return fmt.Errorf("%w: non-literal IP address %q at dial time", ErrEndpointRefused, host)
		}
		if blockedAddr(ip) {
			return fmt.Errorf("%w: resolved address %s is not a public address (rebinding prevention); set NABD_ENDPOINT_POLICY=loopback to allow", ErrEndpointRefused, ip)
		}
		return nil
	}
	return &d
}

// DialControl returns a DialContext function wrapping a net.Dialer that enforces pol.
// Deprecated: use pol.Dialer, endpoint.Transport, or endpoint.Client instead.
func DialControl(base func(ctx context.Context, network, addr string) (net.Conn, error), pol Policy) func(ctx context.Context, network, addr string) (net.Conn, error) {
	d := pol.Dialer(net.Dialer{})
	return d.DialContext
}

// Transport returns an *http.Transport using a net.Dialer configured with pol.
func Transport(pol Policy) *http.Transport {
	d := pol.Dialer(net.Dialer{
		Timeout:   30 * time.Second,
		KeepAlive: 30 * time.Second,
	})
	return &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		DialContext:           d.DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          100,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	}
}

// Client returns an *http.Client configured with the active NABD_ENDPOINT_POLICY
// and the given timeout. A timeout of 0 disables client-level timeout (suitable
// for streaming requests controlled by context deadlines).
func Client(timeout time.Duration) *http.Client {
	pol, _ := ParsePolicy(config.Get("NABD_ENDPOINT_POLICY"))
	return ClientWithPolicy(pol, timeout)
}

// ClientWithPolicy returns an *http.Client configured with the specified policy.
func ClientWithPolicy(pol Policy, timeout time.Duration) *http.Client {
	return &http.Client{
		Transport: Transport(pol),
		Timeout:   timeout,
	}
}

// blockedPrefixes defines non-public IPv4 and IPv6 subnets not fully caught
// by netip.Addr.IsPrivate() / IsLoopback() / IsLinkLocalUnicast().
var blockedPrefixes = []netip.Prefix{
	netip.MustParsePrefix("100.64.0.0/10"), // CGNAT (RFC 6598, includes Alibaba metadata 100.100.100.200)
	netip.MustParsePrefix("192.0.0.0/24"),  // IETF Protocol Assignments (RFC 6890)
	netip.MustParsePrefix("198.18.0.0/15"), // Benchmarking (RFC 2544)
	netip.MustParsePrefix("240.0.0.0/4"),   // Reserved (RFC 1112)
	netip.MustParsePrefix("64:ff9b::/96"),  // Local NAT64 (RFC 6052)
}

func blockedAddr(ip netip.Addr) bool {
	ip = ip.Unmap()
	if !ip.IsValid() {
		return true
	}
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified() || ip.IsMulticast() {
		return true
	}
	for _, p := range blockedPrefixes {
		if p.Contains(ip) {
			return true
		}
	}
	return false
}

// isPrivateLiteralHost returns true if host is a loopback, RFC 1918,
// link-local, CGNAT, ULA, or cloud-metadata literal IP or a .internal/.local/.localhost
// suffix, without performing DNS resolution.
func isPrivateLiteralHost(host string) bool {
	host = strings.TrimPrefix(host, "[")
	host = strings.TrimSuffix(host, "]")
	if ip, err := netip.ParseAddr(host); err == nil {
		return blockedAddr(ip)
	}
	lower := strings.ToLower(host)
	for _, suffix := range []string{".internal", ".local", "localhost", ".localhost"} {
		if lower == strings.TrimPrefix(suffix, ".") || strings.HasSuffix(lower, suffix) {
			return true
		}
	}
	return false
}
