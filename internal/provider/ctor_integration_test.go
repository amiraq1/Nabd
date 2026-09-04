package provider_test

import (
	"os"
	"testing"

	"nabd/internal/provider"
)

func TestConstructorsRequireKeys(t *testing.T) {
	cases := []struct {
		name   string
		envKey string
		ctor   func() (provider.Provider, error)
		getKey func(provider.Provider) string
	}{
		{"Anthropic", "ANTHROPIC_API_KEY", func() (provider.Provider, error) { return provider.NewAnthropic() }, func(p provider.Provider) string { return p.(*provider.Anthropic).Key }},
		{"OpenRouter", "OPENROUTER_API_KEY", func() (provider.Provider, error) { return provider.NewOpenRouter() }, func(p provider.Provider) string { return p.(*provider.OpenAICompat).Key }},
		{"Groq", "GROQ_API_KEY", func() (provider.Provider, error) { return provider.NewGroq() }, func(p provider.Provider) string { return p.(*provider.OpenAICompat).Key }},
		{"NVIDIA", "NVIDIA_API_KEY", func() (provider.Provider, error) { return provider.NewNVIDIA() }, func(p provider.Provider) string { return p.(*provider.OpenAICompat).Key }},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			os.Clearenv()

			// 1. Without key, must error
			_, err := c.ctor()
			if err == nil {
				t.Fatalf("expected error without %s, got nil", c.envKey)
			}

			// 2. With key, must succeed and carry the key
			testKey := "test-key-" + c.name
			os.Setenv(c.envKey, testKey)
			defer os.Unsetenv(c.envKey)

			p, err := c.ctor()
			if err != nil {
				t.Fatalf("unexpected error with key: %v", err)
			}

			gotKey := c.getKey(p)
			if gotKey != testKey {
				t.Errorf("expected key %q, got %q", testKey, gotKey)
			}
		})
	}
}
