package provider

import "testing"

// Every constructor must carry its key into the client. NewOpenRouter once
// lost `Key: k` in a merge and every request answered 401; this pins it.
func TestConstructorsCarryKey(t *testing.T) {
	t.Setenv("NABD_CONFIG", t.TempDir()+"/none")
	t.Setenv("OPENROUTER_API_KEY", "or-k")
	t.Setenv("NVIDIA_API_KEY", "nv-k")
	t.Setenv("GROQ_API_KEY", "gq-k")
	t.Setenv("ANTHROPIC_API_KEY", "an-k")

	or, err := NewOpenRouter()
	if err != nil || or.Key != "or-k" {
		t.Errorf("openrouter: key=%q err=%v", or.Key, err)
	}
	nv, err := NewNVIDIA()
	if err != nil || nv.Key != "nv-k" {
		t.Errorf("nvidia: key=%q err=%v", nv.Key, err)
	}
	if gq := NewGroq(); gq.Key != "gq-k" {
		t.Errorf("groq: key=%q", gq.Key)
	}
	an, err := NewAnthropic()
	if err != nil || an.Key != "an-k" {
		t.Errorf("anthropic: key=%q err=%v", an.Key, err)
	}
}

func TestDefaultMaxTokens(t *testing.T) {
	t.Setenv("NABD_CONFIG", t.TempDir()+"/none")
	t.Setenv("NABD_MAX_TOKENS", "")
	if got := DefaultMaxTokens(); got != 2048 {
		t.Errorf("default = %d, want 2048", got)
	}
	t.Setenv("NABD_MAX_TOKENS", "1024")
	if got := DefaultMaxTokens(); got != 1024 {
		t.Errorf("override = %d, want 1024", got)
	}
	t.Setenv("NABD_MAX_TOKENS", "10") // below floor: ignored
	if got := DefaultMaxTokens(); got != 2048 {
		t.Errorf("floor = %d, want 2048", got)
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
