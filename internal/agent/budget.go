// Package agent: budget.go guesses token counts without shipping a tokenizer.
// It errs high on purpose: an overestimate compacts a turn too early, an
// underestimate hits a hard API rejection mid-sentence. Arabic is the reason
// the naive chars/4 rule fails — non-ASCII runes cost far more per rune.
package agent

import (
	"os"
	"strconv"
	"sync"
	"unicode"

	"nabd/internal/provider"
)

const (
	runesPerTokASCII = 4.0
	runesPerTokOther = 1.6 // Arabic sits near 1.8; 1.6 leans safe
	perMessage       = 8   // role framing, delimiters
	perToolCall      = 20
)

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

func EstimateMessages(ms []provider.Message) int {
	n := 0
	for _, m := range ms {
		n += perMessage + EstimateText(m.Text)
		for _, c := range m.ToolCalls {
			n += perToolCall + EstimateText(c.Name) + EstimateText(string(c.Input))
		}
		for _, r := range m.ToolResults {
			n += perMessage + EstimateText(r.Output)
		}
	}
	return n
}

// Budget knows the window and corrects itself. Providers report real input
// counts; one honest number beats any heuristic, so the ratio is learned.
type Budget struct {
	mu      sync.Mutex
	Limit   int // context window
	Reserve int // room for the reply plus the system prompt
	ratio   float64
}

func NewBudget() *Budget {
	b := &Budget{Limit: 120000, Reserve: 16000, ratio: 1}
	if v := os.Getenv("NABD_CTX"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 8000 {
			b.Limit = n
		}
	}
	return b
}

// maxOutputTokens is the output reservation (max_tokens) sent to the
// provider. Experimental value: the implicit 4096 ate half the TPM budget
// on output, starving input. NABD_MAX_TOKENS overrides; values outside
// [minMaxTokens, maxMaxTokens] or non-numeric fall back to the default.
const (
	defaultMaxTokens = 1024
	minMaxTokens     = 128
	maxMaxTokens     = 8192
)

func maxOutputTokens() int {
	if v := os.Getenv("NABD_MAX_TOKENS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= minMaxTokens && n <= maxMaxTokens {
			return n
		}
	}
	return defaultMaxTokens
}

func (b *Budget) Usable() int { return b.Limit - b.Reserve }

func (b *Budget) Estimate(ms []provider.Message) int {
	b.mu.Lock()
	r := b.ratio
	b.mu.Unlock()
	return int(float64(EstimateMessages(ms)) * r)
}

// Ratio exposes the current calibration factor, for journaling.
func (b *Budget) Ratio() float64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.ratio
}

// Calibrate folds a real input_tokens count back into the ratio, slowly and
// within bounds, so one odd response cannot make the agent reckless. It
// returns true when the ratio actually changed, so the caller can journal
// the adopted value — the budget is now session-varying state and every
// state that changes behaviour must be traceable in the log.
func (b *Budget) Calibrate(actual, estimated int) bool {
	if actual <= 0 || estimated <= 0 {
		return false // a provider that reports no usage must not corrupt the ratio
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	obs := float64(actual) / float64(estimated)
	// A wild single measurement is clamped before it can move the ratio far:
	// observations outside [minObsRatio, maxObsRatio] are ignored entirely.
	if obs < minObsRatio || obs > maxObsRatio {
		return false
	}
	next := b.ratio*0.7 + obs*0.3
	if next < 0.6 {
		next = 0.6
	}
	if next > 2 {
		next = 2
	}
	if next == b.ratio {
		return false
	}
	b.ratio = next
	return true
}

const (
	minObsRatio = 0.5 // below this the measurement is not credible (e.g. a 0)
	maxObsRatio = 4.0 // above this the measurement is not credible
)

func (b *Budget) Pressure(ms []provider.Message) float64 {
	return float64(b.Estimate(ms)) / float64(b.Usable())
}
