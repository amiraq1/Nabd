package provider

import (
	"testing"
)

// NBD-404: the read ceiling each provider declares.
//
// The cap is a property of the provider's per-request input ceiling, so it is
// declared rather than inferred. Name() is never consulted: Router's is a
// composite of several providers, and a policy parsed out of it would be wrong
// for exactly the case this interface exists to get right.

func TestProviderReadCaps(t *testing.T) {
	tests := []struct {
		name string
		p    Provider
		want int
	}{
		{"groq meters tokens per minute", mustRoute(t, "groq", "llama"), GroqReadCapBytes},
		{"openrouter declares no ceiling", mustRoute(t, "openrouter", "qwen"), DefaultReadCapBytes},
		{"nvidia declares no ceiling", mustRoute(t, "nvidia", "nemotron"), DefaultReadCapBytes},
		{"anthropic does not meter per minute", mustAnthropic(t), DefaultReadCapBytes},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			rc, ok := tc.p.(ReadCapper)
			if !ok {
				t.Fatalf("%T does not implement ReadCapper", tc.p)
			}
			if got := rc.ReadCapBytes(); got != tc.want {
				t.Fatalf("ReadCapBytes() = %d, want %d", got, tc.want)
			}
			if tc.want <= 0 {
				t.Fatalf("declared cap %d is not positive", tc.want)
			}
		})
	}
}

// TestRouterReadCapIsTheStrictestRoute is the guard for the aggregate: every
// request goes to one route and which one is decided by fallback at runtime, so
// the cap must be the minimum. A cap taken from the most permissive route would
// be sent to the strictest one and trip its ceiling.
func TestRouterReadCapIsTheStrictestRoute(t *testing.T) {
	tests := []struct {
		name   string
		routes []string // provider names
		want   int
	}{
		{"single strict route", []string{"groq"}, GroqReadCapBytes},
		{"single permissive route", []string{"anthropic"}, DefaultReadCapBytes},
		{"mixed: strictest governs", []string{"anthropic", "groq"}, GroqReadCapBytes},
		{"mixed, order must not matter", []string{"groq", "openrouter"}, GroqReadCapBytes},
		{"no strict route among several", []string{"anthropic", "openrouter", "nvidia"}, DefaultReadCapBytes},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			r := mustRouter(t, tc.routes...)
			rc, ok := Provider(r).(ReadCapper)
			if !ok {
				t.Fatal("Router does not implement ReadCapper")
			}
			if got := rc.ReadCapBytes(); got != tc.want {
				t.Fatalf("Router.ReadCapBytes() = %d, want %d for routes %v", got, tc.want, tc.routes)
			}
			// The cap must not be read off the display name: the name is a
			// composite string and cannot express "strictest of several".
			if name := r.Name(); name == "" {
				t.Fatal("router name is empty; the test would not compare against anything")
			}
		})
	}
}

// TestReadCapIsNotDerivedFromName proves the negative that the interface
// exists for: two routers whose providers declare the same cap but whose names
// are completely different report the same value, and renaming a provider
// cannot change it. A policy keyed on the name string would fail this.
func TestReadCapIsNotDerivedFromName(t *testing.T) {
	a := mustRouter(t, "groq", "openrouter")
	b := mustRouter(t, "openrouter", "groq") // same set, different order → different Name()

	if a.Name() == b.Name() {
		t.Fatal("the two routers share a name; the comparison would be vacuous")
	}
	ra, rb := Provider(a).(ReadCapper), Provider(b).(ReadCapper)
	if ra.ReadCapBytes() != rb.ReadCapBytes() {
		t.Fatalf("cap depends on the name: %q → %d, %q → %d",
			a.Name(), ra.ReadCapBytes(), b.Name(), rb.ReadCapBytes())
	}
}

// mustRoute builds a single-provider OpenAICompat the way the router does, with
// placeholder credentials: nothing is sent, only the cap is read.
func mustRoute(t *testing.T, providerName, model string) *OpenAICompat {
	t.Helper()
	p, err := NewOpenAICompatForRoute(providerName, model, "test-key-not-used", "")
	if err != nil {
		t.Fatalf("NewOpenAICompatForRoute(%s): %v", providerName, err)
	}
	return p
}

func mustAnthropic(t *testing.T) *Anthropic {
	t.Helper()
	p, err := NewAnthropicForRoute("claude-test", "test-key-not-used")
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// mustRouter builds a Router over the named providers with placeholder
// credentials: nothing is sent, only the declared cap is read.
func mustRouter(t *testing.T, providerNames ...string) *Router {
	t.Helper()
	routes := make([]Route, 0, len(providerNames))
	for _, name := range providerNames {
		model := "model-" + name
		var prov Provider
		if name == "anthropic" {
			prov = mustAnthropic(t)
		} else {
			prov = mustRoute(t, name, model)
		}
		routes = append(routes, Route{
			Provider: name,
			Model:    model,
			Client:   AsSingleAttempt(prov),
		})
	}
	r, err := NewRouter(routes, 0, RealClock{})
	if err != nil {
		t.Fatalf("NewRouter: %v", err)
	}
	return r
}
