package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"nabd/internal/config"
)

// TestStandaloneSelectsCustomRegistryProvider pins NABD_PROVIDER as a free
// registry identifier: a provider defined only in providers.json is selected by
// its name alone, and an identifier the registry does not know fails with the
// file and the known ids instead of silently falling through to the
// credential-detection order.
func TestStandaloneSelectsCustomRegistryProvider(t *testing.T) {
	dir := t.TempDir()
	provPath := filepath.Join(dir, "providers.json")
	authPath := filepath.Join(dir, "auth.json")

	if err := os.WriteFile(provPath, []byte(`{
  "provider": {
    "acme": {
      "api": "openai",
      "options": { "baseURL": "https://acme.example/v1" },
      "defaultModel": "acme-1",
      "models": { "acme-1": { "name": "Acme One" } }
    }
  }
}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(authPath, []byte(`{"acme":{"type":"api","key":"acme-key"}}`), 0o600); err != nil {
		t.Fatal(err)
	}

	t.Setenv("NABD_CONFIG", filepath.Join(dir, "config-absent"))
	t.Setenv("NABD_PROVIDERS_FILE", provPath)
	t.Setenv("NABD_AUTH_FILE", authPath)
	t.Setenv("NABD_MODEL", "")
	t.Setenv("NABD_BASE_URL", "")
	config.ResetForTest()
	t.Cleanup(config.ResetForTest)

	t.Run("a custom provider is selected by its name alone", func(t *testing.T) {
		t.Setenv("NABD_PROVIDER", "acme")
		p, err := pickProvider()
		if err != nil {
			t.Fatalf("pickProvider(acme): %v", err)
		}
		if name := p.Name(); !strings.HasPrefix(name, "acme/") {
			t.Errorf("Name() = %q, want an acme/ prefix", name)
		}
	})

	t.Run("an unknown identifier names the file and the known ids", func(t *testing.T) {
		t.Setenv("NABD_PROVIDER", "absent")
		_, err := pickProvider()
		if err == nil {
			t.Fatal("expected an error for an unknown provider id")
		}
		if !strings.Contains(err.Error(), provPath) {
			t.Errorf("error %q must name the providers file %q", err, provPath)
		}
		if !strings.Contains(err.Error(), "acme") {
			t.Errorf("error %q must list the configured ids", err)
		}
	})
}
