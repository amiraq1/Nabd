// Package provider: newroute.go — centralized route construction for the
// router path. Provider identity, dialect, endpoint, model alias, and read
// ceiling all come from the registry (builtin catalog, then
// ~/.ag/providers.json, then ~/.ag/auth.json, with legacy environment/config
// fallback). This is the ONLY place that turns a RouteEntry into a concrete
// Provider, and every constructor receives its values explicitly (F14, X12).
//
// The dialect decides the constructor: "openai" and "anthropic" are the only
// two languages nabd speaks, and no package is loaded at request time.
package provider

import (
	"context"
	"fmt"
	"strings"

	"nabd/internal/registry"
)

// BuildRouteProvider constructs the concrete Provider for a single RouteEntry
// using the registry loaded from the default user paths.
func BuildRouteProvider(entry RouteEntry) (Provider, error) {
	reg, err := registry.Load()
	if err != nil {
		return nil, err
	}
	return BuildRouteProviderWithRegistry(reg, entry)
}

// BuildRouteProviderWithRegistry is BuildRouteProvider with an explicit
// registry, so a caller can supply one without touching the user's files.
//
// The model key from NABD_ROUTES is looked up in the provider's catalog so a
// configured alias id (the Azure/Bedrock pattern) is what is sent on the wire.
// An unlisted model passes through unchanged, so any model name remains usable
// from NABD_ROUTES alone.
//
// Errors name the requested provider, the providers that are actually
// configured, and the file a new provider belongs in — never a key value.
func BuildRouteProviderWithRegistry(reg *registry.Registry, entry RouteEntry) (Provider, error) {
	prov, ok := reg.Get(entry.Provider)
	if !ok {
		return nil, unknownProviderError(reg, entry.Provider)
	}
	if prov.Key == "" {
		return nil, missingKeyError(reg, prov.ID)
	}

	model := prov.ResolveModelID(entry.Model)
	switch prov.API {
	case "anthropic":
		return NewAnthropicDialect(prov.ID, prov.BaseURL, model, prov.Key, prov.ReadCap)
	case "openai":
		return NewOpenAIDialect(prov.ID, prov.BaseURL, model, prov.Key, prov.ReadCap)
	default:
		return nil, fmt.Errorf(
			"provider %q: unsupported api dialect %q; must be 'openai' or 'anthropic'",
			prov.ID, prov.API)
	}
}

// unknownProviderError names the requested provider, every configured provider,
// and the file the missing one belongs in. It never includes a key.
func unknownProviderError(reg *registry.Registry, name string) error {
	list := strings.Join(reg.ProviderIDs(), ", ")
	if list == "" {
		list = "(none)"
	}
	return fmt.Errorf(
		"route provider %q is not configured; configured providers: %s; add %q to %s",
		name, list, name, reg.ProvidersPath)
}

// missingKeyError names the auth file and, for a builtin provider, the legacy
// variable the key may also come from. It never includes a key.
func missingKeyError(reg *registry.Registry, id string) error {
	if legacy := registry.LegacyEnvKey(id); legacy != "" {
		return fmt.Errorf(
			"route provider %q has no API key — add it to %s or set %s",
			id, reg.AuthPath, legacy)
	}
	return fmt.Errorf(
		"route provider %q has no API key — add it to %s", id, reg.AuthPath)
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

// ReadCapBytes delegates to the wrapped provider, so a route reports its
// provider's ceiling whether or not the adapter had to be introduced.
func (s *singleAttemptAdapter) ReadCapBytes() int {
	if rc, ok := s.p.(ReadCapper); ok {
		return rc.ReadCapBytes()
	}
	return DefaultReadCapBytes
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

// ValidateRouteKeys checks that every configured route resolves to a provider
// with a non-empty API key. It returns a combined error listing all problems
// (never any key value). Call this at startup after ParseRoutes and before
// building the Router.
func ValidateRouteKeys(routes []RouteEntry) error {
	reg, err := registry.Load()
	if err != nil {
		return err
	}
	return ValidateRouteKeysWithRegistry(reg, routes)
}

// ValidateRouteKeysWithRegistry is ValidateRouteKeys with an explicit registry.
func ValidateRouteKeysWithRegistry(reg *registry.Registry, routes []RouteEntry) error {
	var problems []string
	seen := make(map[string]struct{}) // avoid duplicate messages for one provider
	for _, r := range routes {
		if _, done := seen[r.Provider]; done {
			continue
		}
		seen[r.Provider] = struct{}{}
		prov, ok := reg.Get(r.Provider)
		if !ok {
			problems = append(problems, unknownProviderError(reg, r.Provider).Error())
			continue
		}
		if prov.Key == "" {
			problems = append(problems, missingKeyError(reg, prov.ID).Error())
		}
	}
	if len(problems) == 0 {
		return nil
	}
	return fmt.Errorf("router: %s", strings.Join(problems, "; "))
}
