package provider

import (
	"path/filepath"
	"testing"

	"nabd/internal/registry"
)

// Every provider built from the registry must carry its key into the client.
// A constructor once lost `Key: k` in a merge and every request answered 401;
// this pins the same contract for the registry-driven builders that replaced
// the per-provider constructors.
func TestConstructorsCarryKey(t *testing.T) {
	dir := t.TempDir()
	env := map[string]string{
		"ANTHROPIC_API_KEY":  "an-k",
		"GROQ_API_KEY":       "gq-k",
		"OPENROUTER_API_KEY": "or-k",
		"NVIDIA_API_KEY":     "nv-k",
	}
	reg, err := registry.LoadFromFiles(
		filepath.Join(dir, "providers.json"),
		filepath.Join(dir, "auth.json"),
		func(k string) string { return env[k] },
	)
	if err != nil {
		t.Fatalf("LoadFromFiles: %v", err)
	}

	for _, tc := range []struct{ id, want string }{
		{"anthropic", "an-k"},
		{"groq", "gq-k"},
		{"openrouter", "or-k"},
		{"nvidia", "nv-k"},
	} {
		p, err := BuildStandaloneProviderWithRegistry(reg, tc.id, "", "")
		if err != nil {
			t.Fatalf("%s: %v", tc.id, err)
		}
		var got string
		switch v := p.(type) {
		case *Anthropic:
			got = v.Key
		case *OpenAICompat:
			got = v.Key
		default:
			t.Fatalf("%s: unexpected provider type %T", tc.id, p)
		}
		if got != tc.want {
			t.Errorf("%s: key = %q, want %q", tc.id, got, tc.want)
		}
	}
}

func TestDefaultMaxTokens(t *testing.T) {
	t.Setenv("NABD_CONFIG", t.TempDir()+"/none")
	t.Setenv("NABD_MAX_TOKENS", "")
	if got := DefaultMaxTokens(); got != 1024 {
		t.Errorf("default = %d, want 1024 (mirrors agent.maxOutputTokens)", got)
	}
	t.Setenv("NABD_MAX_TOKENS", "2048")
	if got := DefaultMaxTokens(); got != 2048 {
		t.Errorf("override = %d, want 2048", got)
	}
	t.Setenv("NABD_MAX_TOKENS", "10") // below floor: ignored
	if got := DefaultMaxTokens(); got != 1024 {
		t.Errorf("floor = %d, want 1024", got)
	}
	t.Setenv("NABD_MAX_TOKENS", "99999") // above ceiling: ignored
	if got := DefaultMaxTokens(); got != 1024 {
		t.Errorf("ceiling = %d, want 1024", got)
	}
}

func TestKeyVarFollowsHost(t *testing.T) {
	for base, want := range map[string]string{
		"https://integrate.api.nvidia.com/v1": "NVIDIA_API_KEY",
		"https://openrouter.ai/api/v1":        "OPENROUTER_API_KEY",
		"https://api.groq.com/openai/v1":      "GROQ_API_KEY",
		"http://localhost:8080/v1":            "API_KEY",
	} {
		if got := (&OpenAICompat{BaseURL: base}).keyVar(); got != want {
			t.Errorf("%s: %q want %q", base, got, want)
		}
	}
}
