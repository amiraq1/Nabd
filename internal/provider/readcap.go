package provider

// The read ceiling a provider can accept.
//
// The read cap is the largest single tool result the model is sent in one
// request, so it is bounded by the provider's per-request input ceiling — and
// that ceiling is a property of the provider, not a fact about nabd. NBD-400
// measured what the cap costs (docs/TECH_DEBT.md, READ_CAP_TURN_COST); this is
// where the cap's own value comes from.
//
// Providers declare it through ReadCapper rather than having it inferred from
// their Name(). Name() is a human-facing string and Router's is a composite of
// several providers ("router/a:x→b:y"), so anything parsed out of it would be
// wrong for exactly the multi-provider case. A declared method cannot be.
//
// The declaration is one-directional: a provider that does not implement
// ReadCapper contributes the generous default, because silence is not evidence
// of a tight ceiling. A Router takes the MINIMUM over its routes — the
// strictest route governs, since the next request may land on any of them.

const (
	// GroqReadCapBytes is the cap for providers that meter tokens per minute.
	// Groq's free key allows 8000 TPM (measured live; the derivation this
	// number once came from was deleted in NBD-403 and the value re-measured in
	// NBD-400).
	GroqReadCapBytes = 3072

	// DefaultReadCapBytes is the cap for a provider with no declared ceiling.
	// It is a DECLARED default, not a derived one: no TPM measurement exists
	// for Anthropic, OpenRouter or NVIDIA in this repository. It is the larger
	// value because the constraint it stands in for — a per-minute input
	// ceiling — is absent, leaving the context window as the only bound. It is
	// overridable with NABD_MAX_READ for exactly that reason, and a custom
	// base URL pointed at a metered clone is the case that override exists for.
	DefaultReadCapBytes = 16384
)

// ReadCapper is implemented by providers that know their own read ceiling.
type ReadCapper interface {
	ReadCapBytes() int
}

var (
	_ ReadCapper = (*Anthropic)(nil)
	_ ReadCapper = (*OpenAICompat)(nil)
	_ ReadCapper = (*Router)(nil)
)

// ReadCapBytes reports the cap for Anthropic's Messages API, which does not
// meter tokens per minute on the plans nabd targets.
func (a *Anthropic) ReadCapBytes() int { return DefaultReadCapBytes }

// ReadCapBytes reports this provider's cap. The value is fixed by the
// constructor that built it, so nothing is parsed here.
func (o *OpenAICompat) ReadCapBytes() int {
	if o.readCapBytes > 0 {
		return o.readCapBytes
	}
	return DefaultReadCapBytes
}

// ReadCapBytes reports the strictest cap among the router's routes.
//
// The minimum is the only safe aggregate: every request goes to one of these
// providers and which one is decided by fallback at runtime, so a cap sized for
// the most permissive route would be sent to the strictest one and trip its
// ceiling — the failure this interface exists to avoid.
//
// The cap is read from each route's client, which holds the provider itself.
// Route.Provider is a normalized allow-listed name and route.Client is the
// concrete provider, so no display string is ever parsed.
func (r *Router) ReadCapBytes() int {
	cap := DefaultReadCapBytes
	for _, route := range r.routes {
		rc, ok := route.Client.(ReadCapper)
		if !ok {
			continue
		}
		if n := rc.ReadCapBytes(); n > 0 && n < cap {
			cap = n
		}
	}
	return cap
}

// readCapForRouteName maps a normalized route provider name to its cap. It
// exists for the route constructor, which receives the name rather than a
// config key, and it is an explicit allow-list rather than a pattern: a name
// this switch does not know gets the declared default, never a guess.
func readCapForRouteName(providerName string) int {
	if providerName == "groq" {
		return GroqReadCapBytes
	}
	return DefaultReadCapBytes
}
