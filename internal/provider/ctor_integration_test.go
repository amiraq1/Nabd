package provider_test

import (
	"path/filepath"
	"testing"

	"nabd/internal/provider"
	"nabd/internal/registry"
)

// Every provider must refuse to build without a key, and must carry the key
// when one is present. The check now goes through the registry, which is the
// only path left after the per-provider constructors were deleted.
func TestConstructorsRequireKeys(t *testing.T) {
	dir := t.TempDir()
	provPath := filepath.Join(dir, "providers.json")
	authPath := filepath.Join(dir, "auth.json")

	cases := []struct {
		id     string
		envKey string
	}{
		{"anthropic", "ANTHROPIC_API_KEY"},
		{"openrouter", "OPENROUTER_API_KEY"},
		{"groq", "GROQ_API_KEY"},
		{"nvidia", "NVIDIA_API_KEY"},
	}

	for _, c := range cases {
		t.Run(c.id, func(t *testing.T) {
			// 1. Without a key anywhere, building must fail.
			bare, err := registry.LoadFromFiles(provPath, authPath, func(string) string { return "" })
			if err != nil {
				t.Fatal(err)
			}
			if _, err := provider.BuildStandaloneProviderWithRegistry(bare, c.id, "", ""); err == nil {
				t.Fatalf("expected an error without %s, got nil", c.envKey)
			}

			// 2. With the key, building must succeed and carry it.
			testKey := "test-key-" + c.id
			reg, err := registry.LoadFromFiles(provPath, authPath, func(k string) string {
				if k == c.envKey {
					return testKey
				}
				return ""
			})
			if err != nil {
				t.Fatal(err)
			}
			p, err := provider.BuildStandaloneProviderWithRegistry(reg, c.id, "", "")
			if err != nil {
				t.Fatalf("unexpected error with key: %v", err)
			}
			if got := keyOf(t, p); got != testKey {
				t.Errorf("expected key %q, got %q", testKey, got)
			}
		})
	}
}

func keyOf(t *testing.T, p provider.Provider) string {
	t.Helper()
	switch v := p.(type) {
	case *provider.Anthropic:
		return v.Key
	case *provider.OpenAICompat:
		return v.Key
	}
	t.Fatalf("unexpected provider type %T", p)
	return ""
}
