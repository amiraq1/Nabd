// Package endpoint implements the two-layer endpoint acceptance policy for
// provider base URLs.
//
// Layer 1 — load-time (CheckBaseURL): validates the configured base URL at
// registry parse time and standalone override time. Under PolicyStrict (default)
// the URL must use HTTPS and must not resolve to a loopback, RFC 1918, link-local,
// CGNAT, ULA, or cloud-metadata address based on literal host or suffix, unless
// explicitly permitted by NABD_ENDPOINT_ALLOW.
// Under PolicyLoopback, plaintext HTTP is permitted strictly for loopback destinations
// (127.0.0.1, ::1, localhost), and RFC 1918 / ULA private LAN addresses are allowed with HTTPS,
// but cloud metadata and non-routable addresses remain refused.
//
// Layer 2 — connect-time (Dialer / Control hook): a hook on net.Dialer.Control
// that inspects the literal IP address and port right before connection
// establishment on the socket, after DNS resolution. This eliminates the TOCTOU
// window and closes the DNS-rebinding path completely.
// Under PolicyStrict, connections to non-public addresses are refused unless
// permitted by NABD_ENDPOINT_ALLOW.
// Under PolicyLoopback, connections to loopback and private LAN addresses are permitted,
// but cloud metadata, link-local, CGNAT, and 6to4 addresses are refused.
// Under PolicyOpen, both layers are disabled.
//
// Policy is selected from the NABD_ENDPOINT_POLICY environment variable:
//
//	strict   (default) — both layers active; http and non-public addresses refused.
//	loopback            — plaintext http allowed for loopback; RFC 1918 / ULA allowed; cloud metadata refused.
//	open                — both layers disabled; declared endpoints are accepted unconditionally.
package endpoint

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"strconv"
	"strings"
	"syscall"
	"time"

	"nabd/internal/config"
)

// Policy controls endpoint acceptance strictness.
type Policy uint8

const (
	// PolicyStrict is the default: HTTPS required, non-public addresses refused
	// at load time and connect time unless explicitly listed in NABD_ENDPOINT_ALLOW.
	PolicyStrict Policy = iota
	// PolicyLoopback allows loopback (with plaintext HTTP) and RFC 1918 / ULA private
	// LAN addresses (with HTTPS). Cloud metadata, link-local, CGNAT, and 6to4 are refused.
	PolicyLoopback
	// PolicyOpen disables both layers. Declared endpoints are accepted
	// unconditionally. This reduces the security posture for the session.
	PolicyOpen
)

func (pol Policy) String() string {
	switch pol {
	case PolicyStrict:
		return "strict"
	case PolicyLoopback:
		return "loopback"
	case PolicyOpen:
		return "open"
	default:
		return fmt.Sprintf("Policy(%d)", pol)
	}
}

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
// It consults NABD_ENDPOINT_ALLOW for explicitly allowed endpoints.
// An empty baseURL is always accepted (the provider will use its default).
func CheckBaseURL(baseURL string, pol Policy) error {
	return CheckBaseURLWithAllow(baseURL, pol, CurrentAllowList())
}

// CheckBaseURLWithAllow validates baseURL according to pol and allowList at load time.
func CheckBaseURLWithAllow(baseURL string, pol Policy, allowList *AllowList) error {
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
	host := u.Hostname()
	if host == "" {
		return fmt.Errorf("%w: baseURL %q has no host", ErrEndpointRefused, baseURL)
	}

	// If explicitly permitted by allowList, accept immediately.
	if allowList != nil && allowList.AllowsBaseURL(u) {
		return nil
	}

	if pol == PolicyLoopback {
		// Under PolicyLoopback:
		// 1. Plaintext HTTP is permitted strictly for loopback destinations.
		if u.Scheme != "https" {
			if u.Scheme != "http" || !isLoopbackHost(host) {
				return fmt.Errorf(
					"%w: baseURL %q uses scheme %q; under loopback policy, only https or loopback http is permitted (e.g. http://127.0.0.1:11434)",
					ErrEndpointRefused, baseURL, u.Scheme)
			}
		}
		// 2. Loopback and RFC 1918 / ULA private LAN addresses are allowed,
		// but cloud metadata, link-local, CGNAT, and 6to4 remain refused.
		if isMetadataOrBlockedLiteralHost(host) {
			return fmt.Errorf(
				"%w: baseURL %q resolves to a cloud-metadata or non-routable address",
				ErrEndpointRefused, baseURL)
		}
		return nil
	}

	// PolicyStrict: reject plaintext http and non-public addresses.
	if u.Scheme != "https" {
		return fmt.Errorf(
			"%w: baseURL %q uses scheme %q; only https is permitted (set NABD_ENDPOINT_ALLOW or NABD_ENDPOINT_POLICY=loopback for a local runtime)",
			ErrEndpointRefused, baseURL, u.Scheme)
	}
	if isPrivateLiteralHost(host) {
		return fmt.Errorf(
			"%w: baseURL %q resolves to a non-public address; "+
				"set NABD_ENDPOINT_ALLOW or NABD_ENDPOINT_POLICY=loopback for a local runtime",
			ErrEndpointRefused, baseURL)
	}
	return nil
}

// Dialer returns a copy of d configured with a Control hook that validates the
// resolved literal IP address immediately before socket connection establishment.
// This closes the DNS-rebinding window with zero TOCTOU: no second DNS resolution
// occurs, and the address evaluated is the exact IP being dialed on the socket.
// Under PolicyStrict, connections to non-public addresses are refused unless permitted
// by NABD_ENDPOINT_ALLOW.
// Under PolicyLoopback, connections to loopback and private LAN addresses are permitted,
// but cloud metadata, link-local, CGNAT, and 6to4 addresses are refused.
// Under PolicyOpen, d is returned without modification.
func (pol Policy) Dialer(d net.Dialer) *net.Dialer {
	return pol.DialerWithAllow(d, CurrentAllowList())
}

// DialerWithAllow returns a copy of d configured with a Control hook that validates
// addresses according to pol and allowList.
func (pol Policy) DialerWithAllow(d net.Dialer, allowList *AllowList) *net.Dialer {
	if pol == PolicyOpen {
		return &d
	}
	prev := d.Control
	d.Control = func(network, address string, c syscall.RawConn) error {
		if prev != nil {
			if err := prev(network, address, c); err != nil {
				return err
			}
		}
		host, portStr, err := net.SplitHostPort(address)
		if err != nil {
			return fmt.Errorf("%w: invalid dial address %q: %v", ErrEndpointRefused, address, err)
		}
		ip, err := netip.ParseAddr(host)
		if err != nil {
			return fmt.Errorf("%w: non-literal IP address %q at dial time", ErrEndpointRefused, host)
		}
		var port uint16
		if p, err := strconv.ParseUint(portStr, 10, 16); err == nil {
			port = uint16(p)
		}
		if allowList != nil && allowList.AllowsAddr(ip, port) {
			return nil
		}
		if blockedAddrForPolicy(ip, pol) {
			return fmt.Errorf("%w: resolved address %s is not permitted under %s endpoint policy (rebinding prevention)", ErrEndpointRefused, ip, pol)
		}
		return nil
	}
	return &d
}

// Transport returns an *http.Transport using a net.Dialer configured with pol
// and the active NABD_ENDPOINT_ALLOW.
// When an HTTP/HTTPS proxy is configured in the environment, it is validated
// against the endpoint policy and allowlist at request time.
func Transport(pol Policy) *http.Transport {
	return TransportWithAllow(pol, CurrentAllowList())
}

// TransportWithAllow returns an *http.Transport using a net.Dialer configured
// with pol and allowList.
func TransportWithAllow(pol Policy, allowList *AllowList) *http.Transport {
	base := net.Dialer{
		Timeout:   30 * time.Second,
		KeepAlive: 30 * time.Second,
	}
	configureDialerResolver(&base)
	d := pol.DialerWithAllow(base, allowList)
	return &http.Transport{
		Proxy: func(r *http.Request) (*url.URL, error) {
			u, err := proxyFromEnv(r)
			if err != nil || u == nil {
				return u, err
			}
			if pol != PolicyOpen {
				if err := CheckBaseURLWithAllow(u.String(), pol, allowList); err != nil {
					return nil, fmt.Errorf("%w: proxy %s", ErrEndpointRefused, err)
				}
			}
			return u, nil
		},
		DialContext:           d.DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          100,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	}
}

// proxyFromEnv inspects environment proxy variables (HTTPS_PROXY, HTTP_PROXY,
// NO_PROXY and their lowercase equivalents) dynamically for each request.
func proxyFromEnv(req *http.Request) (*url.URL, error) {
	if req == nil || req.URL == nil {
		return nil, nil
	}
	host := req.URL.Hostname()
	if host == "localhost" || host == "127.0.0.1" || host == "::1" {
		return nil, nil
	}
	noProxy := os.Getenv("NO_PROXY")
	if noProxy == "" {
		noProxy = os.Getenv("no_proxy")
	}
	if noProxy != "" {
		for _, p := range strings.Split(noProxy, ",") {
			p = strings.TrimSpace(p)
			if p == "*" || p == host || strings.HasSuffix(host, "."+strings.TrimPrefix(p, ".")) {
				return nil, nil
			}
		}
	}
	var raw string
	if req.URL.Scheme == "https" {
		raw = os.Getenv("HTTPS_PROXY")
		if raw == "" {
			raw = os.Getenv("https_proxy")
		}
	}
	if raw == "" {
		raw = os.Getenv("HTTP_PROXY")
		if raw == "" {
			raw = os.Getenv("http_proxy")
		}
	}
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	raw = strings.TrimSpace(raw)
	if !strings.Contains(raw, "://") {
		raw = "http://" + raw
	}
	return url.Parse(raw)
}

// Client returns an *http.Client configured with the active NABD_ENDPOINT_POLICY,
// NABD_ENDPOINT_ALLOW, and the given timeout. A timeout of 0 disables client-level
// timeout (suitable for streaming requests controlled by context deadlines).
func Client(timeout time.Duration) *http.Client {
	pol, _ := ParsePolicy(config.Get("NABD_ENDPOINT_POLICY"))
	return ClientWithAllow(pol, CurrentAllowList(), timeout)
}

// ClientWithPolicy returns an *http.Client configured with the specified policy.
func ClientWithPolicy(pol Policy, timeout time.Duration) *http.Client {
	return ClientWithAllow(pol, CurrentAllowList(), timeout)
}

// ClientWithAllow returns an *http.Client configured with the specified policy and allowList.
func ClientWithAllow(pol Policy, allowList *AllowList, timeout time.Duration) *http.Client {
	return &http.Client{
		Transport: TransportWithAllow(pol, allowList),
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
	netip.MustParsePrefix("2002::/16"),     // 6to4 encapsulation (RFC 3056)
}

func blockedAddr(ip netip.Addr) bool {
	return blockedAddrForPolicy(ip, PolicyStrict)
}

func blockedAddrForPolicy(ip netip.Addr, pol Policy) bool {
	ip = ip.Unmap()
	if !ip.IsValid() {
		return true
	}
	if pol == PolicyLoopback {
		// Under PolicyLoopback, loopback and RFC 1918 / ULA private addresses are allowed.
		// Cloud metadata (link-local, CGNAT), 6to4, unspecified, and multicast remain blocked.
		if ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified() || ip.IsMulticast() {
			return true
		}
		for _, p := range blockedPrefixes {
			if p.Contains(ip) {
				return true
			}
		}
		return false
	}
	// PolicyStrict: all non-public addresses are blocked.
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

// isLoopbackHost reports whether host is a loopback IP literal (127.0.0.1, ::1, etc.)
// or localhost / *.localhost hostname.
func isLoopbackHost(host string) bool {
	host = strings.TrimPrefix(host, "[")
	host = strings.TrimSuffix(host, "]")
	if ip, err := netip.ParseAddr(host); err == nil {
		ip = ip.Unmap()
		return ip.IsLoopback()
	}
	lower := strings.ToLower(host)
	return lower == "localhost" || strings.HasSuffix(lower, ".localhost")
}

// isMetadataOrBlockedLiteralHost reports whether host is a literal IP corresponding
// to cloud metadata, link-local, CGNAT, 6to4, unspecified, or multicast address.
// Under PolicyLoopback, loopback and RFC 1918 / ULA private IPs return false.
func isMetadataOrBlockedLiteralHost(host string) bool {
	host = strings.TrimPrefix(host, "[")
	host = strings.TrimSuffix(host, "]")
	if ip, err := netip.ParseAddr(host); err == nil {
		ip = ip.Unmap()
		if !ip.IsValid() {
			return true
		}
		if ip.IsLoopback() || ip.IsPrivate() {
			return false
		}
		return blockedAddrForPolicy(ip, PolicyLoopback)
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
