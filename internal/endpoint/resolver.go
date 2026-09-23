package endpoint

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
)

const defaultTermuxPrefix = "/data/data/com.termux/files/usr"

// termuxResolvConfPath returns the path to resolv.conf on Termux.
func termuxResolvConfPath() string {
	prefix := strings.TrimSpace(os.Getenv("PREFIX"))
	if prefix == "" {
		prefix = defaultTermuxPrefix
	}
	return filepath.Join(prefix, "etc", "resolv.conf")
}

// parseResolvConf parses nameservers from a resolv.conf reader.
// It parses lines matching the standard "nameserver <IP>" format.
// Lines beginning with # or ; and empty lines are skipped.
// Valid IPv4 and IPv6 addresses are normalized to "ip:53".
func parseResolvConf(r io.Reader) []string {
	if r == nil {
		return nil
	}
	var servers []string
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || line[0] == '#' || line[0] == ';' {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) >= 2 && fields[0] == "nameserver" {
			ipStr := fields[1]
			if addr, err := netip.ParseAddr(ipStr); err == nil {
				servers = append(servers, net.JoinHostPort(addr.String(), "53"))
			}
		}
	}
	return servers
}

// loadTermuxNameservers reads the nameservers from Termux's resolv.conf.
// If the file is missing or has no nameservers, public DNS (1.1.1.1, 8.8.8.8)
// is used ONLY if explicitly requested via NABD_PUBLIC_DNS=1 (or true).
func loadTermuxNameservers() ([]string, error) {
	p := termuxResolvConfPath()
	f, err := os.Open(p)
	if err == nil {
		defer f.Close()
		ns := parseResolvConf(f)
		if len(ns) > 0 {
			return ns, nil
		}
	}

	// Check explicit public DNS opt-in
	if optIn := strings.TrimSpace(os.Getenv("NABD_PUBLIC_DNS")); optIn == "1" || strings.EqualFold(optIn, "true") {
		return []string{"1.1.1.1:53", "8.8.8.8:53"}, nil
	}

	if err != nil {
		return nil, fmt.Errorf("read %s: %w", p, err)
	}
	return nil, fmt.Errorf("no nameservers found in %s", p)
}
