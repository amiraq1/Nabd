package provider

import (
	"path/filepath"
	"testing"

	"nabd/internal/config"
	"nabd/internal/registry"
)

// TestStandaloneProviderReadsEnvEdge pins the two environment overrides the
// standalone path reads at its edge — NABD_MODEL and NABD_BASE_URL — and the
// retry policy it must keep. NABD_BASE_URL now overrides the catalog endpoint
// for every provider, groq included; the deleted per-provider constructors
// ignored it for groq. See docs/TECH_DEBT.md, BASE_URL_UNIFIED.
func TestStandaloneProviderReadsEnvEdge(t *testing.T) {
	dir := t.TempDir()
	// Keep the user's real configuration and registry out of the test.
	t.Setenv("NABD_CONFIG", filepath.Join(dir, "config-absent"))
	t.Setenv("NABD_PROVIDERS_FILE", filepath.Join(dir, "providers.json"))
	t.Setenv("NABD_AUTH_FILE", filepath.Join(dir, "auth.json"))
	t.Setenv("ANTHROPIC_API_KEY", "an-edge")
	t.Setenv("GROQ_API_KEY", "gq-edge")

	config.ResetForTest()
	t.Cleanup(config.ResetForTest)

	t.Run("A: the catalog default model is used when NABD_MODEL is unset", func(t *testing.T) {
		t.Setenv("NABD_MODEL", "")
		t.Setenv("NABD_BASE_URL", "")

		o := standaloneOpenAI(t, "groq")
		if o.Model != "openai/gpt-oss-120b" {
			t.Errorf("Model = %q, want the catalog DefaultModel %q", o.Model, "openai/gpt-oss-120b")
		}
		if o.BaseURL != "https://api.groq.com/openai/v1" {
			t.Errorf("BaseURL = %q, want the catalog endpoint", o.BaseURL)
		}
		if o.retryPolicy != RetryStandalone {
			t.Errorf("retryPolicy = %v, want RetryStandalone", o.retryPolicy)
		}
	})

	t.Run("B: NABD_MODEL overrides and passes through unchanged", func(t *testing.T) {
		t.Setenv("NABD_MODEL", "custom-model-x")
		t.Setenv("NABD_BASE_URL", "")

		o := standaloneOpenAI(t, "groq")
		if o.Model != "custom-model-x" {
			t.Errorf("Model = %q, want the override verbatim", o.Model)
		}
		if o.retryPolicy != RetryStandalone {
			t.Errorf("retryPolicy = %v, want RetryStandalone", o.retryPolicy)
		}
	})

	t.Run("C: NABD_BASE_URL overrides the catalog endpoint, groq included", func(t *testing.T) {
		t.Setenv("NABD_MODEL", "")
		t.Setenv("NABD_BASE_URL", "https://override.example.com/v1")

		o := standaloneOpenAI(t, "groq")
		if o.BaseURL != "https://override.example.com/v1" {
			t.Errorf("groq BaseURL = %q, want the override", o.BaseURL)
		}
		if o.retryPolicy != RetryStandalone {
			t.Errorf("groq retryPolicy = %v, want RetryStandalone", o.retryPolicy)
		}

		a, ok := standalone(t, "anthropic").(*Anthropic)
		if !ok {
			t.Fatal("anthropic did not build an *Anthropic")
		}
		if a.BaseURL != "https://override.example.com/v1" {
			t.Errorf("anthropic BaseURL = %q, want the override", a.BaseURL)
		}
		if a.retryPolicy != RetryStandalone {
			t.Errorf("anthropic retryPolicy = %v, want RetryStandalone", a.retryPolicy)
		}
	})
}

func standalone(t *testing.T, id string) Provider {
	t.Helper()
	p, err := BuildStandaloneProvider(id)
	if err != nil {
		t.Fatalf("BuildStandaloneProvider(%s): %v", id, err)
	}
	return p
}

func standaloneOpenAI(t *testing.T, id string) *OpenAICompat {
	t.Helper()
	o, ok := standalone(t, id).(*OpenAICompat)
	if !ok {
		t.Fatalf("%s did not build an *OpenAICompat", id)
	}
	return o
}

// TestReadCapComesFromRegistryNotName pins the source of the read ceiling: the
// provider's registry entry, never its name. A custom provider that happens to
// be called groq does not inherit Groq's cap; the builtin groq does, because its
// catalog entry states it.
func TestReadCapComesFromRegistryNotName(t *testing.T) {
	dir := t.TempDir()
	customPath := writeRegistryFile(t, dir, "providers.json", `{
  "provider": {
    "metered": {
      "api": "openai",
      "options": { "baseURL": "https://metered.example/v1" },
      "readCap": 1234,
      "defaultModel": "m",
      "models": { "m": { "name": "M" } }
    },
    "groq": {
      "api": "openai",
      "options": { "baseURL": "https://not-groq.example/v1" },
      "defaultModel": "m",
      "models": { "m": { "name": "M" } }
    }
  }
}`)
	customAuth := writeRegistryFile(t, dir, "auth.json", `{
  "metered": { "type": "api", "key": "k" },
  "groq":    { "type": "api", "key": "k" }
}`)
	custom, err := registry.LoadFromFiles(customPath, customAuth, func(string) string { return "" })
	if err != nil {
		t.Fatalf("LoadFromFiles: %v", err)
	}

	builtin, err := registry.LoadFromFiles(
		filepath.Join(dir, "absent-providers.json"),
		filepath.Join(dir, "absent-auth.json"),
		func(k string) string {
			if k == "GROQ_API_KEY" {
				return "gq"
			}
			return ""
		},
	)
	if err != nil {
		t.Fatalf("LoadFromFiles: %v", err)
	}

	capOf := func(t *testing.T, p Provider) int {
		t.Helper()
		rc, ok := p.(ReadCapper)
		if !ok {
			t.Fatalf("%T does not implement ReadCapper", p)
		}
		return rc.ReadCapBytes()
	}

	t.Run("1: an explicit readCap is used by both paths", func(t *testing.T) {
		routed, err := BuildRouteProviderWithRegistry(custom, RouteEntry{Provider: "metered", Model: "m"})
		if err != nil {
			t.Fatal(err)
		}
		if got := capOf(t, routed); got != 1234 {
			t.Errorf("route cap = %d, want the explicit 1234", got)
		}
		standalone, err := BuildStandaloneProviderWithRegistry(custom, "metered", "", "")
		if err != nil {
			t.Fatal(err)
		}
		if got := capOf(t, standalone); got != 1234 {
			t.Errorf("standalone cap = %d, want the explicit 1234", got)
		}
	})

	t.Run("2: a custom groq without readCap gets the default, not Groq's cap", func(t *testing.T) {
		routed, err := BuildRouteProviderWithRegistry(custom, RouteEntry{Provider: "groq", Model: "m"})
		if err != nil {
			t.Fatal(err)
		}
		if got := capOf(t, routed); got != DefaultReadCapBytes {
			t.Errorf("route cap = %d, want DefaultReadCapBytes %d", got, DefaultReadCapBytes)
		}
		standalone, err := BuildStandaloneProviderWithRegistry(custom, "groq", "", "")
		if err != nil {
			t.Fatal(err)
		}
		if got := capOf(t, standalone); got != DefaultReadCapBytes {
			t.Errorf("standalone cap = %d, want DefaultReadCapBytes %d", got, DefaultReadCapBytes)
		}
		if DefaultReadCapBytes == GroqReadCapBytes {
			t.Fatal("the two constants are equal; this test could not tell them apart")
		}
	})

	t.Run("3: the builtin groq carries its cap in the catalog", func(t *testing.T) {
		routed, err := BuildRouteProviderWithRegistry(builtin, RouteEntry{Provider: "groq", Model: "openai/gpt-oss-120b"})
		if err != nil {
			t.Fatal(err)
		}
		if got := capOf(t, routed); got != GroqReadCapBytes {
			t.Errorf("route cap = %d, want GroqReadCapBytes %d", got, GroqReadCapBytes)
		}
		standalone, err := BuildStandaloneProviderWithRegistry(builtin, "groq", "", "")
		if err != nil {
			t.Fatal(err)
		}
		if got := capOf(t, standalone); got != GroqReadCapBytes {
			t.Errorf("standalone cap = %d, want GroqReadCapBytes %d", got, GroqReadCapBytes)
		}
	})
}
