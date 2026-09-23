package provider

import (
	"context"
	"errors"
	"strings"
	"testing"

	"nabd/internal/endpoint"
	"nabd/internal/registry"
)

// TestCustomEndpointIsAcceptedByDesign pins the v11 policy: endpoints in
// providers.json are accepted only if they pass the active NABD_ENDPOINT_POLICY.
// Under the default PolicyStrict, http:// and non-public addresses are refused
// at load time. Under PolicyLoopback, loopback and RFC 1918 are accepted but
// http is still refused unless the address is local. PolicyOpen disables all
// checks.
//
// BREAKING CHANGE from v10: providers.json now refuses plaintext http and
// non-public addresses by default. Set NABD_ENDPOINT_POLICY=loopback for a
// local runtime such as ollama.
func TestCustomEndpointIsAcceptedByDesign(t *testing.T) {
	// Under PolicyLoopback (NABD_ENDPOINT_POLICY=loopback), local runtimes with
	// HTTPS are accepted including loopback, private, and .internal hosts.
	t.Run("loopback_policy_allows_private_https", func(t *testing.T) {
		t.Setenv("NABD_ENDPOINT_POLICY", "loopback")
		dir := t.TempDir()
		provPath := writeRegistryFile(t, dir, "providers.json", `{
  "provider": {
    "local-ollama": {
      "api": "openai",
      "name": "Local Ollama",
      "options": { "baseURL": "https://127.0.0.1:11434/v1" },
      "models": { "llama3.3:70b": { "name": "Llama 3.3 70B" } }
    },
    "private-gw": {
      "api": "openai",
      "name": "Private gateway",
      "options": { "baseURL": "https://10.0.0.7:8000/v1" },
      "models": { "m": { "name": "M" } }
    },
    "suffixed": {
      "api": "anthropic",
      "name": "Internal suffix",
      "options": { "baseURL": "https://gw.internal/v1" },
      "models": { "c": { "name": "C" } }
    }
  }
}`)
		authPath := writeRegistryFile(t, dir, "auth.json", `{
  "local-ollama": { "type": "api", "key": "sk-loopback-test" },
  "private-gw":   { "type": "api", "key": "sk-private-test" },
  "suffixed":     { "type": "api", "key": "sk-suffixed-test" }
}`)

		reg, err := registry.LoadFromFiles(provPath, authPath, func(string) string { return "" })
		if err != nil {
			t.Fatalf("LoadFromFiles under loopback policy: %v", err)
		}

		for _, tc := range []struct {
			provider string
			model    string
			wantURL  string
		}{
			{"local-ollama", "llama3.3:70b", "https://127.0.0.1:11434/v1"},
			{"private-gw", "m", "https://10.0.0.7:8000/v1"},
			{"suffixed", "c", "https://gw.internal/v1"},
		} {
			p, err := BuildRouteProviderWithRegistry(reg, RouteEntry{Provider: tc.provider, Model: tc.model})
			if err != nil {
				t.Fatalf("%s: loopback policy should accept declared endpoint, got: %v", tc.provider, err)
			}
			var got string
			switch c := p.(type) {
			case *OpenAICompat:
				got = c.BaseURL
			case *Anthropic:
				got = c.BaseURL
			default:
				t.Fatalf("%s: unexpected provider type %T", tc.provider, p)
			}
			if got != tc.wantURL {
				t.Errorf("%s: BaseURL = %q, want %q", tc.provider, got, tc.wantURL)
			}
		}
	})

	// Under PolicyStrict (default), plaintext http:// is rejected at load time.
	t.Run("strict_policy_refuses_plaintext_http", func(t *testing.T) {
		t.Setenv("NABD_ENDPOINT_POLICY", "strict")
		dir := t.TempDir()
		provPath := writeRegistryFile(t, dir, "providers.json", `{
  "provider": {
    "bad": {
      "api": "openai",
      "name": "Bad",
      "options": { "baseURL": "http://api.example.com/v1" },
      "models": { "m": { "name": "M" } }
    }
  }
}`)
		authPath := writeRegistryFile(t, dir, "auth.json", `{"bad": {"type": "api", "key": "sk-bad-test"}}`)
		_, err := registry.LoadFromFiles(provPath, authPath, func(string) string { return "" })
		if err == nil {
			t.Fatal("expected LoadFromFiles to fail for http:// under strict policy")
		}
		if !strings.Contains(err.Error(), "https") {
			t.Errorf("error %q does not name the https requirement", err)
		}
	})

	// Under PolicyOpen, any endpoint is accepted unconditionally.
	t.Run("open_policy_accepts_all_endpoints", func(t *testing.T) {
		t.Setenv("NABD_ENDPOINT_POLICY", "open")
		dir := t.TempDir()
		provPath := writeRegistryFile(t, dir, "providers.json", `{
  "provider": {
    "local": {
      "api": "openai",
      "name": "Local",
      "options": { "baseURL": "http://127.0.0.1:11434/v1" },
      "models": { "m": { "name": "M" } }
    }
  }
}`)
		authPath := writeRegistryFile(t, dir, "auth.json", `{"local": {"type": "api", "key": "sk-local-test"}}`)
		reg, err := registry.LoadFromFiles(provPath, authPath, func(string) string { return "" })
		if err != nil {
			t.Fatalf("LoadFromFiles under open policy: %v", err)
		}
		_, err = BuildRouteProviderWithRegistry(reg, RouteEntry{Provider: "local", Model: "m"})
		if err != nil {
			t.Fatalf("BuildRouteProviderWithRegistry under open policy: %v", err)
		}
	})

	// An absent provider must fail with a configuration error, never with an
	// endpoint-scheme or host-range error.
	t.Run("absent_provider_error_names_config_file", func(t *testing.T) {
		t.Setenv("NABD_ENDPOINT_POLICY", "strict")
		dir := t.TempDir()
		provPath := writeRegistryFile(t, dir, "providers.json", `{
  "provider": {
    "present": {
      "api": "openai",
      "name": "Present",
      "options": { "baseURL": "https://api.example.com/v1" },
      "models": { "m": { "name": "M" } }
    }
  }
}`)
		authPath := writeRegistryFile(t, dir, "auth.json", `{"present": {"type": "api", "key": "sk-present-test"}}`)
		reg, err := registry.LoadFromFiles(provPath, authPath, func(string) string { return "" })
		if err != nil {
			t.Fatalf("LoadFromFiles: %v", err)
		}
		_, err = BuildRouteProviderWithRegistry(reg, RouteEntry{Provider: "absent", Model: "m"})
		if err == nil {
			t.Fatal("expected an error for an unconfigured provider")
		}
		for _, forbidden := range []string{"loopback address", "private network", "rebinding"} {
			if strings.Contains(strings.ToLower(err.Error()), forbidden) {
				t.Errorf("error %q must not claim an endpoint rule (%q); expected a config error", err, forbidden)
			}
		}
	})

	// NABD_BASE_URL override in BuildStandaloneProviderWithRegistry must also be validated
	// against the active endpoint policy.
	t.Run("standalone_base_override_refuses_plaintext_http", func(t *testing.T) {
		t.Setenv("NABD_ENDPOINT_POLICY", "strict")
		dir := t.TempDir()
		provPath := writeRegistryFile(t, dir, "providers.json", `{
  "provider": {
    "pub": {
      "api": "openai",
      "name": "Public",
      "options": { "baseURL": "https://api.example.com/v1" },
      "defaultModel": "m",
      "models": { "m": { "name": "M" } }
    }
  }
}`)
		authPath := writeRegistryFile(t, dir, "auth.json", `{"pub": {"type": "api", "key": "sk-test"}}`)
		reg, err := registry.LoadFromFiles(provPath, authPath, func(string) string { return "" })
		if err != nil {
			t.Fatalf("LoadFromFiles: %v", err)
		}

		// Plaintext http in baseOverride must fail
		_, err = BuildStandaloneProviderWithRegistry(reg, "pub", "", "http://attacker.example/v1")
		if err == nil {
			t.Fatal("expected error for plaintext http in NABD_BASE_URL override under strict policy")
		}
		if !strings.Contains(err.Error(), "https") {
			t.Errorf("error %q does not name https requirement", err)
		}

		// Private IP in baseOverride must fail under strict policy
		_, err = BuildStandaloneProviderWithRegistry(reg, "pub", "", "https://10.0.0.1:8000/v1")
		if err == nil {
			t.Fatal("expected error for private address in NABD_BASE_URL override under strict policy")
		}

		// Public HTTPS in baseOverride succeeds
		p, err := BuildStandaloneProviderWithRegistry(reg, "pub", "", "https://other.example.com/v1")
		if err != nil {
			t.Fatalf("unexpected error for valid public override: %v", err)
		}
		if op, ok := p.(*OpenAICompat); ok {
			if op.BaseURL != "https://other.example.com/v1" {
				t.Errorf("got BaseURL %q, want https://other.example.com/v1", op.BaseURL)
			}
		}
	})
}

func TestConstructorDefaultsToGuardedClient(t *testing.T) {
	t.Setenv("NABD_ENDPOINT_POLICY", "strict")
	t.Setenv("NABD_ENDPOINT_ALLOW", "")

	// 1. OpenAI dialect without injection defaults to a guarded client that
	// refuses connections to private addresses with ErrEndpointRefused.
	op, err := NewOpenAIDialect("custom", "https://127.0.0.1:65432/v1", "model", "key", 0)
	if err != nil {
		t.Fatalf("NewOpenAIDialect: %v", err)
	}
	if op.Client == nil || op.Client.Transport == nil {
		t.Fatal("NewOpenAIDialect did not default to a guarded client with transport")
	}

	ch, err := op.Stream(context.Background(), Request{
		Messages: []Message{{Role: User, Text: "ping"}},
	})
	if err != nil {
		if !errors.Is(err, endpoint.ErrEndpointRefused) {
			t.Fatalf("NewOpenAIDialect Stream immediate err = %v, want ErrEndpointRefused", err)
		}
	} else {
		var refused bool
		for chunk := range ch {
			if chunk.Kind == ChunkError && errors.Is(chunk.Err, endpoint.ErrEndpointRefused) {
				refused = true
				break
			}
		}
		if !refused {
			t.Fatal("NewOpenAIDialect Stream did not return ErrEndpointRefused when connecting to private address")
		}
	}

	// 2. Anthropic dialect without injection defaults to a guarded client that
	// refuses connections to private addresses with ErrEndpointRefused.
	ap, err := NewAnthropicDialect("custom", "https://127.0.0.1:65432/v1", "model", "key", 0)
	if err != nil {
		t.Fatalf("NewAnthropicDialect: %v", err)
	}
	if ap.Client == nil || ap.Client.Transport == nil {
		t.Fatal("NewAnthropicDialect did not default to a guarded client with transport")
	}

	ach, err := ap.Stream(context.Background(), Request{
		Messages: []Message{{Role: User, Text: "ping"}},
	})
	if err != nil {
		if !errors.Is(err, endpoint.ErrEndpointRefused) {
			t.Fatalf("NewAnthropicDialect Stream immediate err = %v, want ErrEndpointRefused", err)
		}
	} else {
		var refused bool
		for chunk := range ach {
			if chunk.Kind == ChunkError && errors.Is(chunk.Err, endpoint.ErrEndpointRefused) {
				refused = true
				break
			}
		}
		if !refused {
			t.Fatal("NewAnthropicDialect Stream did not return ErrEndpointRefused when connecting to private address")
		}
	}
}

func TestConstructorsDefaultToGuardedClient(t *testing.T) {
	TestConstructorDefaultsToGuardedClient(t)
}

func TestErrorKindEndpointRefused(t *testing.T) {
	if got := ErrorKindOf(endpoint.ErrEndpointRefused); got != ErrorKindEndpointRefused {
		t.Fatalf("ErrorKindOf(endpoint.ErrEndpointRefused) = %q, want %q", got, ErrorKindEndpointRefused)
	}
	wrapped := errors.Join(errors.New("dial failed"), endpoint.ErrEndpointRefused)
	if got := ErrorKindOf(wrapped); got != ErrorKindEndpointRefused {
		t.Fatalf("ErrorKindOf(wrapped) = %q, want %q", got, ErrorKindEndpointRefused)
	}
}
