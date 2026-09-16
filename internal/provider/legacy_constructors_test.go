package provider

import (
	"path/filepath"
	"testing"

	"nabd/internal/registry"
)

// TestLegacyConstructorsAreGoneAndRoutesStillBuild is the backward-compatibility
// proof for deleting the per-provider constructors. With no
// providers.json at all — a pure legacy environment configuration — the registry
// still builds the nvidia, groq, and openrouter routes, and the endpoint, default
// model, and key arrive exactly as the deleted constructors delivered them. The
// standalone (non-router) path preserves the same three facts plus the retry
// policy those constructors set.
func TestLegacyConstructorsAreGoneAndRoutesStillBuild(t *testing.T) {
	dir := t.TempDir()
	env := map[string]string{
		"NVIDIA_API_KEY":     "nv-legacy",
		"GROQ_API_KEY":       "gq-legacy",
		"OPENROUTER_API_KEY": "or-legacy",
	}
	reg, err := registry.LoadFromFiles(
		filepath.Join(dir, "providers.json"), // deliberately absent
		filepath.Join(dir, "auth.json"),      // deliberately absent
		func(k string) string { return env[k] },
	)
	if err != nil {
		t.Fatalf("LoadFromFiles: %v", err)
	}

	cases := []struct {
		provider string
		model    string
		baseURL  string
		key      string
	}{
		{"nvidia", "moonshotai/kimi-k2.6", "https://integrate.api.nvidia.com/v1", "nv-legacy"},
		{"groq", "openai/gpt-oss-120b", "https://api.groq.com/openai/v1", "gq-legacy"},
		{"openrouter", "anthropic/claude-3.5-haiku", "https://openrouter.ai/api/v1", "or-legacy"},
	}

	for _, tc := range cases {
		t.Run(tc.provider, func(t *testing.T) {
			check := func(kind string, p Provider) {
				o, ok := p.(*OpenAICompat)
				if !ok {
					t.Fatalf("%s: provider type %T, want *OpenAICompat", kind, p)
				}
				if o.BaseURL != tc.baseURL {
					t.Errorf("%s: BaseURL = %q, want %q", kind, o.BaseURL, tc.baseURL)
				}
				if o.Model != tc.model {
					t.Errorf("%s: Model = %q, want the default %q", kind, o.Model, tc.model)
				}
				if o.Key != tc.key {
					t.Errorf("%s: key %q was not carried from the legacy environment", kind, o.Key)
				}
			}

			routed, err := BuildRouteProviderWithRegistry(reg, RouteEntry{Provider: tc.provider, Model: tc.model})
			if err != nil {
				t.Fatalf("route %s: %v", tc.provider, err)
			}
			check("route", routed)

			standalone, err := BuildStandaloneProviderWithRegistry(reg, tc.provider, "", "")
			if err != nil {
				t.Fatalf("standalone %s: %v", tc.provider, err)
			}
			check("standalone", standalone)
			if o := standalone.(*OpenAICompat); o.retryPolicy != RetryStandalone {
				t.Errorf("standalone %s: retryPolicy = %v, want RetryStandalone", tc.provider, o.retryPolicy)
			}
		})
	}
}
