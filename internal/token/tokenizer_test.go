package token

import "testing"

// TestGoldenVectors: the test ranks table (ranks_test.go) is small enough
// to verify by hand. These assertions pin the merge loop's behavior —
// if the loop regresses, a golden vector breaks.
func TestGoldenVectors(t *testing.T) {
	cases := []struct {
		text string
		want int
	}{
		// "the" → [257]: 't''h'(116,104)→256 first (rank 256 < 260),
		// then 256+'e'→257. One token.
		{"the", 1},
		// "he" → [260]: 'h''e'(104,101)→260. One token.
		{"he", 1},
		// "they" → [257, 121]: 't''h'→256, 256+'e'→257, 'y' stays. Two tokens.
		{"they", 2},
		// "world" → [259, 108, 100]: 'w''o'→258, 258+'r'→259, 'l','d' stay.
		{"world", 3},
		// "the world" → "the"(1) + " "(split) + "world"(3) = 4 tokens.
		{"the world", 4},
		// Empty string → 0 tokens.
		{"", 0},
		// "xyz" has no merges → 3 bytes = 3 tokens.
		{"xyz", 3},
		// Punctuation splits: "he." → "he"(1) + "."(1) = 2 tokens.
		{"he.", 2},
	}
	for _, c := range cases {
		if got := testTok.Count(c.text); got != c.want {
			t.Errorf("Count(%q) = %d, want %d", c.text, got, c.want)
		}
	}
}

// TestHeuristicFallback: the zero value and an unknown registry key both
// resolve to HeuristicTokenizer, which overestimates (safe direction).
func TestHeuristicFallback(t *testing.T) {
	var zero Tokenizer = HeuristicTokenizer{}
	if got := zero.Count("hello"); got <= 0 {
		t.Errorf("HeuristicTokenizer.Count = %d, want > 0", got)
	}

	r := NewRegistry()
	unknown := r.Resolve("anthropic/claude-sonnet-5")
	if _, ok := unknown.(HeuristicTokenizer); !ok {
		t.Errorf("unknown model resolved to %T, want HeuristicTokenizer", unknown)
	}
}

// TestRegistryResolve: a registered model resolves to its tokenizer; a
// nil registration is ignored (no panic, no nil installed).
func TestRegistryResolve(t *testing.T) {
	r := NewRegistry()
	r.Register("anthropic/claude-sonnet-5", testTok)
	if got := r.Resolve("anthropic/claude-sonnet-5"); got != testTok {
		t.Errorf("registered model resolved to wrong tokenizer")
	}
	// Unknown key → heuristic, not nil.
	if got := r.Resolve("openai/gpt-4"); got == nil {
		t.Error("unknown model resolved to nil")
	}
	// Nil registration is a no-op.
	r.Register("openai/gpt-4", nil)
	if _, ok := r.Resolve("openai/gpt-4").(HeuristicTokenizer); !ok {
		t.Error("nil registration should not replace the heuristic fallback")
	}
}

// TestBPENilSafe: a nil *BPETokenizer.Count degrades to the heuristic
// instead of panicking — house rule #1, zero value stays safe.
func TestBPENilSafe(t *testing.T) {
	var t2 *BPETokenizer
	if got := t2.Count("hello"); got <= 0 {
		t.Errorf("nil BPETokenizer.Count = %d, want > 0 (heuristic fallback)", got)
	}
}

// TestArabicCount: the test encoder treats Arabic as raw bytes (no
// Arabic-specific merges), so a 4-rune Arabic word is 8 bytes → 8 tokens
// at the byte level. The point is that Count returns a stable, positive
// number for non-ASCII input — the real accuracy comes from the
// provider-specific table, not this test encoder.
func TestArabicCount(t *testing.T) {
	got := testTok.Count("مرحبا")
	if got <= 0 {
		t.Errorf("Arabic Count = %d, want > 0", got)
	}
}
