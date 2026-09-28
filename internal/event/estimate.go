package event

import "unicode"

// Token-estimation heuristics, moved from the agent's budget logic. They are
// pure functions on text, so they live with the event contract: payload and
// presentation estimate costs without importing the coordinator.

const (
	runesPerTokASCII = 4.0
	runesPerTokOther = 1.6 // Arabic sits near 1.8; 1.6 leans safe
)

// EstimateText guesses token counts without shipping a tokenizer. It errs
// high on purpose: an overestimate compacts a turn too early, an
// underestimate hits a hard API rejection mid-sentence. Arabic is the reason
// the naive chars/4 rule fails — non-ASCII runes cost far more per rune.
func EstimateText(s string) int {
	var a, o int
	for _, r := range s {
		if r < unicode.MaxASCII {
			a++
			continue
		}
		o++
	}
	return int(float64(a)/runesPerTokASCII + float64(o)/runesPerTokOther)
}
