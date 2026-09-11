package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestV2StrictRouterPreservesRouteOrder(t *testing.T) {
	t.Setenv("GROQ_API_KEY", "g-secret")
	t.Setenv("NVIDIA_API_KEY", "n-secret")
	p := filepath.Join(t.TempDir(), "config.v2.json")
	body := `{"version":2,"provider":"router","router_mode":"fallback","routes":[{"provider":"groq","model":"openai/gpt-oss-120b"},{"provider":"nvidia","model":"deepseek-ai/deepseek-v4-pro-0813"}],"credentials":{"groq":{"source":"env"},"nvidia":{"source":"env"}}}`
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := ParseV2File(p)
	if err != nil {
		t.Fatal(err)
	}
	want := "groq:openai/gpt-oss-120b,nvidia:deepseek-ai/deepseek-v4-pro-0813"
	if got["NABD_ROUTES"] != want {
		t.Fatalf("routes=%q want %q", got["NABD_ROUTES"], want)
	}
}

func TestV2RejectsUnknownAndCommandSource(t *testing.T) {
	dir := t.TempDir()
	for name, body := range map[string]string{
		"unknown": `{"version":2,"provider":"groq","priority":1,"credentials":{"groq":{"source":"env"}}}`,
		"command": `{"version":2,"provider":"groq","credentials":{"groq":{"source":"command"}}}`,
	} {
		t.Run(name, func(t *testing.T) {
			p := filepath.Join(dir, name)
			if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := ParseV2File(p); err == nil {
				t.Fatal("invalid v2 accepted")
			}
		})
	}
}

func TestV1AndV2TogetherAreFatal(t *testing.T) {
	dir := t.TempDir()
	v1 := filepath.Join(dir, "v1")
	v2 := filepath.Join(dir, "v2")
	if err := os.WriteFile(v1, []byte("NABD_PROVIDER=groq\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(v2, []byte(`{"version":2}`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(EnvVar, v1)
	t.Setenv(V2EnvVar, v2)
	if _, _, err := SelectedPath(); err == nil {
		t.Fatal("v1 and v2 accepted together")
	}
}

func TestV2DisablesImplicitEnvironmentFallback(t *testing.T) {
	t.Setenv("GROQ_API_KEY", "secret")
	p := filepath.Join(t.TempDir(), "config.v2.json")
	body := `{"version":2,"provider":"anthropic","credentials":{"anthropic":{"source":"env"}}}`
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(V2EnvVar, p)
	ResetForTest()
	t.Cleanup(ResetForTest)
	if err := Load(); err != nil {
		t.Fatal(err)
	}
	if got := Get("GROQ_API_KEY"); got != "" {
		t.Fatal("undeclared env credential leaked into v2")
	}
}

func TestV2CredentialFileAndEndpointPolicy(t *testing.T) {
	dir := t.TempDir()
	secret := filepath.Join(dir, "secret")
	if err := os.WriteFile(secret, []byte("file-secret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, "config.v2.json")
	body := `{"version":2,"provider":"openrouter","base_url":"https://example.com/v1","credentials":{"openrouter":{"source":"file","path":"` + secret + `"}}}`
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := ParseV2File(p)
	if err != nil {
		t.Fatal(err)
	}
	if got["OPENROUTER_API_KEY"] != "file-secret" {
		t.Fatal("credential file not loaded")
	}
	for _, raw := range []string{"http://example.com", "https://127.0.0.1/v1", "https://169.254.169.254/v1", "https://localhost/v1"} {
		if err := ValidateEndpointURL(raw); err == nil {
			t.Fatalf("unsafe URL accepted: %s", raw)
		}
	}
}

func TestV2ErrorsNeverContainCredentialValue(t *testing.T) {
	const sentinel = "never-print-v2-secret"
	t.Setenv("GROQ_API_KEY", sentinel)
	p := filepath.Join(t.TempDir(), "config.v2.json")
	if err := os.WriteFile(p, []byte(`{"version":2,"provider":"router","routes":[],"credentials":{"groq":{"source":"env"}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := ParseV2File(p)
	if err == nil || strings.Contains(err.Error(), sentinel) {
		t.Fatalf("unsafe error: %v", err)
	}
}
