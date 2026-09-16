package provider

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"nabd/internal/registry"
)

// writeRegistryFile writes a 0600 file, the mode the registry's secure open
// requires, and returns its path.
func writeRegistryFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// TestRouteResolvesRegistryProvider proves a provider defined only in
// providers.json, with its secret in auth.json, becomes a working route: the
// dialect picks the constructor, the endpoint and read ceiling come from the
// definition, and the model key resolves through the configured alias id.
func TestRouteResolvesRegistryProvider(t *testing.T) {
	dir := t.TempDir()
	provPath := writeRegistryFile(t, dir, "providers.json", `{
  "provider": {
    "novita": {
      "api": "openai",
      "name": "Novita",
      "options": { "baseURL": "https://api.novita.ai/openai/v1" },
      "readCap": 4096,
      "models": {
        "qwen/qwen3.6-27b": { "name": "Qwen 3.6 27B", "id": "qwen/qwen3.6-27b-2026" }
      }
    }
  }
}`)
	authPath := writeRegistryFile(t, dir, "auth.json", `{ "novita": { "type": "api", "key": "sk-novita-test" } }`)

	reg, err := registry.LoadFromFiles(provPath, authPath, func(string) string { return "" })
	if err != nil {
		t.Fatalf("LoadFromFiles: %v", err)
	}

	entry := RouteEntry{Provider: "novita", Model: "qwen/qwen3.6-27b"}
	prov, err := BuildRouteProviderWithRegistry(reg, entry)
	if err != nil {
		t.Fatalf("BuildRouteProviderWithRegistry: %v", err)
	}

	o, ok := prov.(*OpenAICompat)
	if !ok {
		t.Fatalf("provider type = %T, want *OpenAICompat for api dialect \"openai\"", prov)
	}
	if o.BaseURL != "https://api.novita.ai/openai/v1" {
		t.Errorf("BaseURL = %q, want the configured endpoint", o.BaseURL)
	}
	if o.Model != "qwen/qwen3.6-27b-2026" {
		t.Errorf("Model = %q, want the configured alias id sent on the wire", o.Model)
	}
	if o.Key != "sk-novita-test" {
		t.Errorf("Key was not carried from auth.json into the route")
	}
	if got := o.ReadCapBytes(); got != 4096 {
		t.Errorf("ReadCapBytes() = %d, want 4096 from the provider definition", got)
	}
	if name := o.Name(); !strings.HasPrefix(name, "novita/") {
		t.Errorf("Name() = %q, want a novita/ prefix", name)
	}
}

// TestUnknownProviderErrorNamesConfigFile proves the error for an unconfigured
// provider tells the user everything needed to fix it: the name they asked for,
// the providers that are actually configured, and the file to add it in.
func TestUnknownProviderErrorNamesConfigFile(t *testing.T) {
	dir := t.TempDir()
	provPath := filepath.Join(dir, "providers.json")
	authPath := filepath.Join(dir, "auth.json")

	reg, err := registry.LoadFromFiles(provPath, authPath, func(string) string { return "" })
	if err != nil {
		t.Fatalf("LoadFromFiles: %v", err)
	}

	_, err = BuildRouteProviderWithRegistry(reg, RouteEntry{Provider: "mystery", Model: "m"})
	if err == nil {
		t.Fatal("expected an error for an unconfigured provider")
	}
	msg := err.Error()
	for _, want := range []string{"mystery", "anthropic", provPath} {
		if !strings.Contains(msg, want) {
			t.Errorf("error %q must mention %q", msg, want)
		}
	}

	if err := ValidateRouteKeysWithRegistry(reg, []RouteEntry{{Provider: "mystery", Model: "m"}}); err == nil {
		t.Fatal("ValidateRouteKeys must reject an unconfigured provider")
	}
}

// TestLegacyEnvConfigStillBuildsRoutes proves the backward-compatibility
// contract: an existing setup that only sets the legacy environment variables
// still builds every route, with no providers.json or auth.json on disk. It
// also pins the negative — with no credential anywhere, the failure names the
// auth file rather than a key.
func TestLegacyEnvConfigStillBuildsRoutes(t *testing.T) {
	dir := t.TempDir()
	provPath := filepath.Join(dir, "providers.json")
	authPath := filepath.Join(dir, "auth.json")

	env := map[string]string{
		"ANTHROPIC_API_KEY":  "sk-ant-legacy",
		"GROQ_API_KEY":       "gsk_legacy",
		"OPENROUTER_API_KEY": "sk-or-legacy",
		"NVIDIA_API_KEY":     "nvapi-legacy",
	}
	reg, err := registry.LoadFromFiles(provPath, authPath, func(k string) string { return env[k] })
	if err != nil {
		t.Fatalf("LoadFromFiles: %v", err)
	}

	routes := []RouteEntry{
		{Provider: "anthropic", Model: "claude-sonnet-5"},
		{Provider: "groq", Model: "llama-3.3-70b-versatile"},
		{Provider: "openrouter", Model: "anthropic/claude-3.5-haiku"},
		{Provider: "nvidia", Model: "moonshotai/kimi-k2.6"},
	}
	if err := ValidateRouteKeysWithRegistry(reg, routes); err != nil {
		t.Fatalf("legacy environment must satisfy every configured key: %v", err)
	}
	for _, entry := range routes {
		if _, err := BuildRouteProviderWithRegistry(reg, entry); err != nil {
			t.Errorf("legacy environment did not build the %s route: %v", entry.Provider, err)
		}
	}

	// The negative: no credential anywhere is an error about the auth file.
	bare, err := registry.LoadFromFiles(provPath, authPath, func(string) string { return "" })
	if err != nil {
		t.Fatalf("LoadFromFiles: %v", err)
	}
	err = ValidateRouteKeysWithRegistry(bare, routes[:1])
	if err == nil {
		t.Fatal("expected a missing-key error")
	}
	if !strings.Contains(err.Error(), authPath) {
		t.Errorf("missing-key error %q must name the auth file %q", err, authPath)
	}
}
