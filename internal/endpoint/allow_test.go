package endpoint_test

import (
	"net/netip"
	"net/url"
	"testing"

	"nabd/internal/endpoint"
)

func TestParseAllowList(t *testing.T) {
	// Empty or whitespace returns nil
	for _, raw := range []string{"", "   ", "\t\n"} {
		if al := endpoint.ParseAllowList(raw); al != nil {
			t.Errorf("ParseAllowList(%q) = %v, want nil", raw, al)
		}
	}

	// Various formats
	raw := "http://127.0.0.1:11434, https://10.0.0.5:8000/v1, 192.168.1.0/24, localhost:8080, [::1]:9000, 10.10.10.10"
	al := endpoint.ParseAllowList(raw)
	if al == nil {
		t.Fatal("ParseAllowList returned nil for valid list")
	}

	// 1. URL with port
	u1, _ := url.Parse("http://127.0.0.1:11434/api/chat")
	if !al.AllowsBaseURL(u1) {
		t.Errorf("expected %v to be allowed", u1)
	}

	// 2. URL with path prefix
	u2, _ := url.Parse("https://10.0.0.5:8000/v1/models")
	if !al.AllowsBaseURL(u2) {
		t.Errorf("expected %v to be allowed", u2)
	}
	u2BadPath, _ := url.Parse("https://10.0.0.5:8000/v2/models")
	if al.AllowsBaseURL(u2BadPath) {
		t.Errorf("expected %v to be rejected due to path mismatch", u2BadPath)
	}

	// 3. CIDR prefix
	u3, _ := url.Parse("https://192.168.1.55:8443/v1")
	if !al.AllowsBaseURL(u3) {
		t.Errorf("expected %v to be allowed by CIDR", u3)
	}
	u3Outside, _ := url.Parse("https://192.168.2.55:8443/v1")
	if al.AllowsBaseURL(u3Outside) {
		t.Errorf("expected %v to be rejected outside CIDR", u3Outside)
	}

	// 4. localhost:8080
	u4, _ := url.Parse("http://localhost:8080/v1")
	if !al.AllowsBaseURL(u4) {
		t.Errorf("expected %v to be allowed", u4)
	}
	u4IP, _ := url.Parse("http://127.0.0.1:8080/v1")
	if !al.AllowsBaseURL(u4IP) {
		t.Errorf("expected %v to be allowed via localhost mapping", u4IP)
	}

	// 5. Connect-time AllowsAddr
	if !al.AllowsAddr(netip.MustParseAddr("127.0.0.1"), 11434) {
		t.Error("expected 127.0.0.1:11434 to be allowed")
	}
	if al.AllowsAddr(netip.MustParseAddr("127.0.0.1"), 22) {
		t.Error("expected 127.0.0.1:22 to be refused")
	}
	if !al.AllowsAddr(netip.MustParseAddr("192.168.1.100"), 443) {
		t.Error("expected 192.168.1.100:443 to be allowed by CIDR")
	}
	if al.AllowsAddr(netip.MustParseAddr("192.168.2.100"), 443) {
		t.Error("expected 192.168.2.100:443 to be refused outside CIDR")
	}
	if !al.AllowsAddr(netip.MustParseAddr("::1"), 9000) {
		t.Error("expected [::1]:9000 to be allowed")
	}
	if !al.AllowsAddr(netip.MustParseAddr("10.10.10.10"), 12345) {
		t.Error("expected 10.10.10.10 (any port) to be allowed")
	}
}

func TestNilAllowList(t *testing.T) {
	var al *endpoint.AllowList
	u, _ := url.Parse("http://127.0.0.1:11434")
	if al.AllowsBaseURL(u) {
		t.Error("nil AllowList should return false")
	}
	if al.AllowsAddr(netip.MustParseAddr("127.0.0.1"), 11434) {
		t.Error("nil AllowList should return false")
	}
	if al.AllowsAddr(netip.Addr{}, 11434) {
		t.Error("invalid addr should return false")
	}
}
