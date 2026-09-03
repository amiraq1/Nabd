// Package agent: budget.go guesses token counts without shipping a tokenizer.
// It errs high on purpose: an overestimate compacts a turn too early, an
// underestimate hits a hard API rejection mid-sentence. Arabic is the reason
// the naive chars/4 rule fails — non-ASCII runes cost far more per rune.
package agent

import (
	"strconv"
	"sync"
	"unicode"

	"nabd/internal/config"
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
	if v := config.Get("NABD_CTX"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 8000 {
			b.Limit = n
		}
	}
	return b
}

func (b *Budget) Usable() int { return b.Limit - b.Reserve }

func (b *Budget) Estimate(ms []provider.Message) int {
	b.mu.Lock()
	r := b.ratio
	b.mu.Unlock()
	return int(float64(EstimateMessages(ms)) * r)
}

// Calibrate folds a real input_tokens count back into the ratio, slowly and
// within bounds, so one odd response cannot make the agent reckless.
func (b *Budget) Calibrate(actual, estimated int) {
	if actual <= 0 || estimated <= 0 {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	obs := float64(actual) / float64(estimated)
	b.ratio = b.ratio*0.7 + obs*0.3
	switch {
	case b.ratio < 0.6:
		b.ratio = 0.6
	case b.ratio > 2:
		b.ratio = 2
	}
}

func (b *Budget) Pressure(ms []provider.Message) float64 {
	return float64(b.Estimate(ms)) / float64(b.Usable())
}
