// Package token counts tokens. The interface lets a real BPE encoder
// replace the chars/4 heuristic for models we know, while unknown
// models fall back to the heuristic instead of failing.
//
// The zero value of a Tokenizer must be safe to call: HeuristicTokenizer{}
// uses TextHeuristic, which defaults to the chars/4 (ASCII) + chars/1.6
// (non-ASCII) rule. An overestimate is the safe direction (house rule #1:
// the zero value must not grant anything; an overestimate compacts a turn
// too early, an underestimate hits a hard API rejection).
//
// The agent package registers its canonical estimator at startup via
// SetTextHeuristic; until it does, the built-in default (the same rule)
// is used. The indirection breaks the import cycle: agent needs token for
// Budget, and the fallback tokenizer wants agent's estimator.
package token

import "unicode"

// Tokenizer counts the tokens a provider would bill for a string. An
// implementation is allowed to be approximate — the budget calibrates
// against the provider's real prompt_tokens every turn, so a biased
// estimator is corrected within one round.
type Tokenizer interface {
	Count(text string) int
}

// TextHeuristic is the fallback text estimator used by
// HeuristicTokenizer. The default implements the chars/4 (ASCII) +
// chars/1.6 (non-ASCII) rule. The agent package overrides it at startup
// with its canonical implementation (identical rule, single source of
// truth for callers that already import agent).
var TextHeuristic func(string) int = func(s string) int {
	var a, o int
	for _, r := range s {
		if r < unicode.MaxASCII {
			a++
			continue
		}
		o++
	}
	return floatToInt(float64(a)/4.0 + float64(o)/1.6)
}

// SetTextHeuristic overrides the fallback estimator. The agent package
// calls this once at startup. A nil argument resets to the built-in
// default.
func SetTextHeuristic(fn func(string) int) {
	if fn == nil {
		TextHeuristic = func(s string) int {
			var a, o int
			for _, r := range s {
				if r < unicode.MaxASCII {
					a++
					continue
				}
				o++
			}
			return floatToInt(float64(a)/4.0 + float64(o)/1.6)
		}
		return
	}
	TextHeuristic = fn
}

func floatToInt(f float64) int {
	return int(f)
}

// HeuristicTokenizer is the fallback: it uses TextHeuristic. It is what
// an unknown model degrades to — never nil, never an error.
type HeuristicTokenizer struct{}

func (HeuristicTokenizer) Count(text string) int { return TextHeuristic(text) }

// Ensure HeuristicTokenizer satisfies the interface at compile time.
var _ Tokenizer = HeuristicTokenizer{}
