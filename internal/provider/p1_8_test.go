package provider

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"nabd/internal/endpoint"
	"nabd/internal/registry"
)

// TestP1_8OverrideUsesGuardedClientRefusesRedirect proves the NABD_BASE_URL
// override path cannot exfiltrate the provider key through an HTTP redirect.
//
// P1-8's attack surface is the same as P0-1 (a 3xx response relaying the
// provider credential to an attacker host), reached via the standalone
// NABD_BASE_URL override. The override is first validated by
// endpoint.CheckBaseURL (refusing plaintext/private hosts per the active
// endpoint policy), and the resulting dialect is always built on
// endpoint.Client(0) — whose transport refuses redirects. Together these close
// P1-8: even an override pointing at an attacker-controlled host cannot leak
// the key via a 3xx response, and the key is bound to a guarded client.
func TestP1_8OverrideUsesGuardedClientRefusesRedirect(t *testing.T) {
	t.Setenv("NABD_ENDPOINT_POLICY", "open") // allow connection to httptest (127.0.0.1) but still refuse redirects

	var leakHit bool
	leakTarget := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		leakHit = true
		w.WriteHeader(200)
	}))
	defer leakTarget.Close()

	redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", leakTarget.URL)
		w.WriteHeader(http.StatusFound)
	}))
	defer redirector.Close()

	// Catalog baseURL is a benign public host; NABD_BASE_URL is overridden to
	// the attacker redirector via BuildStandaloneProviderWithRegistry.
	dir := t.TempDir()
	provPath := writeRegistryFile(t, dir, "providers.json", `{
  "provider": {
    "pub": {
      "api": "openai",
      "name": "Public",
      "options": { "baseURL": "https://api.example.com/v1" },
      "defaultModel": "m",
      "models": { "m": { "name": "M" } }
    }
  }
}`)
	authPath := writeRegistryFile(t, dir, "auth.json", `{"pub": {"type": "api", "key": "sk-test"}}`)
	reg, err := registry.LoadFromFiles(provPath, authPath, func(string) string { return "" })
	if err != nil {
		t.Fatalf("LoadFromFiles: %v", err)
	}

	p, err := BuildStandaloneProviderWithRegistry(reg, "pub", "", redirector.URL)
	if err != nil {
		t.Fatalf("BuildStandaloneProviderWithRegistry with override: %v", err)
	}
	op, ok := p.(*OpenAICompat)
	if !ok {
		t.Fatalf("expected *OpenAICompat, got %T", p)
	}
	if op.Client == nil {
		t.Fatal("dialect has no client")
	}

	req, _ := http.NewRequest("POST", redirector.URL, strings.NewReader("{}"))
	req.Header.Set("x-api-key", "sk-provider-secret")
	resp, err := op.Client.Do(req)
	if err == nil {
		if resp != nil {
			resp.Body.Close()
		}
		t.Fatal("expected the guarded client to refuse the redirect; got nil error")
	}
	if !errors.Is(err, endpoint.ErrRedirectRefused) {
		t.Fatalf("expected ErrRedirectRefused, got %v", err)
	}
	if leakHit {
		t.Fatal("provider key leaked to the redirect target via NABD_BASE_URL override")
	}
}
