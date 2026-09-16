package provider

import (
	"strings"
	"testing"

	"nabd/internal/registry"
)

// TestCustomEndpointIsAcceptedByDesign pins the v10 policy: any endpoint
// declared in providers.json is accepted, including plaintext http, loopback,
// RFC 1918, and .internal hosts. This is not an oversight and not a guard that
// happens to be missing — it is the decision that makes a local runtime and a
// private gateway usable without a code change. The claim it backs in
// docs/THREAT_MODEL.md states what is given up in exchange.
func TestCustomEndpointIsAcceptedByDesign(t *testing.T) {
	dir := t.TempDir()
	provPath := writeRegistryFile(t, dir, "providers.json", `{
  "provider": {
    "loopback": {
      "api": "openai",
      "name": "Local runtime",
      "options": { "baseURL": "http://127.0.0.1:11434/v1" },
      "models": { "llama3.3:70b": { "name": "Llama 3.3 70B" } }
    },
    "private-net": {
      "api": "openai",
      "name": "Private gateway",
      "options": { "baseURL": "http://10.0.0.7:8000/v1" },
      "models": { "m": { "name": "M" } }
    },
    "suffixed": {
      "api": "anthropic",
      "name": "Internal suffix",
      "options": { "baseURL": "https://gw.internal/v1" },
      "models": { "c": { "name": "C" } }
    }
  }
}`)
	authPath := writeRegistryFile(t, dir, "auth.json", `{
  "loopback":    { "type": "api", "key": "sk-loopback-test" },
  "private-net": { "type": "api", "key": "sk-private-test" },
  "suffixed":    { "type": "api", "key": "sk-suffixed-test" }
}`)

	reg, err := registry.LoadFromFiles(provPath, authPath, func(string) string { return "" })
	if err != nil {
		t.Fatalf("LoadFromFiles: %v", err)
	}

	for _, tc := range []struct {
		provider string
		model    string
		endpoint string
	}{
		{"loopback", "llama3.3:70b", "http://127.0.0.1:11434/v1"},
		{"private-net", "m", "http://10.0.0.7:8000/v1"},
		{"suffixed", "c", "https://gw.internal/v1"},
	} {
		p, err := BuildRouteProviderWithRegistry(reg, RouteEntry{Provider: tc.provider, Model: tc.model})
		if err != nil {
			t.Fatalf("%s: a declared endpoint must be accepted, got: %v", tc.provider, err)
		}
		var got string
		switch c := p.(type) {
		case *OpenAICompat:
			got = c.BaseURL
		case *Anthropic:
			got = c.BaseURL
		default:
			t.Fatalf("%s: unexpected provider type %T", tc.provider, p)
		}
		if got != tc.endpoint {
			t.Errorf("%s: BaseURL = %q, want the declared endpoint %q unchanged", tc.provider, got, tc.endpoint)
		}
	}

	// The negative half of the claim: the failure for an unconfigured provider
	// must still be about configuration, never about a scheme or host rule
	// that no longer exists.
	_, err = BuildRouteProviderWithRegistry(reg, RouteEntry{Provider: "absent", Model: "m"})
	if err == nil {
		t.Fatal("expected an error for an unconfigured provider")
	}
	for _, forbidden := range []string{"https", "public", "loopback address", "private network"} {
		if strings.Contains(strings.ToLower(err.Error()), forbidden) {
			t.Errorf("error %q must not claim a withdrawn endpoint rule (%q)", err, forbidden)
		}
	}
}
