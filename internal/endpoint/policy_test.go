package endpoint_test

import (
	"context"
	"net"
	"strings"
	"testing"

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

// ─── DialControl — connect-time rebinding guard ──────────────────────────────

func TestControlRefusesRebindingToPrivateAddress(t *testing.T) {
	base := func(ctx context.Context, network, addr string) (net.Conn, error) {
		c, _ := net.Pipe()
		return c, nil
	}
	dial := endpoint.DialControl(base, endpoint.PolicyStrict)

	// Use a literal private IP in the addr so no real DNS is needed.
	_, err := dial(context.Background(), "tcp", "10.0.0.1:443")
	if err == nil {
		t.Fatal("expected DialControl to refuse a private IP address")
	}
	if !strings.Contains(err.Error(), "10.0.0.1") {
		t.Errorf("error %q does not name the refused IP", err)
	}
}

func TestControlPassesThroughPublicAddress(t *testing.T) {
	base := func(ctx context.Context, network, addr string) (net.Conn, error) {
		c, _ := net.Pipe()
		return c, nil
	}
	dial := endpoint.DialControl(base, endpoint.PolicyStrict)
	conn, err := dial(context.Background(), "tcp", "93.184.216.34:443")
	if err != nil {
		t.Fatalf("DialControl refused a public IP: %v", err)
	}
	conn.Close()
}

func TestControlIsNoOpUnderOpenPolicy(t *testing.T) {
	called := false
	base := func(ctx context.Context, network, addr string) (net.Conn, error) {
		called = true
		c, _ := net.Pipe()
		return c, nil
	}
	dial := endpoint.DialControl(base, endpoint.PolicyOpen)
	conn, err := dial(context.Background(), "tcp", "10.0.0.1:80")
	if err != nil {
		t.Fatalf("unexpected error under Open policy: %v", err)
	}
	conn.Close()
	if !called {
		t.Error("base dialer should have been called under Open policy")
	}
}

func TestControlRefusesLoopbackAddress(t *testing.T) {
	base := func(ctx context.Context, network, addr string) (net.Conn, error) {
		c, _ := net.Pipe()
		return c, nil
	}
	dial := endpoint.DialControl(base, endpoint.PolicyStrict)
	_, err := dial(context.Background(), "tcp", "127.0.0.1:11434")
	if err == nil {
		t.Fatal("expected DialControl to refuse loopback address")
	}
}

func TestControlRefusesCloudMetadata(t *testing.T) {
	base := func(ctx context.Context, network, addr string) (net.Conn, error) {
		c, _ := net.Pipe()
		return c, nil
	}
	dial := endpoint.DialControl(base, endpoint.PolicyStrict)
	_, err := dial(context.Background(), "tcp", "169.254.169.254:80")
	if err == nil {
		t.Fatal("expected DialControl to refuse cloud metadata address")
	}
}
