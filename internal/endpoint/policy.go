// Package endpoint implements the two-layer endpoint acceptance policy for
// provider base URLs.
//
// Layer 1 — load-time (CheckBaseURL): validates the configured base URL at
// registry parse time. Under PolicyStrict (the default) the URL must use HTTPS
// and must not resolve to a loopback, RFC 1918, link-local, or cloud-metadata
// address based on the literal host.
//
// Layer 2 — connect-time (DialControl): a net.Dialer DialContext wrapper that,
// after DNS resolution, refuses any resolved IP that falls into the private or
// metadata ranges, closing the DNS-rebinding path.
//
// Policy is selected from the NABD_ENDPOINT_POLICY environment variable:
//
//	strict   (default) — both layers active; http and non-public addresses refused.
//	loopback            — HTTPS required, loopback and RFC 1918 allowed (for ollama/local runtimes).
//	open                — both layers disabled; declared endpoints are accepted unconditionally.
//
// BREAKING CHANGE: previously, providers.json accepted any endpoint including
// plaintext http. Set NABD_ENDPOINT_POLICY=loopback for a local runtime such
// as ollama.
package endpoint

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"
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
		return fmt.Errorf("endpoint: invalid baseURL %q: %w", baseURL, err)
	}
	if u.Scheme != "https" {
		return fmt.Errorf(
			"endpoint: baseURL %q uses scheme %q; only https is permitted (set NABD_ENDPOINT_POLICY=loopback for a local runtime)",
			baseURL, u.Scheme)
	}
	host := u.Hostname()
	if host == "" {
		return fmt.Errorf("endpoint: baseURL %q has no host", baseURL)
	}
	if pol == PolicyLoopback {
		// Loopback allows any host; scheme is already checked.
		return nil
	}
	// PolicyStrict: reject private/internal literal hosts.
	if isPrivateLiteralHost(host) {
		return fmt.Errorf(
			"endpoint: baseURL %q resolves to a non-public address; "+
				"set NABD_ENDPOINT_POLICY=loopback for a local runtime",
			baseURL)
	}
	return nil
}

// DialControl returns a DialContext function that wraps base and, after DNS
// resolution, refuses IPs that fall in private or metadata ranges.
// Under PolicyOpen or PolicyLoopback the wrapper is a no-op pass-through.
func DialControl(base func(ctx context.Context, network, addr string) (net.Conn, error), pol Policy) func(ctx context.Context, network, addr string) (net.Conn, error) {
	if pol != PolicyStrict {
		return base
	}
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(addr)
		if err != nil {
			return nil, fmt.Errorf("endpoint: invalid address %q: %w", addr, err)
		}
		// Resolve the host to IPs.
		addrs, err := net.DefaultResolver.LookupHost(ctx, host)
		if err != nil {
			return nil, fmt.Errorf("endpoint: DNS lookup for %q: %w", host, err)
		}
		for _, a := range addrs {
			ip := net.ParseIP(a)
			if ip == nil {
				continue
			}
			if isBlockedIP(ip) {
				return nil, fmt.Errorf(
					"endpoint: resolved address %s for host %q is not a public address; "+
						"this may indicate DNS rebinding. Set NABD_ENDPOINT_POLICY=loopback to allow",
					ip, host)
			}
		}
		return base(ctx, network, net.JoinHostPort(host, port))
	}
}

// isPrivateLiteralHost returns true if host is a loopback, RFC 1918,
// link-local, or cloud-metadata literal IP or a .internal/.local/.localhost
// suffix, without performing DNS resolution.
func isPrivateLiteralHost(host string) bool {
	// Cloud metadata literal IPs (AWS, GCP, Azure).
	cloudMeta := []string{
		"169.254.169.254",
		"fd00:ec2::254",
		"[fd00:ec2::254]",
	}
	for _, cm := range cloudMeta {
		if host == cm {
			return true
		}
	}
	// Literal IP check.
	if ip := net.ParseIP(host); ip != nil {
		return isBlockedIP(ip)
	}
	// Hostname suffix check.
	lower := strings.ToLower(host)
	for _, suffix := range []string{".internal", ".local", "localhost", ".localhost"} {
		if lower == strings.TrimPrefix(suffix, ".") || strings.HasSuffix(lower, suffix) {
			return true
		}
	}
	return false
}

// isBlockedIP reports whether ip falls in a loopback, RFC 1918, link-local,
// or cloud-metadata range that should not be reachable from a provider client.
func isBlockedIP(ip net.IP) bool {
	if ip.IsLoopback() {
		return true
	}
	if ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() {
		return true
	}
	// Cloud metadata ranges: 169.254.169.254 and fd00:ec2::/32.
	meta4 := net.ParseIP("169.254.169.254")
	if ip.Equal(meta4) {
		return true
	}
	_, meta6, _ := net.ParseCIDR("fd00:ec2::/32")
	if meta6 != nil && meta6.Contains(ip) {
		return true
	}
	// RFC 1918 private ranges.
	for _, cidr := range []string{
		"10.0.0.0/8",
		"172.16.0.0/12",
		"192.168.0.0/16",
	} {
		_, network, err := net.ParseCIDR(cidr)
		if err != nil {
			continue
		}
		if network.Contains(ip) {
			return true
		}
	}
	return false
}

// ErrEndpointRefused is the sentinel error returned when a dial is refused.
var ErrEndpointRefused = errors.New("endpoint refused")
