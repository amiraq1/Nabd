package endpoint

import (
	"context"
	"net"
	"net/netip"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"nabd/internal/config"
)

// AllowList holds parsed endpoint exceptions from NABD_ENDPOINT_ALLOW.
// It permits designated base URLs and connect-time addresses while leaving
// the default strict security policy active for all other endpoints.
type AllowList struct {
	entries []allowEntry
}

type allowEntry struct {
	raw      string
	scheme   string       // "http", "https", or "" (any)
	host     string       // lowercase hostname
	port     uint16       // 0 matches any port
	path     string       // optional path prefix (e.g. "/v1")
	ips      []netip.Addr // resolved or literal IPs
	prefix   netip.Prefix // valid when isPrefix is true
	isPrefix bool
	isLoop   bool
}

// CurrentAllowList returns the active AllowList parsed from NABD_ENDPOINT_ALLOW
// (via config.Get or environment variable). Returns nil if empty.
func CurrentAllowList() *AllowList {
	raw := config.Get("NABD_ENDPOINT_ALLOW")
	if raw == "" {
		raw = os.Getenv("NABD_ENDPOINT_ALLOW")
	}
	return ParseAllowList(raw)
}

// ParseAllowList parses a comma- or whitespace-separated list of allowed
// endpoints, URLs, CIDRs, or host:port declarations.
// Returns nil if raw is empty or contains no valid entries.
func ParseAllowList(raw string) *AllowList {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	tokens := strings.FieldsFunc(raw, func(r rune) bool {
		return r == ',' || r == ' ' || r == '\t' || r == '\n'
	})
	var entries []allowEntry
	for _, tok := range tokens {
		tok = strings.TrimSpace(tok)
		if tok == "" {
			continue
		}
		e, ok := parseAllowEntry(tok)
		if ok {
			entries = append(entries, e)
		}
	}
	if len(entries) == 0 {
		return nil
	}
	return &AllowList{entries: entries}
}

func parseAllowEntry(tok string) (allowEntry, bool) {
	e := allowEntry{raw: tok}
	if strings.Contains(tok, "://") {
		u, err := url.Parse(tok)
		if err != nil {
			return e, false
		}
		e.scheme = strings.ToLower(u.Scheme)
		e.host = strings.ToLower(u.Hostname())
		if p := u.Port(); p != "" {
			if portNum, err := strconv.ParseUint(p, 10, 16); err == nil {
				e.port = uint16(portNum)
			}
		}
		if u.Path != "" && u.Path != "/" {
			e.path = strings.TrimRight(u.Path, "/")
		}
	} else if strings.Contains(tok, "/") {
		prefix, err := netip.ParsePrefix(tok)
		if err == nil {
			e.prefix = prefix
			e.isPrefix = true
			return e, true
		}
		return e, false
	} else if h, p, err := net.SplitHostPort(tok); err == nil {
		e.host = strings.ToLower(h)
		if portNum, err := strconv.ParseUint(p, 10, 16); err == nil {
			e.port = uint16(portNum)
		}
	} else {
		e.host = strings.ToLower(tok)
	}

	e.host = strings.TrimPrefix(e.host, "[")
	e.host = strings.TrimSuffix(e.host, "]")

	if ip, err := netip.ParseAddr(e.host); err == nil {
		ip = ip.Unmap()
		e.ips = append(e.ips, ip)
		if ip.IsLoopback() {
			e.isLoop = true
		}
	} else if isLoopbackHost(e.host) {
		e.isLoop = true
		e.ips = append(e.ips, netip.MustParseAddr("127.0.0.1"), netip.MustParseAddr("::1"))
	} else if e.host != "" {
		// Non-loopback hostname: attempt quick DNS lookup for connect-time matching
		ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
		defer cancel()
		if addrs, err := net.DefaultResolver.LookupNetIP(ctx, "ip", e.host); err == nil {
			for _, a := range addrs {
				e.ips = append(e.ips, a.Unmap())
			}
		}
	}
	return e, true
}

// AllowsBaseURL reports whether u is permitted by the allowlist at load time.
func (al *AllowList) AllowsBaseURL(u *url.URL) bool {
	if al == nil || len(al.entries) == 0 || u == nil {
		return false
	}
	uHost := strings.ToLower(u.Hostname())
	uHost = strings.TrimPrefix(uHost, "[")
	uHost = strings.TrimSuffix(uHost, "]")

	uPortStr := u.Port()
	var uPort uint16
	if uPortStr != "" {
		if p, err := strconv.ParseUint(uPortStr, 10, 16); err == nil {
			uPort = uint16(p)
		}
	} else if u.Scheme == "https" {
		uPort = 443
	} else if u.Scheme == "http" {
		uPort = 80
	}

	uIP, err := netip.ParseAddr(uHost)
	hasIP := (err == nil)
	if hasIP {
		uIP = uIP.Unmap()
	}

	for _, e := range al.entries {
		if e.scheme != "" && !strings.EqualFold(e.scheme, u.Scheme) {
			continue
		}
		if e.port != 0 && uPort != 0 && e.port != uPort {
			continue
		}
		if e.path != "" {
			uPath := strings.TrimRight(u.Path, "/")
			if uPath != e.path && !strings.HasPrefix(uPath, e.path+"/") {
				continue
			}
		}
		if e.isPrefix {
			if hasIP && e.prefix.Contains(uIP) {
				return true
			}
			continue
		}
		if e.isLoop && isLoopbackHost(uHost) {
			return true
		}
		if hasIP {
			for _, ip := range e.ips {
				if ip == uIP {
					return true
				}
			}
		}
		if e.host != "" && (e.host == uHost || strings.HasSuffix(uHost, "."+strings.TrimPrefix(e.host, "."))) {
			return true
		}
	}
	return false
}

// AllowsAddr reports whether ip:port is permitted by the allowlist at connect time.
func (al *AllowList) AllowsAddr(ip netip.Addr, port uint16) bool {
	if al == nil || len(al.entries) == 0 {
		return false
	}
	ip = ip.Unmap()
	if !ip.IsValid() {
		return false
	}
	for _, e := range al.entries {
		if e.port != 0 && port != 0 && e.port != port {
			continue
		}
		if e.isPrefix {
			if e.prefix.Contains(ip) {
				return true
			}
			continue
		}
		if e.isLoop && ip.IsLoopback() {
			return true
		}
		for _, allowedIP := range e.ips {
			if allowedIP == ip {
				return true
			}
		}
	}
	return false
}
