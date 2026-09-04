// Package provider: newroute.go — centralized route construction for the
// router path. This is the ONLY place that calls config.Get() for route
// building; all provider constructors receive values explicitly (F14, X12).
//
// The key env-var names for each provider:
//
//	anthropic  → ANTHROPIC_API_KEY
//	groq       → GROQ_API_KEY
//	openrouter → OPENROUTER_API_KEY
//	nvidia     → NVIDIA_API_KEY
//
// NABD_BASE_URL is rejected in router mode (F12); per-provider defaults
// are used instead. NABD_MODEL is ignored in router mode (F11); model
// comes from the NABD_ROUTES entry.
package provider

import (
	"context"
	"fmt"
	"strings"

	"nabd/internal/config"
)

// routeKeyVarNames maps provider name → the config key that holds its API key.
var routeKeyVarNames = map[string]string{
	"anthropic":  "ANTHROPIC_API_KEY",
	"groq":       "GROQ_API_KEY",
	"openrouter": "OPENROUTER_API_KEY",
	"nvidia":     "NVIDIA_API_KEY",
}

// BuildRouteProvider constructs the concrete Provider for a single RouteEntry.
// It reads the required API key from config exactly once and passes it
// explicitly to the provider constructor — never via os.Setenv or globals.
//
// If the key is absent, the error message names the variable but never its
// value (F9, N security policy).
func BuildRouteProvider(entry RouteEntry) (Provider, error) {
	keyVar, ok := routeKeyVarNames[entry.Provider]
	if !ok {
		// Defensive: ParseRoutes already validates against allowedProviderSet,
		// so this path should not be reachable in production.
		return nil, fmt.Errorf(
			"route provider %q: not in supported set (%s)",
			entry.Provider, strings.Join(AllowedProviders, ", "))
	}

	key := config.Get(keyVar)
	if key == "" {
		return nil, fmt.Errorf(
			"route provider %q: %s is not set — add it to ~/.ag/config or the environment",
			entry.Provider, keyVar)
	}

	switch entry.Provider {
	case "anthropic":
		return NewAnthropicForRoute(entry.Model, key)
	case "groq", "openrouter", "nvidia":
		return NewOpenAICompatForRoute(entry.Provider, entry.Model, key, "")
	default:
		return nil, fmt.Errorf("route provider %q: no constructor registered", entry.Provider)
	}
}

// AsSingleAttempt ensures p satisfies SingleAttempt.
func AsSingleAttempt(p Provider) SingleAttempt {
	if sa, ok := p.(SingleAttempt); ok {
		return sa
	}
	return &singleAttemptAdapter{p: p}
}

type singleAttemptAdapter struct {
	p Provider
}

func (s *singleAttemptAdapter) Start(ctx context.Context, req Request) (<-chan Chunk, error) {
	return s.p.Stream(ctx, req)
}

func (s *singleAttemptAdapter) Name() string {
	return s.p.Name()
}

// BuildRoute constructs a Route with its SingleAttempt client for a single RouteEntry.
func BuildRoute(entry RouteEntry) (Route, error) {
	prov, err := BuildRouteProvider(entry)
	if err != nil {
		return Route{}, err
	}
	return Route{
		Provider: entry.Provider,
		Model:    entry.Model,
		Client:   AsSingleAttempt(prov),
	}, nil
}

// ValidateRouteKeys checks that every configured route has a non-empty API key.
// It returns a combined error listing all missing keys (no values exposed).
// Call this at startup after ParseRoutes and before building the Router.
func ValidateRouteKeys(routes []RouteEntry) error {
	var missing []string
	seen := make(map[string]struct{}) // avoid duplicate error messages for same provider
	for _, r := range routes {
		keyVar, ok := routeKeyVarNames[r.Provider]
		if !ok {
			continue
		}
		if _, already := seen[r.Provider]; already {
			continue
		}
		if config.Get(keyVar) == "" {
			missing = append(missing, fmt.Sprintf("%s (%s)", r.Provider, keyVar))
			seen[r.Provider] = struct{}{}
		}
	}
	if len(missing) == 0 {
		return nil
	}
	return fmt.Errorf(
		"router: missing API keys for %d provider(s): %s — add them to ~/.ag/config",
		len(missing), strings.Join(missing, ", "))
}
