package endpoint_test

import (
	"context"
	"errors"
	"net"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"nabd/internal/endpoint"
)

// ─── ParsePolicy ─────────────────────────────────────────────────────────────

func TestParsePolicyStrict(t *testing.T) {
	for _, raw := range []string{"", "strict", "STRICT", "  strict  "} {
		p, err := endpoint.ParsePolicy(raw)
		if err != nil {
			t.Errorf("ParsePolicy(%q): unexpected error: %v", raw, err)
		}
		if p != endpoint.PolicyStrict {
			t.Errorf("ParsePolicy(%q) = %v, want PolicyStrict", raw, p)
		}
	}
}

func TestParsePolicyLoopback(t *testing.T) {
	p, err := endpoint.ParsePolicy("loopback")
	if err != nil {
		t.Fatalf("ParsePolicy(loopback): %v", err)
	}
	if p != endpoint.PolicyLoopback {
		t.Errorf("ParsePolicy(loopback) = %v, want PolicyLoopback", p)
	}
}

func TestParsePolicyOpen(t *testing.T) {
	p, err := endpoint.ParsePolicy("open")
	if err != nil {
		t.Fatalf("ParsePolicy(open): %v", err)
	}
	if p != endpoint.PolicyOpen {
		t.Errorf("ParsePolicy(open) = %v, want PolicyOpen", p)
	}
}

func TestParsePolicyUnknown(t *testing.T) {
	_, err := endpoint.ParsePolicy("permissive")
	if err == nil {
		t.Error("expected error for unknown policy")
	}
	if !strings.Contains(err.Error(), "permissive") {
		t.Errorf("error %q does not name the unknown value", err)
	}
}

// ─── CheckBaseURL — PolicyStrict ─────────────────────────────────────────────

func TestStrictAcceptsPublicHTTPS(t *testing.T) {
	for _, u := range []string{
		"https://api.openai.com/v1",
		"https://api.anthropic.com/v1/messages",
		"https://api.groq.com/openai/v1",
	} {
		if err := endpoint.CheckBaseURL(u, endpoint.PolicyStrict); err != nil {
			t.Errorf("CheckBaseURL(%q, Strict): unexpected error: %v", u, err)
		}
	}
}

func TestStrictRefusesPlaintextHTTP(t *testing.T) {
	err := endpoint.CheckBaseURL("http://api.example.com/v1", endpoint.PolicyStrict)
	if err == nil {
		t.Fatal("expected error for http scheme under Strict policy")
	}
	if !errors.Is(err, endpoint.ErrEndpointRefused) {
		t.Errorf("error %v does not wrap ErrEndpointRefused", err)
	}
	if !strings.Contains(err.Error(), "https") {
		t.Errorf("error %q does not mention https requirement", err)
	}
}

func TestStrictRefusesCloudMetadataLiteral(t *testing.T) {
	for _, u := range []string{
		"https://169.254.169.254/latest/meta-data",
		"https://[fd00:ec2::254]/v1",
	} {
		err := endpoint.CheckBaseURL(u, endpoint.PolicyStrict)
		if err == nil {
			t.Errorf("CheckBaseURL(%q, Strict): expected error for cloud metadata address", u)
		}
		if !errors.Is(err, endpoint.ErrEndpointRefused) {
			t.Errorf("error %v does not wrap ErrEndpointRefused", err)
		}
	}
}

func TestStrictRefusesLoopbackLiteral(t *testing.T) {
	for _, u := range []string{
		"https://127.0.0.1:11434/v1",
		"https://[::1]/v1",
		"https://localhost/v1",
	} {
		err := endpoint.CheckBaseURL(u, endpoint.PolicyStrict)
		if err == nil {
			t.Errorf("CheckBaseURL(%q, Strict): expected error for loopback address", u)
		}
	}
}

func TestStrictRefusesPrivateRFC1918Literal(t *testing.T) {
	for _, u := range []string{
		"https://10.0.0.7:8000/v1",
		"https://172.16.1.1/v1",
		"https://192.168.0.1/v1",
	} {
		err := endpoint.CheckBaseURL(u, endpoint.PolicyStrict)
		if err == nil {
			t.Errorf("CheckBaseURL(%q, Strict): expected error for RFC 1918 address", u)
		}
	}
}

func TestStrictRefusesCGNATLiteral(t *testing.T) {
	for _, u := range []string{
		"https://100.64.0.1:8000/v1",
		"https://100.100.100.200/v1", // Alibaba cloud metadata
	} {
		err := endpoint.CheckBaseURL(u, endpoint.PolicyStrict)
		if err == nil {
			t.Errorf("CheckBaseURL(%q, Strict): expected error for CGNAT address", u)
		}
		if !errors.Is(err, endpoint.ErrEndpointRefused) {
			t.Errorf("error %v does not wrap ErrEndpointRefused", err)
		}
	}
}

func TestStrictRefusesIPv6ULA(t *testing.T) {
	for _, u := range []string{
		"https://[fc00::1]/v1",
		"https://[fd12:3456::1]/v1",
	} {
		err := endpoint.CheckBaseURL(u, endpoint.PolicyStrict)
		if err == nil {
			t.Errorf("CheckBaseURL(%q, Strict): expected error for IPv6 ULA address", u)
		}
		if !errors.Is(err, endpoint.ErrEndpointRefused) {
			t.Errorf("error %v does not wrap ErrEndpointRefused", err)
		}
	}
}

func TestStrictRefusesIPv4MappedIPv6(t *testing.T) {
	for _, u := range []string{
		"https://[::ffff:10.0.0.1]/v1",
		"https://[::ffff:127.0.0.1]/v1",
	} {
		err := endpoint.CheckBaseURL(u, endpoint.PolicyStrict)
		if err == nil {
			t.Errorf("CheckBaseURL(%q, Strict): expected error for IPv4-mapped IPv6 address", u)
		}
		if !errors.Is(err, endpoint.ErrEndpointRefused) {
			t.Errorf("error %v does not wrap ErrEndpointRefused", err)
		}
	}
}

func TestStrictRefusesUnspecifiedAddress(t *testing.T) {
	for _, u := range []string{
		"https://0.0.0.0:8000/v1",
		"https://[::]:8000/v1",
	} {
		err := endpoint.CheckBaseURL(u, endpoint.PolicyStrict)
		if err == nil {
			t.Errorf("CheckBaseURL(%q, Strict): expected error for unspecified address", u)
		}
		if !errors.Is(err, endpoint.ErrEndpointRefused) {
			t.Errorf("error %v does not wrap ErrEndpointRefused", err)
		}
	}
}

func TestStrictRefusesInternalSuffix(t *testing.T) {
	for _, u := range []string{
		"https://gw.internal/v1",
		"https://svc.local/v1",
		"https://host.localhost/v1",
	} {
		err := endpoint.CheckBaseURL(u, endpoint.PolicyStrict)
		if err == nil {
			t.Errorf("CheckBaseURL(%q, Strict): expected error for internal hostname", u)
		}
	}
}

func TestStrictAcceptsEmptyBaseURL(t *testing.T) {
	if err := endpoint.CheckBaseURL("", endpoint.PolicyStrict); err != nil {
		t.Errorf("CheckBaseURL(empty, Strict): unexpected error: %v", err)
	}
}

// ─── CheckBaseURL — PolicyLoopback ───────────────────────────────────────────

func TestLoopbackAllowsLoopbackAndPrivate(t *testing.T) {
	for _, u := range []string{
		"https://127.0.0.1:11434/v1",
		"https://10.0.0.7:8000/v1",
		"https://gw.internal/v1",
	} {
		if err := endpoint.CheckBaseURL(u, endpoint.PolicyLoopback); err != nil {
			t.Errorf("CheckBaseURL(%q, Loopback): unexpected error: %v", u, err)
		}
	}
}

func TestLoopbackStillRefusesHTTP(t *testing.T) {
	err := endpoint.CheckBaseURL("http://127.0.0.1:11434/v1", endpoint.PolicyLoopback)
	if err == nil {
		t.Error("expected error: Loopback still requires HTTPS")
	}
}

// ─── CheckBaseURL — PolicyOpen ───────────────────────────────────────────────

func TestOpenLevelDisablesBothLayers(t *testing.T) {
	for _, u := range []string{
		"http://127.0.0.1:11434/v1",
		"http://10.0.0.7:8000/v1",
		"https://169.254.169.254/v1",
	} {
		if err := endpoint.CheckBaseURL(u, endpoint.PolicyOpen); err != nil {
			t.Errorf("CheckBaseURL(%q, Open): unexpected error: %v", u, err)
		}
	}
}

// ─── DialControl / Control Hook — connect-time rebinding guard ───────────────

// startMockDNSServer starts an in-process UDP DNS server for testing.
// handler receives the query count (1-based for Type A queries) and question name,
// and returns the IPv4 to respond with.
func startMockDNSServer(t *testing.T, handler func(count int, name string) net.IP) *net.Resolver {
	t.Helper()
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to listen for mock DNS: %v", err)
	}
	t.Cleanup(func() { pc.Close() })

	var count int32
	go func() {
		buf := make([]byte, 512)
		for {
			n, addr, err := pc.ReadFrom(buf)
			if err != nil {
				return
			}
			req := buf[:n]
			if len(req) < 12 {
				continue
			}

			// Parse question name
			i := 12
			var nameParts []string
			for i < len(req) && req[i] != 0 {
				l := int(req[i])
				i++
				if i+l > len(req) {
					break
				}
				nameParts = append(nameParts, string(req[i:i+l]))
				i += l
			}
			qName := strings.Join(nameParts, ".")
			i++ // skip null byte
			if i+4 > len(req) {
				continue
			}
			qType := (uint16(req[i]) << 8) | uint16(req[i+1])
			i += 4 // type + class

			var ip net.IP
			if qType == 1 /* Type A */ {
				qCount := atomic.AddInt32(&count, 1)
				ip = handler(int(qCount), qName)
			}

			ip4 := ip.To4()
			if ip4 == nil {
				// Return NODATA response (Answers: 0)
				resp := make([]byte, 0, 12+i)
				resp = append(resp, req[0], req[1]) // ID
				resp = append(resp, 0x81, 0x80)     // Flags: standard response, no error
				resp = append(resp, 0x00, 0x01)     // Questions: 1
				resp = append(resp, 0x00, 0x00)     // Answers: 0
				resp = append(resp, 0x00, 0x00)     // Authority: 0
				resp = append(resp, 0x00, 0x00)     // Additional: 0
				resp = append(resp, req[12:i]...)   // Echo question
				_, _ = pc.WriteTo(resp, addr)
				continue
			}

			resp := make([]byte, 0, 12+i+16)
			resp = append(resp, req[0], req[1]) // ID
			resp = append(resp, 0x81, 0x80)     // Flags: standard response, no error
			resp = append(resp, 0x00, 0x01)     // Questions: 1
			resp = append(resp, 0x00, 0x01)     // Answers: 1
			resp = append(resp, 0x00, 0x00)     // Authority: 0
			resp = append(resp, 0x00, 0x00)     // Additional: 0
			resp = append(resp, req[12:i]...)   // Echo question

			// Answer record
			resp = append(resp, 0xc0, 0x0c)             // Name pointer
			resp = append(resp, 0x00, 0x01)             // Type A
			resp = append(resp, 0x00, 0x01)             // Class IN
			resp = append(resp, 0x00, 0x00, 0x00, 0x00) // TTL: 0
			resp = append(resp, 0x00, 0x04)             // Length: 4
			resp = append(resp, ip4...)                 // IPv4 bytes

			_, _ = pc.WriteTo(resp, addr)
		}
	}()

	return &net.Resolver{
		PreferGo: true,
		Dial: func(ctx context.Context, network, address string) (net.Conn, error) {
			return net.Dial("udp", pc.LocalAddr().String())
		},
	}
}

func TestControlRefusesRebindingViaResolver(t *testing.T) {
	// Simulate DNS rebinding:
	// Query 1 returns a public IP (93.184.216.34)
	// Query 2 returns a private IP (10.0.0.1)
	resolver := startMockDNSServer(t, func(count int, name string) net.IP {
		if count == 1 {
			return net.ParseIP("93.184.216.34")
		}
		return net.ParseIP("10.0.0.1")
	})

	d := endpoint.PolicyStrict.Dialer(net.Dialer{
		Resolver: resolver,
		Timeout:  2 * time.Second,
	})

	// First dial: resolves to 93.184.216.34. Control allows it; connection attempt times out, NOT ErrEndpointRefused.
	ctx1, cancel1 := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel1()
	_, err1 := d.DialContext(ctx1, "tcp", "rebind.example:80")
	if errors.Is(err1, endpoint.ErrEndpointRefused) {
		t.Fatalf("first dial unexpectedly refused by endpoint policy: %v", err1)
	}

	// Second dial: resolves to 10.0.0.1 (DNS rebinding!).
	// Control hook evaluates the literal IP 10.0.0.1 on the socket before connect and refuses it.
	_, err2 := d.DialContext(context.Background(), "tcp", "rebind.example:80")
	if err2 == nil {
		t.Fatal("expected rebinding dial to private IP to be refused")
	}
	if !errors.Is(err2, endpoint.ErrEndpointRefused) {
		t.Fatalf("expected ErrEndpointRefused on rebind, got: %v", err2)
	}
	if !strings.Contains(err2.Error(), "10.0.0.1") {
		t.Errorf("expected error to mention refused IP 10.0.0.1, got: %v", err2)
	}
}

func TestControlRefusesRebindingToPrivateAddress(t *testing.T) {
	d := endpoint.PolicyStrict.Dialer(net.Dialer{
		Timeout: 2 * time.Second,
	})

	// Use a literal private IP in the addr so no real DNS is needed.
	_, err := d.DialContext(context.Background(), "tcp", "10.0.0.1:443")
	if err == nil {
		t.Fatal("expected Dialer Control to refuse a private IP address")
	}
	if !errors.Is(err, endpoint.ErrEndpointRefused) {
		t.Errorf("expected ErrEndpointRefused, got %v", err)
	}
	if !strings.Contains(err.Error(), "10.0.0.1") {
		t.Errorf("error %q does not name the refused IP", err)
	}
}

func TestControlPassesThroughPublicAddress(t *testing.T) {
	d := endpoint.PolicyStrict.Dialer(net.Dialer{
		Timeout: 50 * time.Millisecond,
	})
	// Dials a public IP: Control allows it. The connection will fail at the network
	// layer (timeout / conn refused), NOT with ErrEndpointRefused.
	_, err := d.DialContext(context.Background(), "tcp", "93.184.216.34:443")
	if errors.Is(err, endpoint.ErrEndpointRefused) {
		t.Fatalf("Control unexpectedly refused public IP: %v", err)
	}
}

func TestControlIsNoOpUnderOpenPolicy(t *testing.T) {
	d := endpoint.PolicyOpen.Dialer(net.Dialer{
		Timeout: 50 * time.Millisecond,
	})
	// Under PolicyOpen, Control does not block private IP
	_, err := d.DialContext(context.Background(), "tcp", "10.0.0.1:80")
	if errors.Is(err, endpoint.ErrEndpointRefused) {
		t.Fatalf("unexpected ErrEndpointRefused under Open policy: %v", err)
	}
}

func TestControlRefusesLoopbackAddress(t *testing.T) {
	d := endpoint.PolicyStrict.Dialer(net.Dialer{
		Timeout: 2 * time.Second,
	})
	_, err := d.DialContext(context.Background(), "tcp", "127.0.0.1:11434")
	if err == nil {
		t.Fatal("expected Control to refuse loopback address")
	}
	if !errors.Is(err, endpoint.ErrEndpointRefused) {
		t.Errorf("expected ErrEndpointRefused, got %v", err)
	}
}

func TestControlRefusesCloudMetadata(t *testing.T) {
	d := endpoint.PolicyStrict.Dialer(net.Dialer{
		Timeout: 2 * time.Second,
	})
	_, err := d.DialContext(context.Background(), "tcp", "169.254.169.254:80")
	if err == nil {
		t.Fatal("expected Control to refuse cloud metadata address")
	}
	if !errors.Is(err, endpoint.ErrEndpointRefused) {
		t.Errorf("expected ErrEndpointRefused, got %v", err)
	}
}

func TestControlRefusesCGNATAddress(t *testing.T) {
	d := endpoint.PolicyStrict.Dialer(net.Dialer{
		Timeout: 2 * time.Second,
	})
	_, err := d.DialContext(context.Background(), "tcp", "100.100.100.200:80")
	if err == nil {
		t.Fatal("expected Control to refuse CGNAT address")
	}
	if !errors.Is(err, endpoint.ErrEndpointRefused) {
		t.Errorf("expected ErrEndpointRefused, got %v", err)
	}
}

func TestControlRefusesIPv6ULAAddress(t *testing.T) {
	d := endpoint.PolicyStrict.Dialer(net.Dialer{
		Timeout: 2 * time.Second,
	})
	_, err := d.DialContext(context.Background(), "tcp", "[fc00::1]:80")
	if err == nil {
		t.Fatal("expected Control to refuse IPv6 ULA address")
	}
	if !errors.Is(err, endpoint.ErrEndpointRefused) {
		t.Errorf("expected ErrEndpointRefused, got %v", err)
	}
}

func TestControlRefusesIPv4MappedIPv6(t *testing.T) {
	d := endpoint.PolicyStrict.Dialer(net.Dialer{
		Timeout: 2 * time.Second,
	})
	_, err := d.DialContext(context.Background(), "tcp", "[::ffff:10.0.0.1]:80")
	if err == nil {
		t.Fatal("expected Control to refuse IPv4-mapped IPv6 address")
	}
	if !errors.Is(err, endpoint.ErrEndpointRefused) {
		t.Errorf("expected ErrEndpointRefused, got %v", err)
	}
}

func TestControlRefusesUnspecifiedAddress(t *testing.T) {
	d := endpoint.PolicyStrict.Dialer(net.Dialer{
		Timeout: 2 * time.Second,
	})
	_, err := d.DialContext(context.Background(), "tcp", "0.0.0.0:80")
	if err == nil {
		t.Fatal("expected Control to refuse unspecified address")
	}
	if !errors.Is(err, endpoint.ErrEndpointRefused) {
		t.Errorf("expected ErrEndpointRefused, got %v", err)
	}
}

func TestClientConstructors(t *testing.T) {
	c := endpoint.Client(5 * time.Second)
	if c == nil || c.Transport == nil {
		t.Fatal("endpoint.Client returned nil client or transport")
	}
	if c.Timeout != 5*time.Second {
		t.Errorf("Client timeout = %v, want 5s", c.Timeout)
	}
}
