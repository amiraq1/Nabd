package registry

import (
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestBuiltinDefaultsAreDeclaredNotGuessed(t *testing.T) {
	cat := BuiltinCatalog()
	for providerID, provider := range cat {
		if provider.DefaultModel == "" {
			t.Errorf("provider %q: empty DefaultModel", providerID)
		}
		model, ok := provider.Models[provider.DefaultModel]
		if !ok {
			t.Errorf("provider %q: DefaultModel %q is not declared in Models", providerID, provider.DefaultModel)
		} else if model.ID == "" || model.Name == "" {
			t.Errorf("provider %q default model %q: ID and Name must be non-empty", providerID, provider.DefaultModel)
		}
		for modelID, model := range provider.Models {
			if model.ID == "" || model.Name == "" {
				t.Errorf("provider %q model %q: ID and Name must be non-empty", providerID, modelID)
			}
		}
		u, err := url.Parse(provider.Options.BaseURL)
		if err != nil || u.Scheme != "https" {
			t.Errorf("provider %q: BaseURL %q must use https", providerID, provider.Options.BaseURL)
		}
		if provider.ReadCap <= 0 {
			t.Errorf("provider %q: ReadCap = %d, want positive", providerID, provider.ReadCap)
		}
	}
	if _, ok := cat["groq"].Models["qwen-2.5-32b"]; ok {
		t.Fatal("groq catalog still contains decommissioned qwen-2.5-32b")
	}
}

func TestBuiltinCatalogCoversLegacyProviders(t *testing.T) {
	cat := BuiltinCatalog()
	expected := []string{"anthropic", "groq", "openrouter", "nvidia"}
	for _, id := range expected {
		p, ok := cat[id]
		if !ok {
			t.Fatalf("builtin catalog missing legacy provider %q", id)
		}
		if p.API == "" {
			t.Errorf("provider %q: empty API dialect", id)
		}
		if p.Options.BaseURL == "" {
			t.Errorf("provider %q: empty baseURL", id)
		}
		if len(p.Models) == 0 {
			t.Errorf("provider %q: has no default models", id)
		}
		if LegacyEnvKey(id) == "" {
			t.Errorf("provider %q: missing legacy env key mapping", id)
		}
	}

	if cat["groq"].ReadCap != GroqReadCapBytes {
		t.Errorf("groq ReadCap = %d, want %d", cat["groq"].ReadCap, GroqReadCapBytes)
	}
	if cat["anthropic"].API != "anthropic" {
		t.Errorf("anthropic API = %q, want 'anthropic'", cat["anthropic"].API)
	}
	if cat["groq"].API != "openai" {
		t.Errorf("groq API = %q, want 'openai'", cat["groq"].API)
	}
}

func TestRegistryFileOverridesBuiltin(t *testing.T) {
	tmp := t.TempDir()
	provPath := filepath.Join(tmp, "providers.json")

	content := `{
  "provider": {
    "groq": {
      "api": "openai",
      "name": "Custom Groq",
      "options": { "baseURL": "https://custom.groq.internal/openai/v1" },
      "readCap": 5000,
      "models": {
        "custom-qwen": { "name": "Custom Qwen", "id": "qwen-custom-wire" }
      }
    },
    "novita": {
      "api": "openai",
      "name": "Novita AI",
      "options": { "baseURL": "https://api.novita.ai/openai/v1" }
    }
  }
}`
	if err := os.WriteFile(provPath, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	reg, err := LoadFromFiles(provPath, "", nil)
	if err != nil {
		t.Fatalf("LoadFromFiles failed: %v", err)
	}

	groq, ok := reg.Get("groq")
	if !ok {
		t.Fatal("groq provider not found")
	}
	if groq.Source != "providers.json" {
		t.Errorf("groq Source = %q, want 'providers.json'", groq.Source)
	}
	if groq.BaseURL != "https://custom.groq.internal/openai/v1" {
		t.Errorf("groq BaseURL = %q, want custom URL", groq.BaseURL)
	}
	if groq.ReadCap != 5000 {
		t.Errorf("groq ReadCap = %d, want 5000", groq.ReadCap)
	}
	if _, ok := groq.Models["custom-qwen"]; !ok {
		t.Error("groq missing custom-qwen model")
	}

	novita, ok := reg.Get("novita")
	if !ok {
		t.Fatal("novita provider not found")
	}
	if novita.Source != "providers.json" {
		t.Errorf("novita Source = %q, want 'providers.json'", novita.Source)
	}

	anthropic, ok := reg.Get("anthropic")
	if !ok {
		t.Fatal("anthropic builtin provider not found")
	}
	if anthropic.Source != "builtin" {
		t.Errorf("anthropic Source = %q, want 'builtin'", anthropic.Source)
	}
}

func TestRegistryRejectsUnknownDialect(t *testing.T) {
	cases := []struct {
		name string
		api  string
	}{
		{"google", "google"},
		{"mistral", "mistral"},
		{"empty", ""},
		{"random", "unknown_proto"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			jsonStr := `{"provider":{"test":{"api":"` + tc.api + `","options":{"baseURL":"https://example.com"}}}}`
			_, err := ParseProvidersData([]byte(jsonStr))
			if err == nil {
				t.Fatalf("expected error for dialect %q, got nil", tc.api)
			}
			if !strings.Contains(err.Error(), "unsupported api dialect") {
				t.Errorf("unexpected error message: %v", err)
			}
		})
	}
}

func TestRegistryRejectsUnknownFields(t *testing.T) {
	cases := []struct {
		name    string
		jsonStr string
	}{
		{
			name:    "top-level unknown",
			jsonStr: `{"provider":{}, "extra_field": "val"}`,
		},
		{
			name:    "provider-level unknown",
			jsonStr: `{"provider":{"p":{"api":"openai","unknown_opt":123}}}`,
		},
		{
			name:    "options unknown",
			jsonStr: `{"provider":{"p":{"api":"openai","options":{"baseURL":"http://x","extra":true}}}}`,
		},
		{
			name:    "models unknown",
			jsonStr: `{"provider":{"p":{"api":"openai","models":{"m":{"id":"wire","undeclared":"x"}}}}}`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParseProvidersData([]byte(tc.jsonStr))
			if err == nil {
				t.Fatalf("expected error for unknown field in %s, got nil", tc.name)
			}
			if !strings.Contains(err.Error(), "unknown field") {
				t.Errorf("expected unknown field error, got: %v", err)
			}
		})
	}
}

func TestRegistryRejectsLiteralKeyInProvidersFile(t *testing.T) {
	cases := []struct {
		name    string
		jsonStr string
	}{
		{
			name:    "apiKey field",
			jsonStr: `{"provider":{"p":{"api":"openai","apiKey":"sk-ant-test12345678"}}}`,
		},
		{
			name:    "key field",
			jsonStr: `{"provider":{"p":{"api":"openai","key":"sk-12345678"}}}`,
		},
		{
			name:    "api_key in options",
			jsonStr: `{"provider":{"p":{"api":"openai","options":{"api_key":"sk-12345"}}}}`,
		},
		{
			name:    "token field",
			jsonStr: `{"provider":{"p":{"api":"openai","token":"my-token"}}}`,
		},
		{
			name:    "secret field",
			jsonStr: `{"provider":{"p":{"api":"openai","secret":"topsecret"}}}`,
		},
		{
			name:    "literal sk key in value",
			jsonStr: `{"provider":{"p":{"api":"openai","name":"sk-ant-literal-in-name"}}}`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParseProvidersData([]byte(tc.jsonStr))
			if err == nil {
				t.Fatalf("expected error rejecting literal key in %s, got nil", tc.name)
			}
			lower := strings.ToLower(err.Error())
			if !strings.Contains(lower, "forbidden") && !strings.Contains(lower, "secrets belong") && !strings.Contains(lower, "auth.json") {
				t.Errorf("error did not cite forbidden secret / auth.json: %v", err)
			}
		})
	}
}

func TestAuthFileRejectsOpenPermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("permission bits not enforced on windows")
	}

	tmp := t.TempDir()
	authPath := filepath.Join(tmp, "auth.json")
	if err := os.WriteFile(authPath, []byte(`{"anthropic":{"type":"api","key":"sk-test"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	_ = os.Chmod(authPath, 0o644)
	if fi, err := os.Stat(authPath); err == nil && fi.Mode().Perm()&0o077 == 0 {
		t.Skip("filesystem or umask does not support loose permission bits")
	}

	_, err := ParseAuthFile(authPath)
	if err == nil {
		t.Fatal("expected error for open permissions 0644, got nil")
	}
	if !strings.Contains(err.Error(), "are open to others; run chmod 600") {
		t.Errorf("unexpected error text: %v", err)
	}
}

func TestAuthKeysAreRedactedInErrors(t *testing.T) {
	secret := "sk-ant-supersecretlivekey987654321"

	// Trigger a JSON syntax error or invalid auth type containing the secret
	badJSON := `{"anthropic": {"type": "unsupported_type", "key": "` + secret + `"}}`
	_, err := ParseAuthData([]byte(badJSON))
	if err == nil {
		t.Fatal("expected error, got nil")
	}

	errStr := err.Error()
	if strings.Contains(errStr, secret) {
		t.Fatalf("raw secret leaked in error message: %q", errStr)
	}

	// Also check redactError helper directly
	customErr := redactError(os.ErrPermission)
	if customErr == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestModelIDAliasIsSentToProvider(t *testing.T) {
	tmp := t.TempDir()
	provPath := filepath.Join(tmp, "providers.json")

	content := `{
  "provider": {
    "azure-openai": {
      "api": "openai",
      "name": "Azure OpenAI",
      "options": { "baseURL": "https://custom.azure.com/openai" },
      "models": {
        "gpt4o": {
          "name": "GPT-4o Production Deployment",
          "id": "prod-deployment-gpt-4o-2024-08-06"
        },
        "gpt4o-mini": {
          "name": "GPT-4o Mini Deployment"
        }
      }
    }
  }
}`
	if err := os.WriteFile(provPath, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	reg, err := LoadFromFiles(provPath, "", nil)
	if err != nil {
		t.Fatalf("LoadFromFiles failed: %v", err)
	}

	// 1. Model with alias ID -> should return wire ID
	wireID, err := reg.ResolveModelID("azure-openai", "gpt4o")
	if err != nil {
		t.Fatalf("ResolveModelID failed: %v", err)
	}
	if wireID != "prod-deployment-gpt-4o-2024-08-06" {
		t.Errorf("wireID = %q, want 'prod-deployment-gpt-4o-2024-08-06'", wireID)
	}

	// 2. Model without alias ID -> should return model key
	wireID2, err := reg.ResolveModelID("azure-openai", "gpt4o-mini")
	if err != nil {
		t.Fatalf("ResolveModelID failed: %v", err)
	}
	if wireID2 != "gpt4o-mini" {
		t.Errorf("wireID = %q, want 'gpt4o-mini'", wireID2)
	}

	// 3. Unlisted model -> should return model key itself
	wireID3, err := reg.ResolveModelID("azure-openai", "unlisted-raw-model")
	if err != nil {
		t.Fatalf("ResolveModelID failed: %v", err)
	}
	if wireID3 != "unlisted-raw-model" {
		t.Errorf("wireID = %q, want 'unlisted-raw-model'", wireID3)
	}
}

func TestMigrateFromV1ConfigPreservesBackup(t *testing.T) {
	tmp := t.TempDir()
	configPath := filepath.Join(tmp, "config")
	envPath := filepath.Join(tmp, "env")

	secret := "sk-ant-test12345678abcdef"
	configContent := "ANTHROPIC_API_KEY=" + secret + "\nNABD_MODEL=claude-sonnet-5\nNABD_PROVIDER=anthropic\n"
	envContent := "GROQ_API_KEY=gsk_test12345678abcdef\n"

	if err := os.WriteFile(configPath, []byte(configContent), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(envPath, []byte(envContent), 0o600); err != nil {
		t.Fatal(err)
	}

	summary, err := Migrate(tmp)
	if err != nil {
		t.Fatalf("Migrate failed: %v", err)
	}

	// 1. Verify config.bak exists and matches original config
	bakPath := filepath.Join(tmp, "config.bak")
	bakBytes, err := os.ReadFile(bakPath)
	if err != nil {
		t.Fatalf("config.bak not found: %v", err)
	}
	if string(bakBytes) != configContent {
		t.Errorf("config.bak content mismatch: got %q, want %q", string(bakBytes), configContent)
	}

	// 2. Verify original config was NOT deleted
	origBytes, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("original config was deleted: %v", err)
	}
	if string(origBytes) != configContent {
		t.Errorf("original config mutated: got %q, want %q", string(origBytes), configContent)
	}

	// 3. Verify env.bak exists and matches original env
	envBakPath := filepath.Join(tmp, "env.bak")
	envBakBytes, err := os.ReadFile(envBakPath)
	if err != nil {
		t.Fatalf("env.bak not found: %v", err)
	}
	if string(envBakBytes) != envContent {
		t.Errorf("env.bak content mismatch: got %q, want %q", string(envBakBytes), envContent)
	}

	// 4. Verify auth.json exists with 0600 mode and correct keys
	authPath := filepath.Join(tmp, "auth.json")
	fi, err := os.Stat(authPath)
	if err != nil {
		t.Fatalf("auth.json not found: %v", err)
	}
	if runtime.GOOS != "windows" && fi.Mode().Perm()&0o077 != 0 {
		t.Errorf("auth.json permissions %04o are wider than 0600", fi.Mode().Perm())
	}
	authFile, err := ParseAuthFile(authPath)
	if err != nil {
		t.Fatalf("failed to parse auth.json: %v", err)
	}
	if authFile["anthropic"].Key != secret {
		t.Errorf("auth.json anthropic key mismatch: got %q, want %q", authFile["anthropic"].Key, secret)
	}
	if authFile["groq"].Key != "gsk_test12345678abcdef" {
		t.Errorf("auth.json groq key mismatch: got %q, want %q", authFile["groq"].Key, "gsk_test12345678abcdef")
	}

	// 5. Verify providers.json exists with 0600 mode
	provPath := filepath.Join(tmp, "providers.json")
	pFi, err := os.Stat(provPath)
	if err != nil {
		t.Fatalf("providers.json not found: %v", err)
	}
	if runtime.GOOS != "windows" && pFi.Mode().Perm()&0o077 != 0 {
		t.Errorf("providers.json permissions %04o are wider than 0600", pFi.Mode().Perm())
	}

	// 6. Verify summary does not contain the secret
	summaryStr := summary.String()
	if strings.Contains(summaryStr, secret) {
		t.Fatalf("migration summary leaked raw secret: %q", summaryStr)
	}
	if strings.Contains(summaryStr, "gsk_test12345678abcdef") {
		t.Fatalf("migration summary leaked groq secret: %q", summaryStr)
	}
}

func TestRegistryPrecedenceIsDocumented(t *testing.T) {
	// 1. Documentation must exist and clearly state precedence rules
	if PrecedenceDocumentation == "" {
		t.Fatal("PrecedenceDocumentation is empty")
	}
	if !strings.Contains(PrecedenceDocumentation, "providers.json > Builtin Catalog") {
		t.Errorf("documentation missing 'providers.json > Builtin Catalog': %s", PrecedenceDocumentation)
	}
	if !strings.Contains(PrecedenceDocumentation, "auth.json > Legacy Environment Variables") {
		t.Errorf("documentation missing 'auth.json > Legacy Environment Variables': %s", PrecedenceDocumentation)
	}

	// 2. Behavioral verification of Rule A: providers.json > Builtin Catalog
	tmp := t.TempDir()
	provPath := filepath.Join(tmp, "providers.json")
	authPath := filepath.Join(tmp, "auth.json")

	provContent := `{
  "provider": {
    "groq": {
      "api": "openai",
      "name": "Groq Overridden",
      "options": { "baseURL": "https://overridden.groq.com/v1" }
    }
  }
}`
	if err := os.WriteFile(provPath, []byte(provContent), 0o600); err != nil {
		t.Fatal(err)
	}

	authContent := `{
  "groq": { "type": "api", "key": "key-from-auth" }
}`
	if err := os.WriteFile(authPath, []byte(authContent), 0o600); err != nil {
		t.Fatal(err)
	}

	mockEnv := map[string]string{
		"GROQ_API_KEY":      "key-from-env",
		"ANTHROPIC_API_KEY": "anthropic-key-from-env",
	}

	reg, err := LoadFromFiles(provPath, authPath, func(k string) string {
		return mockEnv[k]
	})
	if err != nil {
		t.Fatalf("LoadFromFiles failed: %v", err)
	}

	// Groq definition was overridden by providers.json
	groq, ok := reg.Get("groq")
	if !ok {
		t.Fatal("groq not found")
	}
	if groq.BaseURL != "https://overridden.groq.com/v1" {
		t.Errorf("groq baseURL = %q, want overridden URL", groq.BaseURL)
	}
	if groq.Source != "providers.json" {
		t.Errorf("groq source = %q, want 'providers.json'", groq.Source)
	}

	// Anthropic was NOT in providers.json -> falls back to builtin catalog
	anthropic, ok := reg.Get("anthropic")
	if !ok {
		t.Fatal("anthropic not found")
	}
	if anthropic.Source != "builtin" {
		t.Errorf("anthropic source = %q, want 'builtin'", anthropic.Source)
	}

	// 3. Behavioral verification of Rule B: auth.json > legacy env
	// Groq has key in auth.json ("key-from-auth") AND in env ("key-from-env") -> auth.json MUST win
	if groq.Key != "key-from-auth" {
		t.Errorf("groq key = %q, want 'key-from-auth' (auth.json must override env)", groq.Key)
	}
	if groq.KeySource != "auth.json" {
		t.Errorf("groq keySource = %q, want 'auth.json'", groq.KeySource)
	}

	// Anthropic has NO entry in auth.json, but has key in env -> env fallback MUST be used
	if anthropic.Key != "anthropic-key-from-env" {
		t.Errorf("anthropic key = %q, want 'anthropic-key-from-env' (fallback to env)", anthropic.Key)
	}
	if anthropic.KeySource != "env" {
		t.Errorf("anthropic keySource = %q, want 'env'", anthropic.KeySource)
	}

	// Provider with neither auth.json nor env -> Key is empty
	nvidia, ok := reg.Get("nvidia")
	if !ok {
		t.Fatal("nvidia not found")
	}
	if nvidia.Key != "" {
		t.Errorf("nvidia key = %q, want empty", nvidia.Key)
	}
	if nvidia.KeySource != "none" {
		t.Errorf("nvidia keySource = %q, want 'none'", nvidia.KeySource)
	}
}
