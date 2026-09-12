package token

import "sync"

// Registry maps a provider/model key to a Tokenizer. An unknown key
// resolves to HeuristicTokenizer{} — the caller always gets something
// that works, never nil. Thread-safe.
type Registry struct {
	mu         sync.RWMutex
	tokenizers map[string]Tokenizer
}

// NewRegistry starts empty: every model resolves to the heuristic until
// one is registered. That is the correct default (degrade, don't fail).
func NewRegistry() *Registry {
	return &Registry{tokenizers: map[string]Tokenizer{}}
}

// Register associates key (convention "provider/model", e.g.
// "anthropic/claude-sonnet-5") with t. A nil t is ignored so a
// misconfigured loader cannot install a nil that would panic on Count.
func (r *Registry) Register(key string, t Tokenizer) {
	if t == nil {
		return
	}
	r.mu.Lock()
	r.tokenizers[key] = t
	r.mu.Unlock()
}

// Resolve returns the tokenizer for key, or HeuristicTokenizer{} if key
// is unknown. Never nil.
func (r *Registry) Resolve(key string) Tokenizer {
	r.mu.RLock()
	t, ok := r.tokenizers[key]
	r.mu.RUnlock()
	if ok {
		return t
	}
	return HeuristicTokenizer{}
}

// ResolveFor is a convenience: split "provider/model" and resolve.
// A name with no "/" resolves to the heuristic — the key convention is
// "provider/model", and a malformed name degrading is the safe failure.
func (r *Registry) ResolveFor(provider, model string) Tokenizer {
	if provider != "" && model != "" {
		return r.Resolve(provider + "/" + model)
	}
	return HeuristicTokenizer{}
}
